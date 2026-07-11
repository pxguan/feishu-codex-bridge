// update.go —— 检查 GitHub Releases 最新版本（Phase 2 update 命令的底层）。
//
// 设计原则：只做「检查 + 安全下载到临时文件」，不自动替换正在运行的二进制
// （替换运行中的可执行文件风险高、且需平台特定处理，留给用户手动操作）。

package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/config"
)

// DefaultRepo 默认上游仓库（fork 可改；也允许 FCB_UPDATE_REPO 环境变量覆盖）。
const DefaultRepo = "modelzen/feishu-codex-bridge"

// Asset 是 release 的一个附件。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Release 是 GitHub release 的精简视图。
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	HTMLURL string  `json:"html_url"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// Latest 查询仓库最新 release。client 可注入（测试用），传 nil 用默认。
func Latest(ctx context.Context, repo string, client *http.Client) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	if client == nil {
		client = http.DefaultClient
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问 GitHub 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub 返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 release 失败：%w", err)
	}
	return &rel, nil
}

// CompareVersion 比较 a、b 语义版本（忽略前导 v 与预发布后缀）。
// 返回 -1（a<b）/ 0（相等）/ 1（a>b）。
func CompareVersion(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// 去掉预发布/元数据后缀。
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < 3; i++ {
		if i < len(parts) {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[i])); err == nil {
				out[i] = n
			}
		}
	}
	return out
}

// AssetForPlatform 在 release 中匹配当前平台（goos/goarch）的附件。
// 兼容常见命名：darwin/linux/windows + amd64(×86_64)/arm64(aarch64)。
func AssetForPlatform(rel *Release, goos, goarch string) (Asset, bool) {
	archAliases := map[string][]string{
		"amd64": {"amd64", "x86_64", "x64"},
		"arm64": {"arm64", "aarch64"},
		"386":   {"386", "i386", "i686"},
	}
	names := []string{goarch}
	if al, ok := archAliases[goarch]; ok {
		names = al
	}
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if !strings.Contains(lower, strings.ToLower(goos)) {
			continue
		}
		for _, n := range names {
			if strings.Contains(lower, n) {
				return a, true
			}
		}
	}
	return Asset{}, false
}

// CurrentPlatformAsset 用运行平台匹配附件。
func CurrentPlatformAsset(rel *Release) (Asset, bool) {
	return AssetForPlatform(rel, runtime.GOOS, runtime.GOARCH)
}

// DownloadToTemp 把 asset 下载到系统临时目录，返回保存路径。不做任何替换。
func DownloadToTemp(ctx context.Context, a Asset, client *http.Client) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载返回 %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "fcb-update-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", fmt.Errorf("写入失败：%w", err)
	}
	return tmp.Name(), nil
}

// ── 更新互斥锁（B）+ 更新结果状态（D）────────────────────────────────────────
// 都落在 appDir，供跨进程协作：Web「升级」跑在 detached helper 里、私聊「更新」跑在
// daemon 里，两者可能并发。锁防止两个更新流程并发把全局目录装坏；状态文件给
// helper 的结果一个 daemon 能读、Web 能轮询的落点（否则 helper 失败对网页端静默）。
//
// Go 版底层是「下载 GitHub release + 替换二进制」，与 TS 的 npm i -g 不同，但锁/状态的
// 协作语义一致：原子占有（O_EXCL）、pid 存活判定、陈旧回收、状态读/写/清。

// 远超任何合理的更新时长：超时回收只是「pid 被复用成别的进程」的兜底——持有者真的死了
// 会被下面的 processAlive 立刻回收，不靠这个时钟。设太短会把一个装得慢的**存活**更新误判成
// 陈旧、抢锁并发跑两个更新（正是本锁要防的），故取 30min 这种绝不会误伤的大值。
const updateLockStaleMs = 30 * 60 * 1000

func updateLockFile() string {
	return filepath.Join(config.AppDir(), "update.lock")
}

func updateStatusFile() string {
	return filepath.Join(config.AppDir(), "update-status.json")
}

// processAlive 进程是否存活（跨平台尽力而为：unix 用 signal(0)；Windows 无 signal(0)
// 支持，保守返回 true 以防误抢活体锁）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		// EPERM：进程在、只是本进程无权 signal → 仍算活。
		if errno, ok := err.(syscall.Errno); ok && errno == syscall.EPERM {
			return true
		}
		return false
	}
	return true
}

// isUpdateLockStale 锁文件是否陈旧（持有者已死 / 超 30min 崩溃残留）。
func isUpdateLockStale(file string) bool {
	b, err := os.ReadFile(file)
	if err != nil {
		return true // 损坏/读不出 → 可回收
	}
	var rec struct {
		PID int   `json:"pid"`
		At  int64 `json:"at"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return true // 损坏 → 可回收
	}
	if rec.PID <= 0 {
		return true // 无 pid → 损坏，可回收
	}
	if !processAlive(rec.PID) {
		return true // 持有者已死 → 优先回收（不受时钟影响）
	}
	return time.Now().UnixMilli()*1000-rec.At > updateLockStaleMs
}

