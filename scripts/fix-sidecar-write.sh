#!/usr/bin/env bash
# 杀掉可能被 Cursor 沙箱污染的 sidecar，并用可写权限重启，验证线索落库。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> kill tg-sidecar on :17888"
pkill -9 -f '/tg-sidecar' 2>/dev/null || true
pkill -9 -f 'tg-sidecar' 2>/dev/null || true
sleep 1
if lsof -nP -iTCP:17888 -sTCP:LISTEN >/dev/null 2>&1; then
  kill -9 "$(lsof -tiTCP:17888 -sTCP:LISTEN)" 2>/dev/null || true
  sleep 1
fi

echo "==> rebuild + restart sidecar"
npm run sidecar:restart

DATA="${TG_SIDECAR_DATA:-$HOME/Library/Application Support/local.tgmatrix.desktop}"
echo "==> write probe on $DATA"
touch "$DATA/.write_probe" && rm -f "$DATA/.write_probe" && echo "dir writable OK"

LEAD="$(sqlite3 "$DATA/app.db" "SELECT id FROM marketing_leads WHERE status='pending' LIMIT 1;" || true)"
if [[ -z "${LEAD}" ]]; then
  LEAD="$(sqlite3 "$DATA/app.db" "SELECT id FROM marketing_leads LIMIT 1;" || true)"
fi
if [[ -n "${LEAD}" ]]; then
  echo "==> POST update lead $LEAD"
  curl -fsS -X POST "http://127.0.0.1:17888/v1/marketing/leads/${LEAD}" \
    -H 'Content-Type: application/json' \
    -d '{"status":"pending"}' >/dev/null
  echo "POST write OK"
else
  echo "!! no leads to probe, skip POST"
fi

curl -fsS "http://127.0.0.1:17888/health"
echo
echo "完成。请回到桌面窗口刷新线索后，再点「AI 分析适配」。"
