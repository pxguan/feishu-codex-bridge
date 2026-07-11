package feishu

import "testing"

func TestNewChannel(t *testing.T) {
	c := NewChannel("cli_test", "secret", "feishu")
	if c.AppID != "cli_test" || c.AppSecret != "secret" || c.Tenant != "feishu" {
		t.Fatalf("channel fields wrong: %+v", c)
	}
}

func TestLarkClient_LazyBuild(t *testing.T) {
	c := NewChannel("cli_test", "secret", "feishu")
	cli := c.LarkClient()
	if cli == nil {
		t.Fatal("LarkClient should not be nil after first call")
	}
	// 第二次调返回同一个（非 nil）。
	cli2 := c.LarkClient()
	if cli2 == nil {
		t.Fatal("second LarkClient should not be nil")
	}
}

func TestChannel_ShutdownIdempotent(t *testing.T) {
	c := NewChannel("cli_test", "secret", "feishu")
	c.Shutdown() // 未连接 → 不 panic
	c.Shutdown() // 幂等
}
