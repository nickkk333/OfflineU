#!/usr/bin/env bash
# ============================================================
# 打包飞牛OS（fnOS）.fpk 安装包（Docker 模式）
#
# 用法：
#   bash packaging/fnos-app/build-fpk.sh [version] [platform] [image] [output]
#     version  写进 manifest 的版本号，必须形如 1.2.3 / 1.2.3-beta
#              （非数字版本——例如 latest——会被飞牛拒绝安装，脚本会换成 0.0.<日期>）
#     platform 目标平台，x86 或 arm（默认 x86；必须与 NAS 的架构一致）
#     image    容器内使用的镜像，带 tag 的完整引用（默认 ghcr.io/nickkk333/offlineu:latest）
#     output   产物路径，相对路径基于仓库根目录
#              （默认 image-dist/offlineu_<version>_<platform>.fpk，飞牛要求这种命名）
#
# 依赖：bash / git / tar / sed / md5sum（Linux、macOS、WSL、Git Bash 均可）
#
# 说明：生命周期脚本由第三方通用打包框架 conversun/fnos-apps 提供（shared/cmd/*），
#       这里只做应用源组装 + app.tgz + 调用其 build-fpk.sh。
#       本 fpk 只是 fnOS 外壳：应用本体仍是 GHCR 上的 Docker 镜像，
#       所以目标机需要能拉取该镜像（或在 fnOS 里先导入镜像 tar）。
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_SRC="$SCRIPT_DIR"

APPNAME="offlineu"

PLATFORM="${2:-x86}"
IMAGE="${3:-ghcr.io/nickkk333/offlineu:latest}"

# fnOS 只认 x[.y[.z]][-build] 这种数字版本；`latest`、commit hash 之类会被判成
# 「应用包不符合系统要求」而拒绝安装，所以这里统一归一化。
normalize_version() {
  local v="${1#v}"
  if printf '%s' "$v" | grep -Eq '^[0-9]+(\.[0-9]+)*(-[0-9A-Za-z.]+)?$'; then
    printf '%s' "$v"
  fi
}

RAW_VERSION="${1:-}"
VERSION="$(normalize_version "${RAW_VERSION}")"
if [ -z "$VERSION" ]; then
  VERSION="0.0.$(date +%Y%m%d)"
  echo ">>> 警告：'${RAW_VERSION}' 不是 fnOS 认可的版本号（x[.y[.z]][-build]），manifest 用 ${VERSION}"
  echo ">>>       想用真实版本号就：make fnos-fpk FNOS_VERSION=1.0.0，或先打 tag（v1.0.0）"
fi

# 飞牛手动安装要求文件名是 <appname>_<version>_<platform>.fpk
OUTPUT="${4:-image-dist/${APPNAME}_${VERSION}_${PLATFORM}.fpk}"

case "$OUTPUT" in
  /*) ;;
  [A-Za-z]:*) ;;
  *) OUTPUT="$REPO_ROOT/$OUTPUT" ;;
esac

case "$(basename "$OUTPUT")" in
  "${APPNAME}"_*_*.fpk) ;;
  *) echo ">>> 警告：包名建议是 ${APPNAME}_<version>_<platform>.fpk，当前为 $(basename "$OUTPUT")，可能被系统拒绝" ;;
esac

TOOLKIT_REPO="https://github.com/conversun/fnos-apps"
APP_NAME_DIR="offlineu"

# 网络不稳/离线时可以复用已有的框架副本：FNOS_TOOLKIT_DIR=/path/to/fnos-apps
if [ -n "${FNOS_TOOLKIT_DIR:-}" ] && [ -d "${FNOS_TOOLKIT_DIR}/scripts" ]; then
  echo ">>> 复用本地打包框架：${FNOS_TOOLKIT_DIR}"
  TOOLKIT_DIR="$FNOS_TOOLKIT_DIR"
else
  TOOLKIT_DIR="$(mktemp -d)/fnos-toolkit"
  trap 'rm -rf "$TOOLKIT_DIR"' EXIT
  echo ">>> 获取 fnOS 打包框架"
  # autocrlf=false：Windows 上默认会把 checkout 变成 CRLF，而 fnOS 上的 bash 不认 "#!...^M"
  git -c core.autocrlf=false -c core.eol=lf clone --depth 1 "$TOOLKIT_REPO" "$TOOLKIT_DIR"
fi

# 框架里的 cmd/*、wizard/* 会直接进 fpk 在 Linux 上执行，统一成 LF（幂等）
find "$TOOLKIT_DIR/scripts" "$TOOLKIT_DIR/shared" -type f -exec sed -i 's/\r$//' {} +

APP_DIR="$TOOLKIT_DIR/apps/${APP_NAME_DIR}"

echo ">>> 组装应用目录"
mkdir -p "$APP_DIR/fnos"
cp -a "$APP_SRC"/. "$APP_DIR/fnos/"
rm -f "$APP_DIR/fnos/build-fpk.sh"
# ICON.PNG(64) / ICON_256.PNG(256) 已经在 packaging/fnos-app 下，随上面的 cp -a 一起带过去

# 桌面入口用 images/{0}.png，{0} 会被替换成 64 / 256，所以 ui/images 里两个尺寸都要有
if [ -d "$APP_DIR/fnos/ui/images" ]; then
  cp "$APP_SRC/ICON.PNG" "$APP_DIR/fnos/ui/images/64.png"
  cp "$APP_SRC/ICON_256.PNG" "$APP_DIR/fnos/ui/images/256.png"
fi

echo ">>> 规范化行尾（Windows 检出可能带 CRLF，Linux 上的 bash 会认不出 shebang）"
find "$APP_DIR/fnos" -type f \
  \( -path "*/cmd/*" -o -path "*/wizard/*" -o -path "*/config/*" \
     -o -name "*.sc" -o -name "manifest" -o -name "config" -o -name "*.yaml" \) \
  -exec sed -i 's/\r$//' {} +

echo ">>> 写入镜像引用"
sed -i.bak "s|@IMAGE@|${IMAGE}|g" "$APP_DIR/fnos/docker/docker-compose.yaml"
rm -f "$APP_DIR/fnos/docker/docker-compose.yaml.bak"

echo ">>> 生成 app.tgz（Docker 模式只需要 compose + UI）"
( cd "$APP_DIR/fnos" && tar -czf app.tgz docker ui )

echo ">>> 打包 fpk"
mkdir -p "$(dirname "$OUTPUT")"
# 框架脚本会把 fpk_version 写进 manifest（已上架的包都有这个字段，缺了容易被判成旧格式）
export FPK_VERSION="${VERSION}"
( cd "$TOOLKIT_DIR" \
  && NAME="$(scripts/build-fpk.sh apps/offlineu apps/offlineu/fnos/app.tgz "$VERSION" "$PLATFORM" | tail -n 1)" \
  && cp "$NAME" "$OUTPUT" )

echo ">>> 完成：$OUTPUT"
echo ">>> 平台：${PLATFORM}（目标机 uname -m 应为 $([ "$PLATFORM" = "arm" ] && echo aarch64 || echo x86_64)，不一致会报「应用包不符合系统要求」）"
ls -lh "$OUTPUT"
