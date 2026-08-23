#!/usr/bin/env bash
# VIGIL MEMORY — the demo, and the proof.
#
# Runs the real firewall twice in two separate OS processes with a `kill`
# between them, and shows the second process blocking an agent it has never
# itself seen. Nothing is stubbed: the same Go binary, the same SQLite file,
# the same code path an agent hits through MCP.
#
#   ./demo/memory_demo.sh
#
# The claim being demonstrated is narrow and checkable: after the terminal
# dies, VIGIL still knows. Everything printed below is produced live.

set -euo pipefail

cd "$(dirname "$0")/.."

BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; RST=$'\033[0m'

DB="${SIBYL_DB_PATH:-/tmp/vigil-memory-demo.db}"
PORT="${SIBYL_PORT:-8788}"
PY="${PYTHON:-python3}"

rule() { printf '%s\n' "────────────────────────────────────────────────────────────────────"; }
step() { printf '\n%s%s%s\n' "$BOLD" "$1" "$RST"; }

# A fresh database each run, so the demo is reproducible rather than
# depending on whatever state a previous run left behind.
rm -f "$DB"

step "VIGIL MEMORY — cross-session trust demo"
rule
printf 'commit    : %s%s%s\n' "$YEL" "$(git rev-parse --short HEAD)" "$RST"
printf 'timestamp : %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
printf 'database  : %s\n' "$DB"
rule

# --- start the memory service ---------------------------------------------
step "[0] starting the memory service (local SQLite, no API key)"
SIBYL_DB_PATH="$DB" SIBYL_PORT="$PORT" $PY services/sibyl-memory/app.py >/tmp/vigil-sibyl-demo.log 2>&1 &
SIBYL_PID=$!
trap 'kill $SIBYL_PID 2>/dev/null || true' EXIT

for _ in $(seq 1 40); do
  curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
  sleep 0.25
done
curl -sf "http://127.0.0.1:$PORT/health" | $PY -m json.tool | sed 's/^/    /'

export VIGIL_SIBYL_URL="http://127.0.0.1:$PORT"

# The incident-response denylist. Something has to catch the typosquat the
# FIRST time — memory does not originate enforcement, it records what was
# decided and re-applies it. This is that first catch.
export VIGIL_COMPROMISED_PACKAGES="reqeusts,cross-env-2"

# --- session 1 -------------------------------------------------------------
step "[1] SESSION 1 — agent attempts a typosquat, three times"
printf '%s    Each attempt runs the real firewall. Watch the trust score fall.%s\n' "$DIM" "$RST"
go run ./cmd/vigil-demo-agent -session s1 -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 3 | tee /tmp/vigil-demo-s1.txt

step "[2] KILL — the process ends. This is the part that normally erases everything."
kill $SIBYL_PID 2>/dev/null || true
wait $SIBYL_PID 2>/dev/null || true
printf '    memory service pid %s terminated\n' "$SIBYL_PID"
printf '    on disk: %s bytes at %s\n' "$(stat -c%s "$DB" 2>/dev/null || stat -f%z "$DB")" "$DB"
printf '    %sno process is holding any of this in memory any more%s\n' "$DIM" "$RST"

sleep 1

# --- session 2 -------------------------------------------------------------
step "[3] SESSION 2 — brand new processes, no shared state"
SIBYL_DB_PATH="$DB" SIBYL_PORT="$PORT" $PY services/sibyl-memory/app.py >/tmp/vigil-sibyl-demo2.log 2>&1 &
SIBYL_PID=$!
for _ in $(seq 1 40); do
  curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
  sleep 0.25
done

printf '%s    Same agent id, same tool, brand new firewall.%s\n' "$DIM" "$RST"
printf '%s    Note: the denylist is NOT set this time — only memory can block now.%s\n' "$DIM" "$RST"
env -u VIGIL_COMPROMISED_PACKAGES go run ./cmd/vigil-demo-agent \
  -session s2-fresh -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 1 | tee /tmp/vigil-demo-s2.txt

# --- the deletion test -----------------------------------------------------
step "[4] DELETION TEST — remove the memory layer, run the identical call"
printf '%s    VIGIL_SIBYL_DISABLED=1 severs the memory layer and nothing else.%s\n' "$DIM" "$RST"
env -u VIGIL_COMPROMISED_PACKAGES VIGIL_SIBYL_DISABLED=1 go run ./cmd/vigil-demo-agent \
  -session s3-nomem -agent trading-agent-alpha \
  -tool run_command -arg 'pip install reqeusts' -repeat 1 | tee /tmp/vigil-demo-s3.txt

# --- verdict, read back from what actually happened ------------------------
# Parsed from the real output rather than asserted, so this summary cannot
# drift away from the behaviour it is describing.
s2_decision=$(grep -oE '\b(ALLOW|BLOCK|PAUSE)\b' /tmp/vigil-demo-s2.txt | head -1)
s3_decision=$(grep -oE '\b(ALLOW|BLOCK|PAUSE)\b' /tmp/vigil-demo-s3.txt | head -1)
s2_trust=$(grep -oE 'trust=[0-9]+' /tmp/vigil-demo-s2.txt | head -1 | cut -d= -f2)

rule
printf '%sWhat actually happened%s\n' "$BOLD" "$RST"
printf '  session 1  three violations recorded; trust walked down the ladder\n'
printf '  kill       every process died; only the SQLite file survived\n'
printf '  session 2  %s%s%s with recalled trust=%s, denylist absent, no LLM, no graph\n' \
  "$GRN" "${s2_decision:-?}" "$RST" "${s2_trust:-?}"
printf '  deleted    %s%s%s — identical call, memory severed\n' \
  "$RED" "${s3_decision:-?}" "$RST"
rule

if [ "$s2_decision" = "BLOCK" ] && [ "$s3_decision" = "ALLOW" ]; then
  printf '\n%sGATE PASSED%s  memory is load-bearing: it is the only thing standing\n' "$GRN" "$RST"
  printf 'between session 2 and a package the agent was already caught installing.\n\n'
  exit 0
fi

printf '\n%sGATE FAILED%s  expected session 2 to BLOCK and the memory-less run to\n' "$RED" "$RST"
printf 'ALLOW. Got %s and %s. The load-bearing claim is not demonstrated.\n\n' \
  "${s2_decision:-?}" "${s3_decision:-?}"
exit 1
