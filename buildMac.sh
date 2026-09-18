#!/usr/bin/env bash
# =============================================================
# Git AI Tool macOS 一键打包
#
# ⚠️ 必须在 Mac 上执行（Wails 的 darwin 构建依赖 cgo + Xcode/WKWebView，
#    Windows 无法交叉编译出 mac 版本）。从 GitHub 拉取本仓库后在 mac 上跑：
#      ./buildMac.sh                 # 前端 + 全量构建 + 打包 zip（universal 双架构）
#      ./buildMac.sh -s              # 跳过前端构建（复用已有 frontend/dist）
#      ./buildMac.sh -arch arm64     # 只构建单架构（universal / arm64 / amd64）
#      ./buildMac.sh -keep           # 保留打包暂存目录便于排查
#   产物：build/dist/git-ai-tool-v<版本>-mac-<架构>.zip + .sha256
# =============================================================
set -euo pipefail

SKIP_FRONTEND=0
ARCH="universal"
KEEP=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    -s|--skip-frontend) SKIP_FRONTEND=1 ;;
    -arch) ARCH="$2"; shift ;;
    -keep) KEEP=1 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
  shift
done
case "$ARCH" in universal|arm64|amd64) ;; *) echo "-arch 仅支持 universal / arm64 / amd64"; exit 1 ;; esac

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

# 版本号与 Windows 打包脚本同源：读 app.go 里的 appVersion
VERSION=$(sed -n 's/.*appVersion = *"\([^"]*\)".*/\1/p' app.go | head -n1)
[[ -n "$VERSION" ]] || { echo "错误：未能从 app.go 读取 appVersion"; exit 1; }
echo "== Git AI Tool v${VERSION} (mac/${ARCH}) =="

command -v go  >/dev/null || { echo "错误：未安装 Go (https://go.dev/dl/)"; exit 1; }
command -v npm >/dev/null || { echo "错误：未安装 Node.js/npm"; exit 1; }
command -v git >/dev/null || { echo "错误：未安装 git，先执行 xcode-select --install"; exit 1; }

# wails CLI 与 go.mod 版本保持一致，没有就装
if ! command -v wails >/dev/null 2>&1; then
  WAILS_VER=$(grep -o 'github.com/wailsapp/wails/v2 v[0-9.]*' go.mod | head -n1 | awk '{print $2}')
  WAILS_VER="${WAILS_VER:-v2.10.1}"
  echo "== 安装 wails CLI ${WAILS_VER} =="
  GOBIN="$(go env GOPATH)/bin" go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VER}"
fi
export PATH="$(go env GOPATH)/bin:$PATH"

# 前端构建（-s 跳过时要求 frontend/dist 已存在）
if [[ $SKIP_FRONTEND -eq 0 ]]; then
  echo "== npm install + npm run build =="
  ( cd frontend && npm install && npm run build )
elif [[ ! -f frontend/dist/index.html ]]; then
  echo "错误：-s 需要 frontend/dist 已存在，先去掉 -s 跑一次"; exit 1
fi

echo "== wails build -platform darwin/${ARCH} =="
BUILD_ARGS=(build -platform "darwin/${ARCH}" -ldflags "-w -s")
[[ $SKIP_FRONTEND -eq 1 ]] && BUILD_ARGS+=(-s)
wails "${BUILD_ARGS[@]}"

APP="build/bin/git-ai-tool.app"
[[ -d "$APP" ]] || { echo "错误：构建产物缺失 $APP"; exit 1; }
echo "== 产物架构：$(lipo -archs "$APP/Contents/MacOS/git-ai-tool") =="

# ---- 打包绿色 zip ----
STAGE="build/.mac-staging"
rm -rf "$STAGE"; mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/Git AI Tool.app"

# 使用说明（UTF-8，mac 记事本/文本编辑可正常打开）
cat > "$STAGE/使用说明.txt" <<'EOF'
Git AI Tool (macOS) 使用说明
================================

一、运行
  1. 把「Git AI Tool.app」拷到「应用程序」文件夹（或任意目录）。
  2. 首次打开若提示"无法验证开发者"（未签名应用的正常拦截）：
     右键点击 App → 「打开」→ 再点「打开」；
     或在终端执行：xattr -cr "/Applications/Git AI Tool.app"

二、依赖
  - 需要 git 命令：终端执行 xcode-select --install，或 brew install git。
  - macOS 10.15 (Catalina) 及以上。

三、配置文件位置
  ~/Library/Application Support/GitAITool/config.json
  （首次点「保存配置」时自动生成；内含 API Key，请勿外传）

四、从源码重打包
  git clone 本仓库后，在仓库目录执行 ./buildMac.sh
EOF

ZIP_NAME="git-ai-tool-v${VERSION}-mac-${ARCH}.zip"
mkdir -p build/dist
rm -f "build/dist/$ZIP_NAME" "build/dist/$ZIP_NAME.sha256"
echo "== 压缩 $ZIP_NAME =="
( cd "$STAGE" && ditto -c -k --sequesterRsrc --keepParent "Git AI Tool.app" "使用说明.txt" "../dist/$ZIP_NAME" )
( cd build/dist && shasum -a 256 "$ZIP_NAME" > "$ZIP_NAME.sha256" )

SIZE=$(du -h "build/dist/$ZIP_NAME" | cut -f1)
echo "== 完成：build/dist/$ZIP_NAME ($SIZE) =="
[[ $KEEP -eq 1 ]] || rm -rf "$STAGE"
