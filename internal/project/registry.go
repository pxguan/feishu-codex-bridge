package project

// registry.go —— 项目（飞书群↔cwd）注册表（对齐 TS project/registry）。
// 纯数据层：Project 结构 + 纯函数（权限档/免@）+ Store CRUD（并发原子写）。
// 不依赖飞书 SDK（建群/公告等在 lifecycle/onboarding，飞书 SDK 就绪后实现）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/modelzen/feishu-codex-bridge/internal/agent"
)

// Project 一个项目 = 一个飞书群绑定的固定工作目录。
type Project struct {
	Name          string                `json:"name"`   // 唯一项目名（=群名）
	ChatID        string                `json:"chatId"` // 绑定的飞书群 chat_id（oc_xxx）
	Cwd           string                `json:"cwd"`    // codex 工作目录（绝对）
	Blank         bool                  `json:"blank"`  // bridge 在 projectsRootDir 下新建的空项目
	CreatedAt     int64                 `json:"createdAt"`
	Branch        string                `json:"branch,omitempty"`
	Kind          string                `json:"kind,omitempty"`      // multi(默认)|single
	NoMention     *bool                 `json:"noMention,omitempty"` // nil=用 DefaultNoMention
	Origin        string                `json:"origin,omitempty"`    // created(默认)|joined
	AddedBy       string                `json:"addedBy,omitempty"`
	AllowedUsers  []string              `json:"allowedUsers,omitempty"` // 响应白名单；空=所有人
	Mode          agent.PermissionMode  `json:"mode,omitempty"`         // nil/空=full
	GuestMode     agent.PermissionMode  `json:"guestMode,omitempty"`    // 空=同 Mode
	Network       *bool                 `json:"network,omitempty"`
	AutoCompact   *bool                 `json:"autoCompact,omitempty"` // nil=默认 on
	Backend       string                `json:"backend,omitempty"`     // 空=codex 默认
	DefaultModel  string                `json:"defaultModel,omitempty"`
	DefaultEffort agent.ReasoningEffort `json:"defaultEffort,omitempty"`
	SourceURL     string                `json:"sourceUrl,omitempty"`   // 关联的云文档 URL（评论回复定位项目用）
}

// DefaultNoMention 免@默认：除「joined + single」组合外都开。
// （joined 群 + 单会话 = 整个群每条消息都跑 codex，太激进 → 默认关。）
func DefaultNoMention(p Project) bool {
	origin := p.Origin
	if origin == "" {
		origin = "created"
	}
	kind := p.Kind
	if kind == "" {
		kind = "multi"
	}
	return !(origin == "joined" && kind == "single")
}

// EffectiveMode 权限档：空=full（保留历史 danger-full-access）。
func EffectiveMode(p Project) agent.PermissionMode {
	if p.Mode == "" {
		return agent.PermissionFull
	}
	return p.Mode
}

// EffectiveGuestMode 非管理员档：空=同 EffectiveMode（不分级）。
func EffectiveGuestMode(p Project) agent.PermissionMode {
	if p.GuestMode != "" {
		return p.GuestMode
	}
	return EffectiveMode(p)
}

// TurnTier 按 sender 是否 admin 解析权限档 + role + 是否分级。
// split=true 时 admin/guest 跑独立 codex thread（沙箱 + 会话历史都按 role 隔离）。
func TurnTier(p Project, isAdminSender bool) (mode agent.PermissionMode, role string, split bool) {
	admin := EffectiveMode(p)
	guest := EffectiveGuestMode(p)
	if isAdminSender {
		mode, role = admin, "admin"
	} else {
		mode, role = guest, "guest"
	}
	split = guest != admin
	return
}

// ── Store（CRUD + 并发原子写）─────────────────────────────────────

const fileVersion = 1

type storeFile struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// Store 项目注册表（path 注入；mutex 串行 read-modify-write）。
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore 构造（path = config.BotProjectsFile(appID)）。
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path 返回存储文件路径。
func (s *Store) Path() string { return s.path }

func (s *Store) read() ([]Project, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sf storeFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, err
	}
	return sf.Projects, nil
}

func (s *Store) write(projects []Project) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Version: fileVersion, Projects: projects}, "", "  ")
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

// List 全部项目。
func (s *Store) List() ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// GetByChatID 按群 chat_id 查；未找到返回 (nil, nil)。
func (s *Store) GetByChatID(chatID string) (*Project, error) {
	projects, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].ChatID == chatID {
			return &projects[i], nil
		}
	}
	return nil, nil
}

// GetByName 按项目名查。
func (s *Store) GetByName(name string) (*Project, error) {
	projects, err := s.List()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i], nil
		}
	}
	return nil, nil
}

// ErrProjectNameExists 项目名已存在。
var ErrProjectNameExists = errors.New("project: name already exists")

// ErrChatAlreadyBound 该群已绑定其它项目。
var ErrChatAlreadyBound = errors.New("project: chat already bound")

// Add 新增项目。name 唯一 + chatId 不重复（注册表级硬守卫，防一群双绑）。
func (s *Store) Add(p Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, err := s.read()
	if err != nil {
		return err
	}
	for _, x := range projects {
		if x.Name == p.Name {
			return fmt.Errorf("%w: %s", ErrProjectNameExists, p.Name)
		}
	}
	if p.ChatID != "" {
		for _, x := range projects {
			if x.ChatID == p.ChatID {
				return fmt.Errorf("%w: %s（已绑定 %s）", ErrChatAlreadyBound, p.ChatID, x.Name)
			}
		}
	}
	projects = append(projects, p)
	return s.write(projects)
}

// Update 按名 patch（函数式指针修改，基于最新盘值）；不存在 no-op。
func (s *Store) Update(name string, fn func(*Project)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, err := s.read()
	if err != nil {
		return err
	}
	for i := range projects {
		if projects[i].Name == name {
			fn(&projects[i])
			return s.write(projects)
		}
	}
	return nil
}

// Remove 按名删除（解绑），返回被删项；不存在返回 (nil, nil)。
func (s *Store) Remove(name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	projects, err := s.read()
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].Name == name {
			removed := projects[i]
			projects = append(projects[:i], projects[i+1:]...)
			if err := s.write(projects); err != nil {
				return nil, err
			}
			return &removed, nil
		}
	}
	return nil, nil
}

// ListProjectsIn 读指定 projects.json（跨 bot 聚合用，ENOENT 返回空）。
func ListProjectsIn(path string) ([]Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sf storeFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, err
	}
	return sf.Projects, nil
}