// AcquireUpdateLock 跨进程互斥：给更新流程上锁，防止 Web「升级」与私聊「更新」并发跑两个
// 全局更新、把同一目录装坏。拿到锁返回 release()；已有**新鲜且存活**的更新在跑则返回
// false（调用方应提示「更新已在进行」并**不**继续）。陈旧锁（持有者已死 / 超 30min 崩溃残留）
// 会被回收。O_EXCL('wx') 原子创建，与单例锁同套路。任何**非**「已存在」的 fs 异常也一律返回
// false（当作拿不到锁）而**不** panic——否则 fire-and-forget 的调用方会 unhandled panic、卡片定格无反馈。
func AcquireUpdateLock() (release func(), ok bool) {
	file := updateLockFile()
	for attempt := 0; attempt < 3; attempt++ {
		fd, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fd.WriteString(fmt.Sprintf(`{"pid":%d,"at":%d}`, os.Getpid(), time.Now().UnixMilli()*1000))
			_ = fd.Close()
			return func() {
				b, rerr := os.ReadFile(file)
				if rerr == nil {
					var rec struct {
						PID int `json:"pid"`
					}
					if json.Unmarshal(b, &rec) == nil && rec.PID == os.Getpid() {
						_ = os.Remove(file)
						return
					}
				}
				// 锁已不属于自己（被回收/换主）→ 别动。
			}, true
		}
		if !os.IsExist(err) {
			return nil, false // EACCES/ENOSPC/… → 当作拿不到锁，绝不 panic
		}
		if !isUpdateLockStale(file) {
			return nil, false // 活体新鲜持有者 → 更新进行中
		}
		_ = os.Remove(file) // 回收陈旧锁后重试 wx 创建
	}
	return nil, false // 连续抢回收都输 → 当作进行中（安全：绝不并发更新）
}

// UpdatePhase 更新阶段。
type UpdatePhase string

const (
	UpdatePhaseInstalling UpdatePhase = "installing"
	UpdatePhaseRestarting UpdatePhase = "restarting"
	UpdatePhaseDone       UpdatePhase = "done"
	UpdatePhaseError      UpdatePhase = "error"
)

// UpdateStatus 一次更新的进度/结果（供 Web 轮询；失败对网页端不再静默）。
type UpdateStatus struct {
	Phase  UpdatePhase `json:"phase"`
	OK     *bool       `json:"ok,omitempty"`
	Message string     `json:"message,omitempty"`
	From   string      `json:"from,omitempty"`
	To     string      `json:"to,omitempty"`
	At     int64       `json:"at"` // epoch ms；Web 端提交升级前会 clear，故读到的即本次结果。
}

var statusMu sync.Mutex

// WriteUpdateStatus 记录更新进度/结果，供 Web 控制台轮询显示（尤其失败）。同步、尽力而为。
func WriteUpdateStatus(status UpdateStatus) {
	statusMu.Lock()
	defer statusMu.Unlock()
	if status.At == 0 {
		status.At = time.Now().UnixMilli() * 1000
	}
	b, err := json.Marshal(status)
	if err != nil {
		return
	}
	_ = os.WriteFile(updateStatusFile(), b, 0o600)
}

// ReadUpdateStatus 读取最近一次更新结果；无记录/损坏返回 nil。绝不 panic。
func ReadUpdateStatus() *UpdateStatus {
	statusMu.Lock()
	defer statusMu.Unlock()
	b, err := os.ReadFile(updateStatusFile())
	if err != nil {
		return nil
	}
	var s UpdateStatus
	if err := json.Unmarshal(b, &s); err != nil {
		return nil
	}
	if s.Phase == "" || s.At == 0 {
		return nil
	}
	return &s
}

// ClearUpdateStatus 清掉上一次的更新结果——Web 提交新升级前调用，之后读到的状态才确定属于本次。
func ClearUpdateStatus() {
	statusMu.Lock()
	defer statusMu.Unlock()
	_ = os.Remove(updateStatusFile())
}
