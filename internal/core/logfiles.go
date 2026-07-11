package core

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 按天切文件 writer：写到 dir/<YYYY-MM-DD>.log，跨天自动切。
// 文件以 0600 打开（追加）；目录由调用方在 InitFileLogging 时 MkdirAll。
type dailyRotateWriter struct {
	dir string

	mu  sync.Mutex
	day string // 当前持有文件对应的日期 "2006-01-02"
	f   *os.File
}

func (w *dailyRotateWriter) Write(p []byte) (int, error) {
	today := time.Now().Format(dateFormat)
	w.mu.Lock()
	defer w.mu.Unlock()
	if today != w.day {
		if err := w.rotateLocked(today); err != nil {
			return 0, err
		}
	}
	if w.f == nil {
		// 容错：开文件失败时不阻塞日志，丢弃本条。
		return len(p), nil
	}
	return w.f.Write(p)
}

func (w *dailyRotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		return err
	}
	return nil
}

func (w *dailyRotateWriter) rotateLocked(today string) error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	f, err := os.OpenFile(filepath.Join(w.dir, today+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		w.day = ""
		return err
	}
	w.f = f
	w.day = today
	return nil
}

const dateFormat = "2006-01-02"

// TodayLogFile 返回 logsDir 下今天的日志文件绝对路径（不保证存在）。
func TodayLogFile(logsDir string) string {
	return filepath.Join(logsDir, time.Now().Format(dateFormat)+".log")
}

// GCOldLogs 删除 logsDir 下文件名早于 retentionDays 的 *.log（按文件名日期，非 mtime）。
// 返回删除的文件数与错误。retentionDays<=0 时用默认 7。
func GCOldLogs(logsDir string, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		retentionDays = 7
	}
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Format(dateFormat)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(name, ".log")
		if !looksLikeDate(day) {
			continue
		}
		if day < cutoff {
			if err := os.Remove(filepath.Join(logsDir, name)); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func looksLikeDate(s string) bool {
	if len(s) != len(dateFormat) {
		return false
	}
	if _, err := time.Parse(dateFormat, s); err != nil {
		return false
	}
	return true
}

// ReadRecentLogs 读 logsDir 下今天日志的尾部，最多 maxBytes 字节。
// 不足 maxBytes 则补昨天文件的尾部（对齐 TS readRecentLogs 语义）。
// maxBytes<=0 时默认 16KiB。
func ReadRecentLogs(logsDir string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 16 * 1024
	}
	today := time.Now().Format(dateFormat)
	data, err := readTail(filepath.Join(logsDir, today+".log"), maxBytes)
	if err != nil {
		return nil, err
	}
	if len(data) >= maxBytes {
		return data, nil
	}
	// 补昨天。
	yesterday := time.Now().AddDate(0, 0, -1).Format(dateFormat)
	rest := maxBytes - len(data)
	prev, err := readTail(filepath.Join(logsDir, yesterday+".log"), rest)
	if err != nil {
		return data, nil // 昨天文件缺失不算错。
	}
	return append(prev, data...), nil
}

func readTail(path string, maxBytes int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size <= int64(maxBytes) {
		return io.ReadAll(f)
	}
	if _, err := f.Seek(size-int64(maxBytes), io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// ListLogFiles 返回 logsDir 下所有合法日期 *.log，按日期升序（旧→新）。
// 供测试与二期 SSE 日志流使用。
func ListLogFiles(logsDir string) ([]string, error) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var days []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(name, ".log")
		if looksLikeDate(day) {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	return days, nil
}
