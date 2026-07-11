package claude

// history.go —— 读 ~/.claude/projects/<cwd-hash>/*.jsonl 历史（港口 TS claude-agent/history）。
//
// 与 codex（app-server thread/read RPC）不同，claude 的历史是本地 JSONL 转录：
// 会话按 cwd 哈希分目录，文件名为 <sessionId>.jsonl，与 `claude -r` 同源（双向可见）。
// 这里用「按文件名找 sessionId + 按首行 cwd 过滤项目」的方式，避免复刻 cwd→哈希算法。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// claudeHistoryMessage 一行 JSONL 转录的形态。
type claudeHistoryMessage struct {
	Type        string `json:"type"` // session | user | assistant | summary
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Summary     string `json:"summary"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	CreatedAt    int64 `json:"createdAt"`
	LastModified int64 `json:"lastModified"`
}

func claudeProjectsDir() string {
	return filepath.Join(HomeDir(), ".claude", "projects")
}

func findSessionFile(sessionID string) string {
	matches, _ := filepath.Glob(filepath.Join(claudeProjectsDir(), "*", sessionID+".jsonl"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// ListThreads 列出目标 cwd 下的最近会话（newest first）。
func listClaudeSessions(cwd string, limit int) ([]agent.ThreadSummary, error) {
	files, err := filepath.Glob(filepath.Join(claudeProjectsDir(), "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []agent.ThreadSummary
	for _, f := range files {
		sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		mcwd, created, modified, preview := scanSessionMeta(f)
		// 仅列目标 cwd 的会话（resume 同项目才可见）。
		if cwd != "" && mcwd != "" && mcwd != cwd {
			continue
		}
		updated := modified
		if updated == 0 {
			updated = created
		}
		if updated == 0 {
			updated = info.ModTime().Unix()
		}
		out = append(out, agent.ThreadSummary{
			SessionID: sid,
			Preview:   preview,
			CreatedAt: created,
			UpdatedAt: updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// scanSessionMeta 轻量扫描：取 session 行的 cwd/时间戳 + summary/首条用户文本作预览。
func scanSessionMeta(f string) (cwd string, created, modified int64, preview string) {
	file, err := os.Open(f)
	if err != nil {
		return
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m claudeHistoryMessage
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		switch m.Type {
		case "session":
			cwd = m.Cwd
			if created == 0 {
				created = m.CreatedAt
			}
			if modified == 0 {
				modified = m.LastModified
			}
		case "summary":
			if preview == "" {
				preview = m.Summary
			}
		case "user":
			if preview == "" {
				if t := firstUserText(m.Message.Content); t != "" {
					preview = t
				}
			}
		}
	}
	return
}

// ReadHistory 回看某会话的转写摘要。
func readClaudeHistory(cwd, sessionID string, maxTurns int) (agent.ThreadHistory, error) {
	f := findSessionFile(sessionID)
	if f == "" {
		return agent.ThreadHistory{}, nil
	}
	msgs, err := readAllMessages(f)
	if err != nil {
		return agent.ThreadHistory{}, err
	}
	return foldSessionMessages(msgs, maxTurns, cwd), nil
}

func readAllMessages(f string) ([]claudeHistoryMessage, error) {
	file, err := os.Open(f)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []claudeHistoryMessage
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m claudeHistoryMessage
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// foldSessionMessages 把有序转录折成可渲染轮次（user→assistant + reasoning + tools）。
// 逻辑港口 TS history.ts 的 foldSessionMessages。
func foldSessionMessages(msgs []claudeHistoryMessage, maxTurns int, cwd string) agent.ThreadHistory {
	var turns []agent.HistoryTurn
	var cur *agent.HistoryTurn
	var curTools []*agent.HistoryTool
	toolById := map[string]*agent.HistoryTool{}

	flush := func() {
		if cur != nil && (cur.UserText != "" || cur.AssistantText != "" || len(curTools) > 0) {
			cur.Tools = make([]agent.HistoryTool, len(curTools))
			for i, tp := range curTools {
				cur.Tools[i] = *tp
			}
			turns = append(turns, *cur)
		}
		cur = nil
		curTools = nil
		toolById = map[string]*agent.HistoryTool{}
	}
	ensure := func() *agent.HistoryTurn {
		if cur == nil {
			cur = &agent.HistoryTurn{}
		}
		return cur
	}

	for _, msg := range msgs {
		if msg.Type != "user" && msg.Type != "assistant" {
			continue
		}
		blocks := parseBlocks(msg.Message.Content)
		if msg.Type == "user" {
			var texts []string
			attached := false
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" && !isBoilerplateUserText(b.Text) {
					texts = append(texts, b.Text)
				} else if b.Type == "tool_result" && b.ToolUseID != "" {
					if tool, ok := toolById[b.ToolUseID]; ok {
						tool.Output = toolResultText(b.Content)
						if b.IsError {
							tool.Failed = true
						}
						attached = true
					}
				}
			}
			userText := strings.TrimSpace(strings.Join(texts, "\n"))
			if userText != "" {
				flush()
				ensure().UserText = userText
			} else if attached {
				ensure()
			}
		} else { // assistant
			c := ensure()
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					if c.AssistantText != "" {
						c.AssistantText += "\n\n"
					}
					c.AssistantText += b.Text
				} else if (b.Type == "thinking" || b.Type == "redacted_thinking") && b.Thinking != "" {
					if c.Reasoning != "" {
						c.Reasoning += "\n\n"
					}
					c.Reasoning += b.Thinking
				} else if b.Type == "tool_use" || b.Type == "server_tool_use" {
					tp := &agent.HistoryTool{Title: toolTitle(b.Name, b.Input, cwd)}
					curTools = append(curTools, tp)
					if b.ID != "" {
						toolById[b.ID] = tp
					}
				}
			}
		}
	}
	flush()

	total := len(turns)
	kept := turns
	if total > maxTurns {
		kept = turns[total-maxTurns:]
	}
	return agent.ThreadHistory{Turns: kept, TotalTurns: total}
}

func parseBlocks(content json.RawMessage) []contentBlock {
	if len(content) == 0 {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	return blocks
}

func firstUserText(content json.RawMessage) string {
	blocks := parseBlocks(content)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// isBoilerplateUserText 跳过 claude 注入的样板（不显示为「用户消息」）。
func isBoilerplateUserText(text string) bool {
	t := strings.TrimLeft(text, " \t\n\r")
	return strings.HasPrefix(t, "<environment_context>") ||
		strings.HasPrefix(t, "# AGENTS.md") ||
		strings.HasPrefix(t, "<system-reminder>") ||
		strings.HasPrefix(t, "Caveat:")
}
