#!/usr/bin/env bash
cd "$(dirname "$0")/.."
bash scripts/fix-sidecar-write.sh
echo
read -r -p "按回车关闭…"
