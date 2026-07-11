package feishu

import "testing"

// TestChannelConnState 验证 ConnState 初始值 + setConnState 驱动的状态机
// （真实连接态由 SDK 的 OnReady/OnReconnecting/OnReconnected/OnDisconnected
// 回调在 Connect 内调用 setConnState；这里直接验证 getter/setter 逻辑）。
func TestChannelConnState(t *testing.T) {
	c := NewChannel("app", "secret", "lark")
	if got := c.ConnState(); got != "disconnected" {
		t.Fatalf("初始应 disconnected，实际 %q", got)
	}

	c.setConnState("connected")
	if got := c.ConnState(); got != "connected" {
		t.Fatalf("connecting 后应为 connected，实际 %q", got)
	}

	c.setConnState("reconnecting")
	if got := c.ConnState(); got != "reconnecting" {
		t.Fatalf("应为 reconnecting，实际 %q", got)
	}

	c.setConnState("disconnected")
	if got := c.ConnState(); got != "disconnected" {
		t.Fatalf("应为 disconnected，实际 %q", got)
	}
}
