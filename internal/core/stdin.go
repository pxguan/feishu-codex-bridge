package core

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// stdin.go —— stdin 读取工具（hook 子命令接收 payload、交互式 prompt）。

// ReadAllStdin 读完 stdin 全部字节。hook 子命令用：codex/claude hook 把 payload 写到
// bridge 进程的 stdin，bridge 一次性读完再解析。
func ReadAllStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}

// ReadLine 读一行（去掉末尾换行）。交互式提示用（bot init / doctor）。
// 遇 EOF 返回已读部分（可能为空）。
func ReadLine() (string, error) {
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
