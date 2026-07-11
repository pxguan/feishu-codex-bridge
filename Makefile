# feishu-codex-bridge (Go) —— 构建/测试/发布入口
# 版本号由 git describe 推导，编译期 ldflags 注入 internal/core.version。

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X github.com/modelzen/feishu-codex-bridge/internal/core.version=$(VERSION)
BINARY   := feishu-codex-bridge

.PHONY: build test vet fmt lint clean run release help

build: ## 构建二进制（注入版本号）
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/feishu-codex-bridge

run: build ## 前台运行（开发态）
	./$(BINARY) run

test: ## 跑全部单测/集成测试
	go test ./...

test-race: ## 带竞态检测
	go test -race ./...

vet: ## go vet
	go vet ./...

fmt: ## gofmt 格式化
	gofmt -s -w .

lint: vet ## 静态检查 + 格式校验
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:\n$$out"; exit 1; fi

coverage: ## 生成覆盖率报告
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

clean: ## 清理产物
	rm -f $(BINARY) coverage.out coverage.html

release: build ## 发布打包（二期接 go-selfupdate 打包并上传 GitHub Releases）
	@echo "release $(VERSION) — Phase 2 将接 go-selfupdate 上传 Releases"

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
