#!/usr/bin/env bash
# install_subconverter.sh - 一键安装 subconverter 到本项目 bin/subconverter/
#
# 用法：
#   ./install_subconverter.sh              # 自动检测架构
#   ./install_subconverter.sh linux64       # 指定架构
#   ./install_subconverter.sh darwinarm     # macOS ARM
#
# 下载源由 scbase.txt 指定：
#   推荐（支持 vless）：
#     https://github.com/asdlokj1qpi233/subconverter/releases/latest/download/subconverter_{arch}.tar.gz
#   兼容旧格式（nightly.link base，自动拼接 /subconverter_ARCH.zip）：
#     https://nightly.link/tindy2013/subconverter/actions/runs/XXXXX
#   注意：官方 tindy2013 构建不支持 vless 分享链接解析，纯 vless 订阅会得到
#   "No nodes were found!"；需要转换 vless 请使用上方 {arch} 模板源。
# 也可通过环境变量 SCBASE 覆盖。

set -e

# 项目根目录（脚本所在目录）
ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="$ROOT_DIR/bin/subconverter"
SCBASE_FILE="$ROOT_DIR/scbase.txt"

# 读取 scbase.txt（或环境变量）
if [ -n "$SCBASE" ]; then
    SCBASE_URL="$SCBASE"
elif [ -f "$SCBASE_FILE" ]; then
    SCBASE_URL="$(head -1 "$SCBASE_FILE" | tr -d '[:space:]')"
fi
# 空则回退：优先支持 vless 的 fork 模板源
[ -n "${SCBASE_URL:-}" ] || SCBASE_URL="https://github.com/asdlokj1qpi233/subconverter/releases/latest/download/subconverter_{arch}.tar.gz"

echo "=============================="
echo " edt_panel subconverter 安装脚本"
echo "=============================="
echo " 下载源: $SCBASE_URL"
echo " 安装到: $INSTALL_DIR"
echo ""

# 检测架构
ARCH="$1"
if [ -z "$ARCH" ]; then
    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    MACHINE="$(uname -m)"
    case "$OS" in
        linux)
            case "$MACHINE" in
                x86_64|amd64) ARCH="linux64" ;;
                aarch64|arm64) ARCH="aarch64" ;;
                armv7l) ARCH="armv7" ;;
                *) echo "不支持的架构: $MACHINE"; exit 1 ;;
            esac
            ;;
        darwin)
            case "$MACHINE" in
                x86_64|amd64) ARCH="darwin64" ;;
                arm64|aarch64) ARCH="darwinarm" ;;
                *) echo "不支持的架构: $MACHINE"; exit 1 ;;
            esac
            ;;
        mingw*|msys*|cygwin*)
            case "$MACHINE" in
                x86_64|amd64) ARCH="win64" ;;
                *) ARCH="win32" ;;
            esac
            ;;
        *) echo "不支持的系统: $OS"; exit 1 ;;
    esac
fi

echo " 架构: $ARCH"

# 构建下载 URL：{arch} 模板直接替换；否则兼容 nightly.link base + zip 后缀
if [[ "$SCBASE_URL" == *"{arch}"* ]]; then
    DOWNLOAD_URL="${SCBASE_URL/\{arch\}/$ARCH}"
else
    DOWNLOAD_URL="${SCBASE_URL%/}/subconverter_${ARCH}.zip"
fi
echo " 下载: $DOWNLOAD_URL"
echo ""

# 下载
TMP_DIR="$(mktemp -d)"
trap "rm -rf $TMP_DIR" EXIT
ZIP_FILE="$TMP_DIR/subconverter.zip"

echo "下载中..."
if command -v curl &>/dev/null; then
    curl -sSL -o "$ZIP_FILE" "$DOWNLOAD_URL" || { echo "下载失败"; exit 1; }
elif command -v wget &>/dev/null; then
    wget -q -O "$ZIP_FILE" "$DOWNLOAD_URL" || { echo "下载失败"; exit 1; }
else
    echo "错误: 需要 curl 或 wget"
    exit 1
fi

echo "下载完成: $(du -h "$ZIP_FILE" | cut -f1)"

# 清理旧安装
if [ -d "$INSTALL_DIR" ]; then
    echo "清理旧安装..."
    rm -rf "$INSTALL_DIR"
fi

# 解压
echo "解压到 $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"
if command -v unzip &>/dev/null; then
    unzip -q -o "$ZIP_FILE" -d "$INSTALL_DIR"
else
    # 尝试 Python 解压
    python3 -c "
import zipfile, sys
with zipfile.ZipFile('$ZIP_FILE') as z:
    z.extractall('$INSTALL_DIR')
" || { echo "解压失败: 需要 unzip 或 python3"; exit 1; }
fi

# 设置可执行权限
SC_BIN="$INSTALL_DIR/subconverter"
if [ -f "$SC_BIN" ]; then
    chmod +x "$SC_BIN"
    echo ""
    echo "=============================="
    echo " 安装成功！"
    echo "=============================="
    echo " 二进制: $SC_BIN"
    echo " 版本:"
    "$SC_BIN" -v 2>/dev/null | head -2 || echo "  (无法获取版本)"
    echo ""
    echo " 配置 config.yml 启用："
    echo "   subconverter:"
    echo "     mode: local"
    echo "     bin: bin/subconverter/subconverter"
    echo ""
    echo " 或通过 CLI 参数启动："
    echo "   ./edt --sc-mode=local"
else
    echo "错误: 解压后未找到 subconverter 二进制"
    ls -la "$INSTALL_DIR"
    exit 1
fi
