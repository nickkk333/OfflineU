# OfflineU —— 一键构建
#
#   make           显示所有目标
#   make all       exe + Docker 镜像 + 飞牛 fpk（一键出全部分发物）
#   make exe       Windows exe（前端已内嵌，单文件）
#   make image     Docker 镜像 offlineu:local 与 offlineu:<版本>
#   make fpk       飞牛OS / fnOS 安装包（内置镜像，离线安装）
#
# 版本号一律来自 git tag，和 build-windows.ps1、docker/run.ps1、fnos/build.ps1
# 保持同一条规则：HEAD 正好打着 tag（v2.1.2）就用 2.1.2，否则用 latest。
# 所以要出带版本号的产物，先 `git tag v2.1.2 && git push origin v2.1.2`。
#
# 依赖：Go 1.23+、Node 22+（构建前端）、Docker（镜像与 fpk）。
# fpk 还需要 PowerShell（Windows 上是 powershell，其它平台是 pwsh）。

.DEFAULT_GOAL := help

PKG      := github.com/nickkk333/offlineu
PLATFORM ?= linux/amd64
EXE_ARCH ?= amd64
NPM      := npm --prefix web

# 版本号。用 2>&1 而不是 2>/dev/null，因为 cmd 和 sh 都认这个写法；
# 没有 tag 时 git 会打印一行 fatal:...，下面的判断会把它识别为 latest。
GIT_TAG := $(shell git describe --tags --exact-match HEAD 2>&1)
ifeq ($(patsubst v%,%,$(GIT_TAG)),$(GIT_TAG))
VERSION := latest
else
VERSION := $(patsubst v%,%,$(GIT_TAG))
endif

# 跨平台差异：Windows 用 cmd + powershell，其它平台用 sh + pwsh。
ifeq ($(OS),Windows_NT)
	PS := powershell -NoProfile -ExecutionPolicy Bypass -File
	# cmd 的 echo 会把引号一起打印出来，所以只在 sh 一侧加引号；
	# 空行在 cmd 里要写成 echo.（裸 echo 会输出 "ECHO is off."）。
	Q     :=
	BLANK := echo.
else
	PS := pwsh -NoProfile -File
	Q     := "
	BLANK := echo ""
endif

.PHONY: help all web exe exe-portable image image-save fpk check vet test fmt clean

help: ## 显示所有目标
	@echo $(Q)OfflineU $(VERSION)$(Q)
	@$(BLANK)
	@echo $(Q)  make all          exe + 镜像 + fpk（一键出全部分发物）$(Q)
	@echo $(Q)  make exe          Windows exe → dist/offlineu-$(VERSION).exe$(Q)
	@echo $(Q)  make exe-portable 便携版 exe（内嵌 ffmpeg，走 build-windows.ps1）$(Q)
	@echo $(Q)  make image        Docker 镜像 offlineu:local + offlineu:$(VERSION)$(Q)
	@echo $(Q)  make image-save   镜像导出为 image-dist/offlineu-amd64-$(VERSION).tar$(Q)
	@echo $(Q)  make fpk          飞牛OS 包 fnos/offlineu_$(VERSION)_x86.fpk$(Q)
	@$(BLANK)
	@echo $(Q)  make web          只构建前端 web/dist$(Q)
	@echo $(Q)  make check        go vet + go test$(Q)
	@echo $(Q)  make vet / test / fmt$(Q)
	@echo $(Q)  make clean        清掉 dist、image-dist、fnos 下的 fpk$(Q)
	@$(BLANK)
	@echo $(Q)版本来自 git tag：$(GIT_TAG) → $(VERSION)$(Q)

all: exe image fpk ## 一键：exe + 镜像 + fpk

# ---------------------------------------------------------------------------
# 前端。Go 用 //go:embed all:web/dist 把它编进二进制，所以 exe 之前必须先构建。
# ---------------------------------------------------------------------------
web:
	$(NPM) install --no-audit --no-fund
	$(NPM) run build

# ---------------------------------------------------------------------------
# Windows exe。交叉编译，任何平台都能产出 Windows 版本；产物是静态单文件，
# 不依赖任何运行时。SKIP_WEB=1 可跳过前端（web/dist 已是最新的时）。
# ---------------------------------------------------------------------------
ifeq ($(SKIP_WEB),1)
exe:
else
exe: web
endif
exe: export GOOS := windows
exe: export GOARCH := $(EXE_ARCH)
exe: export CGO_ENABLED := 0
exe:
ifeq ($(OS),Windows_NT)
	@if not exist dist md dist
else
	@mkdir -p dist
endif
	go build -trimpath -ldflags="-s -w -X $(PKG)/internal/offlineu.Version=$(VERSION)" -o dist/offlineu-$(VERSION).exe .
	@echo $(Q)Done: dist/offlineu-$(VERSION).exe$(Q)

# 便携版：内嵌静态 ffmpeg（约 166 MB），首次播放 MKV / MPEG-TS 不用联网下载。
# 由 PowerShell 脚本完成，需要 Windows 宿主。
exe-portable: web
ifeq ($(OS),Windows_NT)
	$(PS) build-windows.ps1
else
	@echo "build-windows.ps1 需要 Windows（它要下载并内嵌 Windows 版 ffmpeg）。"
	@echo "其它平台请用 'make exe' 交叉编译。"
	@exit 1
endif

# ---------------------------------------------------------------------------
# Docker 镜像。前端在镜像里从源码构建，所以不依赖本地的 web/dist。
# ---------------------------------------------------------------------------
image:
	docker build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t offlineu:local -t offlineu:$(VERSION) -f Dockerfile .

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
# 飞牛OS 包。镜像已经由 image 目标编好，这里用 -SkipBuild 复用，避免重复构建；
# 脚本会把它 docker save 进包里，安装时离线 docker load。
# ---------------------------------------------------------------------------
fpk: image
	$(PS) fnos/build.ps1 -SkipBuild

# ---------------------------------------------------------------------------
# 检查与清理
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
	-if exist dist rd /s /q dist
	-if exist image-dist rd /s /q image-dist
	-del /q fnos\*.fpk 2>NUL
else
	rm -rf dist image-dist
	rm -f fnos/*.fpk
endif
	@echo $(Q)已清理 dist、image-dist、fnos 下的 fpk（web/dist 保留）$(Q)
