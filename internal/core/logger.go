package core

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"sync"
)

// logger.go —— 全局结构化日志器 + trace 上下文传播 + 按天切文件。
//
// 与 TS core/logger 的对齐（见方案 §4）：
//   - JSON 行落盘 logsDir/YYYY-MM-DD.log，按天切换（dailyRotateWriter）。
//   - traceId/chatId/msgId 通过 context.Context 传播，traceHandler 自动注入每条记录。
//     （TS 用 AsyncLocalStorage 隐式传播；Go 用 context 显式传播，更显式可控。）
//   - 三级：Info / Warn / Fail(=slog Error)；Fail 自动带 err。
//   - GCOldLogs 按文件名日期删早于 retention 的；ReadRecentLogs 读今天尾部。
//
// Phase 1 简化点（无外部契约，二期增强）：
//   - stdout 冒泡不做「按 event 前缀 allowlist + emoji 定制格式」，统一 JSON 全量输出到给定 stdout
//     （调用方可传 io.Discard 关闭）。TS 那套 ✓/↻/✗/▸ 终端美化二期再补。

const traceIDLen = 8

var (
	logMu  sync.RWMutex
	defLog = newDefaultLogger() // 进程级默认 logger；InitFileLogging / SetLogger 替换。
)

func newDefaultLogger() *slog.Logger {
	// 默认：JSON → stdout，级别 Info，未带 trace。
	return slog.New(traceHandler{Handler: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})})
}

// L 返回进程级默认 logger。
func L() *slog.Logger {
	logMu.RLock()
	defer logMu.RUnlock()
	return defLog
}

// SetLogger 替换进程级默认 logger（测试注入用）。
func SetLogger(l *slog.Logger) {
	logMu.Lock()
	defer logMu.Unlock()
	if l != nil {
		defLog = l
	}
}

// InitFileLogging 配置「文件 + stdout」双通道日志：
//   - logsDir 非空 → 落盘 logsDir/YYYY-MM-DD.log（按天切，0600）。
//   - stdout 非空 → 同时写 stdout（可传 io.Discard 关闭终端）。
//
// 返回装配好的 logger（同时设为进程默认）。
//
// 调用方典型：InitFileLogging(paths.LogsDir(), os.Stdout)。
func InitFileLogging(logsDir string, stdout io.Writer) (*slog.Logger, error) {
	var writers []io.Writer
	if stdout != nil {
		writers = append(writers, stdout)
	}
	if logsDir != "" {
		if err := os.MkdirAll(logsDir, 0o755); err != nil {
			return nil, err
		}
		writers = append(writers, &dailyRotateWriter{dir: logsDir})
	}
	var w io.Writer = io.Discard
	if len(writers) == 1 {
		w = writers[0]
	} else if len(writers) > 1 {
		w = io.MultiWriter(writers...)
	}
	l := slog.New(traceHandler{Handler: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})})
	SetLogger(l)
	return l, nil
}

// ── trace 上下文 ───────────────────────────────────────────────

type ctxKey int

const (
	ckTrace ctxKey = iota
	ckChat
	ckMsg
)

// NewTraceID 生成 8 字符随机 traceId（[0-9a-z]）。
// 对应 TS newTraceId = Math.random().toString(36).slice(2,10)；改用 crypto/rand 更稳。
func NewTraceID() string {
	var raw [traceIDLen]byte
	var out [traceIDLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto_rand 极少失败；退化用时间戳兜底，保证始终返回非空 8 字符。
		return "00000000"
	}
	for i := range out {
		out[i] = lex36[int(raw[i])%36]
	}
	return string(out[:])
}

const lex36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// WithTrace 把 traceId/chatId/msgId 注入 ctx；空串不覆盖（允许链式部分更新）。
func WithTrace(ctx context.Context, traceID, chatID, msgID string) context.Context {
	if traceID != "" {
		ctx = context.WithValue(ctx, ckTrace, traceID)
	}
	if chatID != "" {
		ctx = context.WithValue(ctx, ckChat, chatID)
	}
	if msgID != "" {
		ctx = context.WithValue(ctx, ckMsg, msgID)
	}
	return ctx
}

// TraceFields 取出 ctx 里的 trace 三元组；都没有则 ok=false。
func TraceFields(ctx context.Context) (traceID, chatID, msgID string, ok bool) {
	traceID, _ = ctx.Value(ckTrace).(string)
	chatID, _ = ctx.Value(ckChat).(string)
	msgID, _ = ctx.Value(ckMsg).(string)
	ok = traceID != "" || chatID != "" || msgID != ""
	return
}

// traceHandler 包装底层 Handler，在每条记录上自动追加 ctx 里的 traceId/chatId/msgId。
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if traceID, chatID, msgID, ok := TraceFields(ctx); ok {
		if traceID != "" {
			r.AddAttrs(slog.String("traceId", traceID))
		}
		if chatID != "" {
			r.AddAttrs(slog.String("chatId", chatID))
		}
		if msgID != "" {
			r.AddAttrs(slog.String("msgId", msgID))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}
func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// ── 便捷封装（对齐 TS log.info/warn/fail 的 event+phase+msg 习惯）────────

func Info(ctx context.Context, event, phase, msg string, attrs ...slog.Attr) {
	logAttrs(ctx, slog.LevelInfo, event, phase, msg, nil, attrs)
}
func Warn(ctx context.Context, event, phase, msg string, attrs ...slog.Attr) {
	logAttrs(ctx, slog.LevelWarn, event, phase, msg, nil, attrs)
}
func Fail(ctx context.Context, event, phase string, err error, attrs ...slog.Attr) {
	logAttrs(ctx, slog.LevelError, event, phase, err.Error(), err, attrs)
}

func logAttrs(ctx context.Context, lvl slog.Level, event, phase, msg string, err error, attrs []slog.Attr) {
	all := make([]slog.Attr, 0, len(attrs)+4)
	all = append(all, slog.String("event", event), slog.String("phase", phase))
	if err != nil {
		all = append(all, slog.Any("err", err))
	}
	all = append(all, attrs...)
	L().LogAttrs(ctx, lvl, msg, all...)
}
