# OfflineU —— 一键构建（只走 Docker）
#
#   make           显示所有目标
#   make all       Docker 镜像 + 64 位镜像 tar（一键出全部分发物）
#   make image     Docker 镜像 offlineu:local 与 offlineu:<版本>
#   make image-save 镜像导出 image-dist/offlineu-amd64-<版本>.tar（64 位）
#
# 分发方式只有 Docker：Windows exe 与飞牛OS / fnOS 的 fpk 适配都已移除。
# CI（.github/workflows/docker-build.yml）与这里同一条规则：
#   推送 main/master -> ghcr.io/<repo>:latest + offlineu-amd64-latest.tar
#   打 v* tag        -> ghcr.io/<repo>:X.Y.Z   + offlineu-amd64-X.Y.Z.tar（挂 Release）
#
# 版本号一律来自 git tag，和 docker/run.ps1 保持一致：HEAD 正好打着 tag
# （v2.1.2）就用 2.1.2，否则用 latest。要出带版本号的产物，先
# `git tag v2.1.2 && git push origin v2.1.2`。
#
# 依赖：Docker（镜像与 tar）。Go 1.23+ / Node 22+ 只在开发自检（make check /
# make web）时需要——分发镜像里的前端与二进制由 Dockerfile 自己构建。

.DEFAULT_GOAL := help

PLATFORM ?= linux/amd64
NPM      := npm --prefix web

# 版本号。用 2>&1 而不是 2>/dev/null，因为 cmd 和 sh 都认这个写法；
# 没有 tag 时 git 会打印一行 fatal:...，下面的判断会把它识别为 latest。
GIT_TAG := $(shell git describe --tags --exact-match HEAD 2>&1)
ifeq ($(patsubst v%,%,$(GIT_TAG)),$(GIT_TAG))
VERSION := latest
else
VERSION := $(patsubst v%,%,$(GIT_TAG))
endif

# 跨平台差异：Windows 用 cmd，其它平台用 sh。
ifeq ($(OS),Windows_NT)
Q     :=
BLANK := echo.
else
Q     := "
BLANK := echo ""
endif

.PHONY: help all web image image-save check vet test fmt clean

help: ## 显示所有目标
	@echo $(Q)OfflineU $(VERSION)$(Q)
	@$(BLANK)
	@echo $(Q)  make all         Docker 镜像 + 64 位镜像 tar（一键出全部分发物）$(Q)
	@echo $(Q)  make image        Docker 镜像 offlineu:local + offlineu:$(VERSION)$(Q)
	@echo $(Q)  make image-save   64 位镜像导出 image-dist/offlineu-amd64-$(VERSION).tar$(Q)
	@$(BLANK)
	@echo $(Q)  make web          构建前端 web/dist（开发自检用，镜像内不需要）$(Q)
	@echo $(Q)  make check        go vet + go test$(Q)
	@echo $(Q)  make vet / test / fmt$(Q)
	@echo $(Q)  make clean        清掉 image-dist（web/dist 保留）$(Q)
	@$(BLANK)
	@echo $(Q)版本来自 git tag：$(GIT_TAG) → $(VERSION)$(Q)

all: image-save ## 一键：Docker 镜像 + 64 位镜像 tar

# ---------------------------------------------------------------------------
# 前端。分发镜像在 Dockerfile 里自己构建前端；这里只用于本地开发自检
# （go run / go test 需要 web/dist 才能看到界面）。
# ---------------------------------------------------------------------------
web:
	$(NPM) install --no-audit --no-fund
	$(NPM) run build

# ---------------------------------------------------------------------------
# Docker 镜像。PLATFORM 默认 linux/amd64（64 位）；多架构发布由 CI 的
# buildx 完成（linux/amd64,linux/arm64）。VERSION 会以 --build-arg 注入
# 二进制，和镜像 tag、Release 文件名保持同一个号码。
# ---------------------------------------------------------------------------
image:
	docker build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t offlineu:local -t offlineu:$(VERSION) -f Dockerfile .

# 64 位镜像文件：docker save 导出，目标机上 `docker load -i` 离线导入，无需镜像仓库。
image-save: image
ifeq ($(OS),Windows_NT)
	@if not exist image-dist md image-dist
else
	@mkdir -p image-dist
endif
	docker save offlineu:local offlineu:$(VERSION) -o image-dist/offlineu-amd64-$(VERSION).tar
	@echo $(Q)Done: image-dist/offlineu-amd64-$(VERSION).tar$(Q)
	@echo $(Q)目标机上：docker load -i offlineu-amd64-$(VERSION).tar$(Q)

# ---------------------------------------------------------------------------
# 检查与清理（开发用）
# ---------------------------------------------------------------------------
check: vet test

vet:
	go vet ./...

test:
	go test ./...

fmt:
	gofmt -w internal

clean:
ifeq ($(OS),Windows_NT)
	-if exist image-dist rd /s /q image-dist
else
	rm -rf image-dist
endif
	@echo $(Q)已清理 image-dist（web/dist 保留）$(Q)
