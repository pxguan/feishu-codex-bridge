package card

// context_gauge.go —— 上下文用量阈值（对齐 TS card/context-gauge 的纯函数部分）。
// 卡片构造（colorNote/note 等 element）在 cards.go port 后接上；这里先定阈值纯函数。

import "math"

// 阈值（占用窗口的比例）。
const (
	CtxWarn = 0.7  // 🟡 首档可见
	CtxHigh = 0.85 // 🟠
	CtxCrit = 0.95 // 🔴
)

// CtxTier 阈值档位。
type CtxTier struct {
	Level  int    // 0=低于 WARN；1/2/3=yellow/orange/red
	Color  string // green/yellow/orange/red
	Dot    string // 🟢/🟡/🟠/🔴
	Advice string // /compact 建议（level 0 为空）
}

// CtxTierFor 按占用比例返回档位。
func CtxTierFor(frac float64) CtxTier {
	switch {
	case frac >= CtxCrit:
		return CtxTier{Level: 3, Color: "red", Dot: "🔴", Advice: "强烈建议 `/compact` 压缩"}
	case frac >= CtxHigh:
		return CtxTier{Level: 2, Color: "orange", Dot: "🟠", Advice: "建议 `/compact` 压缩"}
	case frac >= CtxWarn:
		return CtxTier{Level: 1, Color: "yellow", Dot: "🟡", Advice: "可考虑 `/compact` 压缩"}
	}
	return CtxTier{Level: 0, Color: "green", Dot: "🟢"}
}

// CtxPercent 占用整百分比（clamp 0..100）；窗口未知返回 ok=false。
func CtxPercent(used int, window *int) (int, bool) {
	if window == nil || *window <= 0 {
		return 0, false
	}
	pct := int(math.Round(float64(used) / float64(*window) * 100))
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// RunCardGaugeVisible 运行卡是否应显示水位 gauge（仅 ≥ WARN）。
func RunCardGaugeVisible(used int, window *int) bool {
	if window == nil || *window <= 0 {
		return false
	}
	return float64(used)/float64(*window) >= CtxWarn
}

// K 紧凑数字（≥1000 → Nk）。
func K(n int) string {
	if n >= 1000 {
		return itoaK(int(math.Round(float64(n)/1000))) + "k"
	}
	if n < 0 {
		n = 0
	}
	return itoaK(n)
}

func itoaK(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for n > 0 {
		p--
		buf[p] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[p:])
}
