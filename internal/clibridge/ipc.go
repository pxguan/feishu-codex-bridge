package clibridge

// ipc.go —— Unix socket IPC（对齐 TS cli-bridge/ipc.ts）。
// agent hook 进程与本 daemon 间通过 Unix domain socket 通信：
//   - 每连接一条请求（首行 JSON），daemon 处理后回一行 JSON。
//   - 服务端在 handleMessage 阻塞（等飞书点击/本机回归/超时）期间，连接保持打开；
//     shutdown 时先 destroy 在途连接，避免挂死的 hook 卡住优雅退出。

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// DefaultSocketPath 返回 cli-bridge IPC socket 的稳定路径（run 的 server 与 hook 的
// client 共用，保证同用户下一致）。优先用系统缓存目录，失败回退 /tmp。
func DefaultSocketPath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	base := filepath.Join(dir, "feishu-codex-bridge")
	_ = os.MkdirAll(base, 0o700)
	return filepath.Join(base, "clibridge.sock")
}

// CliBridgeIpcServer IPC server（持有在途连接）。
type CliBridgeIpcServer struct {
	listener net.Listener
	sockets  sync.Map // *net.UnixConn
	closed   bool
}

// StartCliBridgeIpcServer 启 IPC server。handleMessage 处理每条 hook 消息。
func StartCliBridgeIpcServer(socketPath string, handleMessage func(msg CliHookMessage) (CliHookResponse, error)) (*CliBridgeIpcServer, error) {
	if runtime.GOOS != "windows" {
		_ = os.Remove(socketPath)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(socketPath, 0o600); err != nil {
			_ = listener.Close()
			return nil, err
		}
	}
	srv := &CliBridgeIpcServer{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			uc, ok := conn.(*net.UnixConn)
			if !ok {
				_ = conn.Close()
				continue
			}
			srv.sockets.Store(uc, struct{}{})
			go srv.serveConn(uc, handleMessage)
		}
	}()
	return srv, nil
}

func (s *CliBridgeIpcServer) serveConn(conn *net.UnixConn, handleMessage func(msg CliHookMessage) (CliHookResponse, error)) {
	defer func() {
		s.sockets.Delete(conn)
		_ = conn.Close()
	}()
	_ = conn.SetReadDeadline(time.Now().Add(25 * time.Hour))
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	handled := false
	for {
		if handled {
			break
		}
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if idx := indexByte(buf, '\n'); idx >= 0 {
				line := string(buf[:idx])
				handled = true
				resp := handleAndSerialize(line, handleMessage)
				_, _ = conn.Write([]byte(resp + "\n"))
			}
		}
		if err != nil {
			return
		}
	}
}

func handleAndSerialize(line string, handleMessage func(msg CliHookMessage) (CliHookResponse, error)) string {
	var msg CliHookMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		resp := CliHookResponse{Decision: DecisionFallbackLocal, Reason: err.Error()}
		b, _ := json.Marshal(resp)
		return string(b)
	}
	resp, err := handleMessage(msg)
	if err != nil {
		resp = CliHookResponse{Decision: DecisionFallbackLocal, Reason: err.Error()}
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// Close 关闭 server（先 destroy 在途连接）。
func (s *CliBridgeIpcServer) Close() error {
	s.closed = true
	s.sockets.Range(func(key, _ any) bool {
		if uc, ok := key.(*net.UnixConn); ok {
			_ = uc.Close()
		}
		s.sockets.Delete(key)
		return true
	})
	return s.listener.Close()
}

// SendCliHookMessage 客户端：连 socket、发一条 JSON、读回一行 JSON。
func SendCliHookMessage(socketPath string, msg CliHookMessage) (CliHookResponse, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return CliHookResponse{}, err
	}
	defer conn.Close()
	uc := conn.(*net.UnixConn)
	_ = uc.SetDeadline(time.Now().Add(25 * time.Hour))
	data, _ := json.Marshal(msg)
	if _, err := uc.Write(append(data, '\n')); err != nil {
		return CliHookResponse{}, err
	}
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	for {
		n, err := uc.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if idx := indexByte(buf, '\n'); idx >= 0 {
				var resp CliHookResponse
				if err := json.Unmarshal(buf[:idx], &resp); err != nil {
					return CliHookResponse{}, err
				}
				return resp, nil
			}
		}
		if err != nil {
			return CliHookResponse{}, err
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
