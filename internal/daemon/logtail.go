package daemon

import (
	"bufio"
	"context"
	"os"
	"strings"
	"time"
)

// tailLines 返回文件最后 n 行（n<=0 表示全部）。用于 `daemon logs` 静态查看。
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// FollowLogs 持续把日志新增行推到返回的 channel，直到 ctx 取消。
// 支持日志轮转（文件被截断时回到文件头重新读）。
//
// 用 bufio.Reader 逐字节读并手动拼行——不能用 bufio.Scanner：Scanner 一旦
// 读到 EOF 就永久停止，无法在文件后续追加时继续读取（tailing 必需）。
func (m *Manager) FollowLogs(ctx context.Context) (<-chan string, error) {
	f, err := os.Open(m.LogFile)
	if err != nil {
		return nil, err
	}
	out := make(chan string, 64)
	go func() {
		defer close(out)
		defer f.Close()
		if _, err := f.Seek(0, 2); err != nil { // 从末尾开始
			return
		}
		reader := bufio.NewReader(f)
		var line []byte
		const maxLine = 1 << 20
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		emit := func(s string) bool {
			if s == "" {
				return true
			}
			select {
			case out <- s:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for {
			b, rerr := reader.ReadByte()
			if rerr == nil {
				if b == '\n' {
					if !emit(strings.TrimRight(string(line), "\r")) {
						return
					}
					line = line[:0]
				} else if len(line) < maxLine {
					line = append(line, b)
				}
				continue
			}
			// 读到 EOF / 错误：处理日志轮转（文件被截断时回到文件头）。
			if fi, serr := f.Stat(); serr == nil {
				if pos, _ := f.Seek(0, 1); pos > fi.Size() {
					if _, serr2 := f.Seek(0, 0); serr2 == nil {
						reader.Reset(f)
						line = line[:0]
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return out, nil
}
