package cli

// clibridge_wire.go —— 把 cli-bridge（☕ 咖啡一下）运行时接到 run 的活跃 bot。
// 负责：构造 Service、注入飞书/card/project 依赖、注册卡片回调、启动 IPC server。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/bot"
	"github.com/modelzen/feishu-codex-bridge/internal/card"
	"github.com/modelzen/feishu-codex-bridge/internal/clibridge"
	"github.com/modelzen/feishu-codex-bridge/internal/config"
	"github.com/modelzen/feishu-codex-bridge/internal/feishu"
	"github.com/modelzen/feishu-codex-bridge/internal/project"
)

// ownerCardEntry 维护 owner 卡 message_id → card_id + 单调 seq（CardKit 整卡更新需递增 seq）。
type ownerCardEntry struct {
	cardID string
	seq    int
}

// setupCliBridge 若 cliBridge 启用且可启用，则构造并启动服务；否则返回 (nil, false)。
func setupCliBridge(ctx context.Context, out io.Writer, orch *bot.Orchestrator, ch *feishu.Channel, botCfg config.AppConfig) (*clibridge.Service, bool, error) {
	prefs := config.GetCliBridgePreferences(botCfg)
	owner := config.ResolveOwner(botCfg)
	if !prefs.Enabled || owner == "" {
		return nil, false, nil
	}

	tracker := struct {
		mu sync.Mutex
		m  map[string]*ownerCardEntry
	}{m: map[string]*ownerCardEntry{}}

	genUUID := func() string {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}

	deps := clibridge.ServiceDeps{
		Cfg:        botCfg,
		SocketPath: clibridge.DefaultSocketPath(),
	}
	// SendOwnerCard：建 CardKit 实体 + 发到 owner open_id，记下 message_id→card_id 以便原地更新。
	deps.SendOwnerCard = func(sctx context.Context, c card.CardObject) (string, error) {
		cardJSON, err := json.Marshal(c)
		if err != nil {
			return "", err
		}
		cardID, err := ch.CreateCardKitEntity(sctx, string(cardJSON))
		if err != nil {
			return "", fmt.Errorf("create cardkit entity: %w", err)
		}
		content := fmt.Sprintf(`{"type":"card","data":{"card_id":"%s"}}`, cardID)
		msgID, err := ch.CreateMessageRaw(sctx, "open_id", owner, "interactive", content)
		if err != nil {
			return "", err
		}
		tracker.mu.Lock()
		tracker.m[msgID] = &ownerCardEntry{cardID: cardID, seq: 0}
		tracker.mu.Unlock()
		return msgID, nil
	}
	deps.UpdateOwnerCard = func(sctx context.Context, messageID string, c card.CardObject) bool {
		tracker.mu.Lock()
		e, ok := tracker.m[messageID]
		if !ok {
			tracker.mu.Unlock()
			return false
		}
		e.seq++
		seq := e.seq
		tracker.mu.Unlock()
		cardJSON, err := json.Marshal(c)
		if err != nil {
			return false
		}
		if err := ch.UpdateCardKitEntity(sctx, e.cardID, string(cardJSON), seq, genUUID()); err != nil {
			return false
		}
		return true
	}
	deps.SendGroupTopic = func(sctx context.Context, chatID, markdown string, replyInThread bool) error {
		if replyInThread {
			_, err := ch.SendMarkdownInThread(sctx, chatID, markdown)
			return err
		}
		_, err := ch.SendMarkdown(sctx, chatID, markdown)
		return err
	}
	deps.AddTypingReaction = func(sctx context.Context, messageID string) (string, error) {
		return ch.AddMessageReaction(sctx, messageID, "Typing")
	}
	deps.RemoveTypingReaction = func(sctx context.Context, messageID, reactionID string) error {
		return ch.RemoveMessageReaction(sctx, messageID, reactionID)
	}
	deps.IsBoundProject = func(cwd string) bool {
		projects, err := orch.ProjectStore.List()
		if err != nil {
			return false
		}
		for i := range projects {
			if projects[i].Cwd == cwd {
				return true
			}
		}
		return false
	}
	deps.FindProjectByCwd = func(cwd string) (*clibridge.ProjectRef, error) {
		projects, err := orch.ProjectStore.List()
		if err != nil {
			return nil, err
		}
		for i := range projects {
			if projects[i].Cwd == cwd {
				return &clibridge.ProjectRef{ChatID: projects[i].ChatID, Name: projects[i].Name, Kind: projects[i].Kind}, nil
			}
		}
		return nil, nil
	}
	deps.CreateProjectForCwd = func(cwd, source string) (*clibridge.ProjectRef, error) {
		name := "auto-" + filepath.Base(cwd)
		if _, err := orch.ProjectStore.GetByName(name); err == nil {
			name = name + "-" + strconv.Itoa(int(time.Now().Unix()%100000))
		}
		chatID, err := ch.CreateChat(ctx, name, owner)
		if err != nil {
			return nil, err
		}
		proj := project.Project{
			Name: name, ChatID: chatID, Cwd: cwd, Kind: "multi",
			CreatedAt: time.Now().UnixMilli(), Origin: "created",
		}
		if err := orch.ProjectStore.Add(proj); err != nil {
			return nil, err
		}
		return &clibridge.ProjectRef{ChatID: chatID, Name: name, Kind: "multi"}, nil
	}

	svc := clibridge.CreateCliBridgeService(deps)
	svc.RegisterCardActions(orch.Dispatcher)
	if err := svc.Start(ctx); err != nil {
		return nil, false, fmt.Errorf("cli-bridge 启动失败：%w", err)
	}
	orch.CliBridge = svc
	fmt.Fprintf(out, "☕ cli-bridge（咖啡一下）已启动：%s\n", deps.SocketPath)
	return svc, true, nil
}
