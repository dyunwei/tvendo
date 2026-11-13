#!/bin/sh
set -eu
base={{SHELL_BASE}}
version={{SHELL_VERSION}}
dest="${TVENDO_INSTALL_DIR:-$HOME/.local/bin}"
while [ "$#" -gt 0 ]; do
 case "$1" in
  --version) [ "$#" -ge 2 ] || exit 2; [ "$2" = "$version" ] || { echo "此部署提供版本 $version" >&2; exit 2; }; shift 2 ;;
  --dir) [ "$#" -ge 2 ] || exit 2; dest=$2; shift 2 ;;
  *) echo '用法: sh install.sh [--version 版本] [--dir 安装目录]' >&2; exit 2 ;;
 esac
done
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo '请在 Windows 下载 manifest.json 中对应的 .exe，或使用 npm 安装。' >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo '不支持的 CPU 架构' >&2; exit 2 ;; esac
name="tvendo-$os-$arch"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
fetch() {
 if command -v curl >/dev/null 2>&1; then curl --fail --silent --show-error --output "$2" "$1"
 elif command -v wget >/dev/null 2>&1; then wget -q -O "$2" "$1"
 else echo '需要 curl 或 wget' >&2; exit 2; fi
}
fetch "$base/cli/$name" "$tmp/$name"
fetch "$base/cli/SHA256SUMS" "$tmp/SHA256SUMS"
expected=$(awk -v name="$name" '$2==name {print $1}' "$tmp/SHA256SUMS")
[ "${#expected}" -eq 64 ] || { echo '校验清单无效' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp/$name" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$tmp/$name" | awk '{print $1}')
else echo '需要 sha256sum 或 shasum' >&2; exit 2; fi
[ "$expected" = "$actual" ] || { echo '二进制校验失败，未安装' >&2; exit 1; }
chmod 755 "$tmp/$name"
installed_version=$("$tmp/$name" --version)
[ "$installed_version" = "$version" ] || { echo '下载产物版本不一致，请重试下载' >&2; exit 1; }
mkdir -p "$dest"
# Prepare in the destination filesystem, then atomically replace the executable.
staged=$(mktemp "$dest/.tvendo.XXXXXX")
trap 'rm -rf "$tmp"; rm -f "$staged"' EXIT HUP INT TERM
cp "$tmp/$name" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$dest/tvendo"
echo "Tvendo $version 已安装到 $dest/tvendo"
echo "请确保 $dest 在 PATH 中，或用完整路径执行。"
