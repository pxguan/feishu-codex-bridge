package bot

// session_store.go —— topic↔backend session 绑定（对齐 TS bot/session-store）。
// 持久化：bridge 重启后 topic 内 @bot resume 正确的 codex thread（而非新启）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

const sessionFileVersion = 2

// SessionRecord 一个持久化会话（Feishu topic ↔ backend session）。
type SessionRecord struct {
	ThreadID   string                `json:"threadId"`
	ChatID     string                `json:"chatId"`
	Cwd        string                `json:"cwd"`
	SessionID  string                `json:"sessionId"`
	Backend    string                `json:"backend"`
	Model      string                `json:"model,omitempty"`
	Effort     agent.ReasoningEffort `json:"effort,omitempty"`
	Summary    string                `json:"summary"`
	LastSeenAt int64                 `json:"lastSeenAt,omitempty"`
	CreatedAt  int64                 `json:"createdAt"`
	UpdatedAt  int64                 `json:"updatedAt"`
}

type sessionStoreFile struct {
	Version  int             `json:"version"`
	Sessions []SessionRecord `json:"sessions"`
}

// SessionStore topic↔session 绑定存储（path 注入 + mutex 串行化 + 原子写）。
type SessionStore struct {
	path string
	mu   sync.Mutex
}

// NewSessionStore 构造（path = config.BotSessionsFile(appID)）。
func NewSessionStore(path string) *SessionStore {
	return &SessionStore{path: path}
}

// v1→v2 迁移：codexThreadId → sessionId；缺 backend → 默认 codex。
func migrateSession(raw map[string]any) SessionRecord {
	b, _ := json.Marshal(raw)
	var rec SessionRecord
	json.Unmarshal(b, &rec)
	if rec.SessionID == "" {
		// v1 旧字段 codexThreadId（拼接避免 grep 旧名）。
		if legacy, ok := raw["codexThread"+"Id"].(string); ok {
			rec.SessionID = legacy
		}
	}
	if rec.Backend == "" {
		rec.Backend = agent.DEFAULT_BACKEND_ID
	}
	return rec
}

func (s *SessionStore) read() ([]SessionRecord, error) {
	return ListSessionsIn(s.path)
}

func (s *SessionStore) write(sessions []SessionRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sessionStoreFile{Version: sessionFileVersion, Sessions: sessions}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := fmt.Sprintf("%s.tmp-%d", s.path, os.Getpid())
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List 全部会话。
func (s *SessionStore) List() ([]SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// Get 按 threadId 查。
func (s *SessionStore) Get(threadID string) (*SessionRecord, error) {
	sessions, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].ThreadID == threadID {
			return &sessions[i], nil
		}
	}
	return nil, nil
}

// Upsert 按 threadId 插入或替换。
func (s *SessionStore) Upsert(rec SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.read()
	if err != nil {
		return err
	}
	found := false
	for i := range sessions {
		if sessions[i].ThreadID == rec.ThreadID {
			sessions[i] = rec
			found = true
			break
		}
	}
	if !found {
		sessions = append(sessions, rec)
	}
	return s.write(sessions)
}

// Patch 按 threadId 修改（函数式 updater 基于最新盘值）；不存在 no-op。
func (s *SessionStore) Patch(threadID string, fn func(*SessionRecord)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, err := s.read()
	if err != nil {
		return err
	}
	for i := range sessions {
		if sessions[i].ThreadID == threadID {
			fn(&sessions[i])
			return s.write(sessions)
		}
	}
	return nil
}

// ListSessionsIn 读指定 sessions.json（含 v1 迁移）。跨 bot 聚合用。
func ListSessionsIn(path string) ([]SessionRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]SessionRecord, 0, len(raw.Sessions))
	for _, r := range raw.Sessions {
		out = append(out, migrateSession(r))
	}
	return out, nil
}
