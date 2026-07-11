package cli

// update.go —— Phase 2 update 命令（检查 GitHub Releases 最新版）。
// 安全策略：默认只检查并打印下载链接；--download 仅下载到临时文件，
// 不自动替换运行中的二进制（避免高风险自替换）。

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/modelzen/feishu-codex-bridge/internal/core"
	"github.com/modelzen/feishu-codex-bridge/internal/update"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "检查 GitHub Releases 最新版本（可下载到临时文件，不自动替换）",
		RunE:  updateRun,
	}
	cmd.Flags().Bool("download", false, "下载匹配平台的二进制到临时文件（不替换）")
	cmd.Flags().String("repo", "", "上游仓库 owner/name（默认 "+update.DefaultRepo+"，或环境变量 FCB_UPDATE_REPO）")
	return cmd
}

func updateRun(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	repo := ""
	if r, _ := cmd.Flags().GetString("repo"); r != "" {
		repo = r
	} else if e := os.Getenv("FCB_UPDATE_REPO"); e != "" {
		repo = e
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := update.Latest(ctx, repo, nil)
	if err != nil {
		return err
	}
	cur := core.Version()
	fmt.Fprintf(out, "当前版本：%s\n最新版本：%s（%s）\n", cur, rel.TagName, rel.HTMLURL)

	switch update.CompareVersion(cur, rel.TagName) {
	case 0:
		fmt.Fprintln(out, "✅ 已是最新版本。")
		return nil
	case 1:
		fmt.Fprintln(out, "ℹ️ 当前版本比最新还新（开发构建？）。")
		return nil
	}

	fmt.Fprintln(out, "⬆️ 有新版本可用。")
	if rel.Body != "" {
		fmt.Fprintf(out, "变更摘要：\n%s\n", rel.Body)
	}
	asset, ok := update.CurrentPlatformAsset(rel)
	if !ok {
		fmt.Fprintln(out, "⚠️ 未找到匹配当前平台的预编译附件，可用附件：")
		for _, a := range rel.Assets {
			fmt.Fprintf(out, "  · %s  %s\n", a.Name, a.BrowserDownloadURL)
		}
		return nil
	}
	fmt.Fprintf(out, "本平台附件：%s\n下载：%s\n", asset.Name, asset.BrowserDownloadURL)

	if dl, _ := cmd.Flags().GetBool("download"); dl {
		fmt.Fprintf(out, "正在下载到临时文件…\n")
		path, derr := update.DownloadToTemp(ctx, asset, nil)
		if derr != nil {
			return derr
		}
		fmt.Fprintf(out, "✅ 已下载到：%s\n", path)
		fmt.Fprintln(out, "请手动替换当前二进制（为安全起见不自动替换运行中的进程），再重启 daemon。")
	}
	return nil
}
