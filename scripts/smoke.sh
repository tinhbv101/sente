#!/usr/bin/env bash
# Check that a deployed Sente node actually works, not just that it answers.
#
#   scripts/smoke.sh https://sente.example.com
#
# Exits non-zero on the first failure, so it is safe to run from a deploy script.
set -euo pipefail

BASE="${1:-http://localhost:8080}"
PASS=0

# SMOKE_INSECURE=1 accepts a self-signed certificate, for a local or staging box
# whose proxy has no real one yet. Never needed against a real domain, so never
# the default.
curl() { command curl ${SMOKE_INSECURE:+-k} "$@"; }
# The checks run inside `bash -c`, which does not inherit functions unless told to.
export -f curl

# Padding is done in code points rather than with printf's %-Ns, which counts
# bytes and so mis-aligns every accented label.
pad() { python3 -c "import sys; t=sys.argv[1]; print(t + ' ' * max(1, 38 - len(t)), end='')" "$1"; }

check() {
  printf '  '; pad "$1"; printf ' '
  shift
  if "$@"; then
    echo "ok"
    PASS=$((PASS + 1))
  else
    echo "FAILED"
    exit 1
  fi
}

json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

echo "Kiểm tra $BASE"

check "liveness" \
  bash -c "curl -sf '$BASE/healthz' | grep -q '\"status\":\"ok\"'"

check "readiness: postgres và redis" \
  bash -c "curl -sf '$BASE/readyz' | grep -q '\"postgres\":\"ok\"' && curl -sf '$BASE/readyz' | grep -q '\"redis\":\"ok\"'"

RULES=$(curl -sf "$BASE/v1/config" | json "d['rules_version']")
check "config trả về rules_version" test -n "$RULES"

# Signing up, creating a game and reading it back exercises the database, the
# lease, the actor and the rules engine in one go. If any of them is broken this
# is where it shows, not at three in the morning.
TOKEN=$(curl -sf -X POST "$BASE/v1/auth/guest" | json "d['access_token']")
check "đăng ký tài khoản khách" test -n "$TOKEN"

GAME=$(curl -sf -X POST "$BASE/v1/games" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"board_size":9,"rules":"japanese","time_control":{"kind":"absolute","main_time_ms":600000}}' \
  | json "d['game_id']")
check "tạo ván" test -n "$GAME"

STATE=$(curl -sf "$BASE/v1/games/$GAME" -H "Authorization: Bearer $TOKEN")
check "đọc lại ván vừa tạo" \
  bash -c "echo '$STATE' | grep -q '\"phase\":\"playing\"'"
check "bàn cờ đúng 81 giao điểm và trống" \
  bash -c "test \$(echo '$STATE' | python3 -c \"import json,sys; b=json.load(sys.stdin)['board']; print(len(b) if set(b)=={'.'} else 0)\") -eq 81"

check "endpoint có bảo vệ token" \
  bash -c "test \$(curl -s -o /dev/null -w '%{http_code}' -X POST '$BASE/v1/games') -eq 401"

# The invite-link journey (docs/01 J1): one person makes a link, a stranger
# previews it without signing in, then signs in and accepts.
CODE=$(curl -sf -X POST "$BASE/v1/challenges" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"board_size":9,"creator_color":"black","time_control":{"kind":"absolute","main_time_ms":600000}}' \
  | json "d['code']")
check "tạo lời mời qua link" test -n "$CODE"

check "xem trước lời mời không cần đăng nhập" \
  bash -c "curl -sf '$BASE/v1/challenges/$CODE' | grep -q '\"status\":\"pending\"'"

FRIEND=$(curl -sf -X POST "$BASE/v1/auth/guest" | json "d['access_token']")
JOINED=$(curl -sf -X POST "$BASE/v1/challenges/$CODE/accept" -H "Authorization: Bearer $FRIEND" \
  | json "d['game_id']")
check "bạn nhận lời mời và vào ván" test -n "$JOINED"

check "lời mời đã dùng không nhận lần hai" \
  bash -c "test \$(curl -s -o /dev/null -w '%{http_code}' -X POST '$BASE/v1/challenges/$CODE/accept' -H 'Authorization: Bearer $FRIEND') -eq 409"

check "có header rate limit" \
  bash -c "curl -sfI '$BASE/v1/config' | grep -qi 'x-ratelimit-limit'"

echo
echo "$PASS/$PASS đạt · rules_version $RULES"
