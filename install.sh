#!/bin/sh
# 一键安装 trail 的 Codex 面板：
#   curl -fsSL https://raw.githubusercontent.com/devilcoolyue/trail/main/install.sh | sh
#
# 从 GitHub Releases 下载对应系统的 trail，校验 SHA256 后装进 PATH；没有 tmux 时用 Homebrew 装上。
# 环境变量：TRAIL_VERSION=v0.1.1 指定版本，TRAIL_INSTALL_DIR=~/bin 指定安装目录。
set -eu

repo=devilcoolyue/trail
say() { printf '%s\n' "$*"; }
die() { printf 'trail 安装失败：%s\n' "$*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in darwin | linux) ;; *) die "不支持的系统 $os" ;; esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "不支持的 CPU $(uname -m)" ;;
esac

version=${TRAIL_VERSION:-}
if [ -z "$version" ]; then
  # releases/latest 会重定向到 .../releases/tag/<版本>，不用调 API
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
    die "连不上 GitHub"
  version=${url##*/}
fi
case $version in v*) ;; *) die "没找到发布版本（$version）" ;; esac

# 安装目录：已经装过就装回原处，否则选第一个可写的
dir=${TRAIL_INSTALL_DIR:-}
if [ -z "$dir" ] && old=$(command -v trail 2>/dev/null) && [ -w "$(dirname "$old")" ]; then
  dir=$(dirname "$old")
fi
if [ -z "$dir" ]; then
  for d in /opt/homebrew/bin /usr/local/bin; do
    if [ -d "$d" ] && [ -w "$d" ]; then dir=$d; break; fi
  done
fi
dir=${dir:-$HOME/.local/bin}
mkdir -p "$dir"

name=trail_${version}_${os}_${arch}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say "下载 trail $version（$os/$arch）…"
base=https://github.com/$repo/releases/download/$version
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || die "下载 $base/$name.tar.gz 失败"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "下载校验文件失败"
want=$(grep "$name.tar.gz" "$tmp/SHA256SUMS" | awk '{print $1}')
if command -v sha256sum >/dev/null; then
  got=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
else
  got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
fi
[ -n "$want" ] && [ "$want" = "$got" ] || die "SHA256 校验不通过"
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
install -m 755 "$tmp/$name/trail" "$dir/trail"
say "✓ 已安装 $dir/trail"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "! $dir 不在 PATH 里，在 ~/.zshrc 里加一行：export PATH=\"$dir:\$PATH\"" ;;
esac

if ! command -v tmux >/dev/null; then
  if command -v brew >/dev/null; then
    say "没有 tmux，用 Homebrew 安装…"
    brew install tmux
  else
    say "! 还需要 tmux 3.2 及以上：macOS 用 brew install tmux，Linux 用系统的包管理器"
  fi
fi
command -v codex >/dev/null || say "! 没找到 codex 命令：npm i -g @openai/codex 或 brew install --cask codex"

say ""
say "用法：在项目目录里用 trail codex 代替 codex 启动，对话时按 Ctrl-] 打开对话索引。"
say "想继续用自己的 tmux 的话，运行一次 trail setup。"
