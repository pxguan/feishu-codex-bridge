package codex

import "encoding/json"

// protocol.go —— codex app-server JSON-RPC 协议的精简类型（仅 event-map 实际用到的子集）。
//
// 完整 generated 协议（450+ 类型，由 `codex app-server generate-ts` 产出）无需逐类型平移：
// Go 侧只把 event-map 与 app-server-client 实际用到的 method/字段做成 struct。
// ServerNotification.Params 用 json.RawMessage 持有，由 MapNotification 按 method 二次解析。

// ServerNotification codex app-server 推来的通知（有 method 无 id）。
type ServerNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// ThreadItem codex turn 里的一个条目（agentMessage/reasoning/commandExecution/fileChange/...）。
// 单 struct + Type 标签 + 分支字段（松散，按 Type 取用）。
// Content 是多态：reasoning=[]string、userMessage=[]UserContentPart，故用 json.RawMessage
// + ReasoningContent()/UserText() helper 按需解析。
type ThreadItem struct {
	ID               string             `json:"id"`
	Type             string             `json:"type"`                       // agentMessage|reasoning|commandExecution|fileChange|webSearch|mcpToolCall|dynamicToolCall|userMessage
	Text             string             `json:"text,omitempty"`             // agentMessage
	Content          json.RawMessage    `json:"content,omitempty"`          // reasoning: []string; userMessage: []UserContentPart
	Summary          []string           `json:"summary,omitempty"`          // reasoning（摘要）
	Command          string             `json:"command,omitempty"`          // commandExecution
	Cwd              string             `json:"cwd,omitempty"`              // commandExecution
	AggregatedOutput string             `json:"aggregatedOutput,omitempty"` // commandExecution
	ExitCode         *int               `json:"exitCode,omitempty"`         // commandExecution
	Changes          []FileUpdateChange `json:"changes,omitempty"`          // fileChange
	// mapTurn 扩展（thread/read 历史条目用）
	Status  string `json:"status,omitempty"`  // commandExecution/fileChange/mcpToolCall/dynamicToolCall
	Query   string `json:"query,omitempty"`   // webSearch
	Server  string `json:"server,omitempty"`  // mcpToolCall
	Tool    string `json:"tool,omitempty"`    // mcpToolCall/dynamicToolCall
	Err     string `json:"error,omitempty"`   // mcpToolCall
	Success *bool  `json:"success,omitempty"` // dynamicToolCall
}

// Turn thread/read 返回的一轮（含 items + 元数据）。
type Turn struct {
	Items     []ThreadItem `json:"items"`
	StartedAt int64        `json:"startedAt"`
	Name      string       `json:"name"`
	Preview   string       `json:"preview"`
	CreatedAt int64        `json:"createdAt"`
	UpdatedAt int64        `json:"updatedAt"`
}

// UserContentPart userMessage 的 content 片段。
type UserContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// FileUpdateChange 一个文件的改动。
// Kind 在线上是 string 或 {type:string}（serde tag），故用 json.RawMessage 自行解析。
type FileUpdateChange struct {
	Path string          `json:"path"`
	Diff string          `json:"diff"`
	Kind json.RawMessage `json:"kind"`
}

// GoalUpdateParams thread/goal/updated 的 goal 字段。
type GoalUpdateParams struct {
	Status          string `json:"status"`
	Objective       string `json:"objective"`
	TokensUsed      int    `json:"tokensUsed"`
	TimeUsedSeconds int    `json:"timeUsedSeconds"`
	TokenBudget     *int   `json:"tokenBudget"`
}

// ── 各 method 的 params 解析形态 ─────────────────────────────────

type pThreadID struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}
type pTurnID struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}
type pItemDelta struct {
	ItemID string `json:"itemId"`
	Delta  string `json:"delta"`
}
type pItem struct {
	Item ThreadItem `json:"item"`
}
type pTokenUsage struct {
	TokenUsage struct {
		Last struct {
			TotalTokens int `json:"totalTokens"`
		} `json:"last"`
		ModelContextWindow *int `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}
type pGoal struct {
	Goal GoalUpdateParams `json:"goal"`
}
type pError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
	WillRetry bool `json:"willRetry"`
}
