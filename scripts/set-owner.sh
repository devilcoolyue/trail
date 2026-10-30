#!/usr/bin/env bash
# 把项目里的 OWNER 占位换成真实的 GitHub 用户名：scripts/set-owner.sh yourname
set -euo pipefail
cd "$(dirname "$0")/.."

owner=${1:?用法：scripts/set-owner.sh <GitHub 用户名>}
grep -rIl --exclude-dir=dist --exclude-dir=.git --exclude=release.sh --exclude=set-owner.sh OWNER . |
  while read -r f; do
    sed -i.bak "s/OWNER/$owner/g" "$f" && rm "$f.bak"
    echo "已替换 $f"
  done
