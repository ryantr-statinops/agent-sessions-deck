#!/usr/bin/env bash
# Stage 00 terminal spike - one-command rerun.
#   bash run.sh            # full spike, evidence in evidence/
#   bash run.sh --skip-go  # python probes only (offline-friendly)
#
# Bounded: every probe has internal timeouts; the whole script is capped by
# `timeout` when invoked via the exact rerun command in README.md.
# Cleanup-safe: the EXIT trap signals ONLY the direct child this process
# started (recorded PID + start-identity check) and removes ONLY this
# invocation's private dir (.run/<run-id>/). No `pkill -f`, no `pgrep -f`
# kills, no /tmp wildcards anywhere in this spike. The outer terminal is
# never touched (all tests use their own PTYs); `cleanup.sh` reaps only
# stale per-invocation dirs with verified-dead owners. Evidence files are
# overwritten on each run.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
EV="$HERE/evidence"
RUN_ROOT="$HERE/.run"
RUN_ID="run-$$-$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM"
RUN_DIR="$RUN_ROOT/$RUN_ID"
SKIP_GO=0
[ "${1:-}" = "--skip-go" ] && SKIP_GO=1

mkdir -p "$EV"
mkdir -p "$RUN_DIR"
chmod 700 "$RUN_ROOT" "$RUN_DIR" 2>/dev/null || true
export LC_ALL=en_US.UTF-8
export TERM=xterm-256color
# Scoped scratch root for this invocation; probes create unique subdirs
# inside it (or a private mkdtemp when run standalone) and clean only those.
export ASD_SPIKE_RUN_DIR="$RUN_DIR"
export ASD_SPIKE_RUN_ID="$RUN_ID"

# Start-identity helpers: /proc stat starttime (field 22) distinguishes PID
# reuse from the process we actually started.
proc_starttime() {
  # $1 = pid; prints starttime ticks or UNKNOWN.
  if [ ! -r "/proc/$1/stat" ]; then echo "UNKNOWN"; return 1; fi
  sed 's/^.*) //' "/proc/$1/stat" 2>/dev/null | cut -d' ' -f20 2>/dev/null || echo "UNKNOWN"
}
proc_ppid() {
  ps -o ppid= -p "$1" 2>/dev/null | tr -d ' ' || echo ""
}
echo "$$ $(proc_starttime $$ 2>/dev/null || echo UNKNOWN)" > "$RUN_DIR/runner.pid"

CURRENT_CHILD=""
CURRENT_CHILD_DESC=""
kill_known_child() {
  # Signal ONLY a recorded direct child of this shell. Refuse anything else
  # (wrong PPID, PID reuse, or empty) rather than risk another run's process.
  local pid="$1" desc="${2:-child}"
  case "$pid" in ''|*[!0-9]*) return 0;; esac
  [ "$pid" != "$$" ] || return 0
  if ! kill -0 "$pid" 2>/dev/null; then return 0; fi
  local ppid
  ppid="$(proc_ppid "$pid")"
  if [ "$ppid" != "$$" ]; then
    echo "cleanup: refusing to signal PID $pid ($desc): not our child (ppid=${ppid:-?})" >&2
    return 0
  fi
  kill -TERM "$pid" 2>/dev/null || return 0
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.5
  done
  kill -KILL "$pid" 2>/dev/null || true
}
cleanup() {
  # Bounded second net for abnormal termination: only our current direct
  # child, then only our own private dir. Never touches other runs or /tmp.
  if [ -n "${CURRENT_CHILD:-}" ]; then
    kill_known_child "$CURRENT_CHILD" "$CURRENT_CHILD_DESC" || true
  fi
  if [ -n "${RUN_DIR:-}" ] && [ "$RUN_DIR" != "$RUN_ROOT" ] && [ "$RUN_DIR" != "$HERE" ]; then
    case "$RUN_DIR" in
      "$RUN_ROOT"/run-*) rm -rf "$RUN_DIR" || true;;
      *) echo "cleanup: refusing to remove unexpected RUN_DIR=$RUN_DIR" >&2;;
    esac
  fi
  # The harness uses only private PTYs and never changes the caller's terminal.
}
trap cleanup EXIT

FAIL=0
run_probe() {
  local name="$1"; shift
  echo "=== $name ==="
  timeout 120 "$@" --out "$EV/$name.json" &
  CURRENT_CHILD=$!
  CURRENT_CHILD_DESC="timeout $* ($name)"
  echo "$CURRENT_CHILD $(proc_starttime "$CURRENT_CHILD" 2>/dev/null || echo UNKNOWN) $CURRENT_CHILD_DESC" > "$RUN_DIR/child-$name.pid"
  local rc=0
  if wait "$CURRENT_CHILD"; then rc=0; else rc=$?; fi
  CURRENT_CHILD=""
  CURRENT_CHILD_DESC=""
  rm -f "$RUN_DIR/child-$name.pid"
  if [ "$rc" -eq 0 ]; then
    echo "--- $name OK"
  else
    echo "--- $name FAILED (exit $rc)"
    FAIL=1
  fi
}

run_probe pty_basics   python3 "$HERE/probes/pty_basics.py"
run_probe query_response python3 "$HERE/probes/query_response.py"
run_probe altscreen     python3 "$HERE/probes/altscreen.py"
run_probe two_sessions  python3 "$HERE/probes/two_sessions.py"
run_probe corpus        python3 "$HERE/probes/corpus.py"

if [ "$SKIP_GO" = 0 ] && command -v go >/dev/null 2>&1; then
  echo "=== go_spike ==="
  ( cd "$HERE/go" && timeout 150 go run . --out "$EV/go_spike.json" ) &
  CURRENT_CHILD=$!
  CURRENT_CHILD_DESC="timeout go run (go_spike)"
  echo "$CURRENT_CHILD $(proc_starttime "$CURRENT_CHILD" 2>/dev/null || echo UNKNOWN) $CURRENT_CHILD_DESC" > "$RUN_DIR/child-go_spike.pid"
  go_rc=0
  if wait "$CURRENT_CHILD"; then go_rc=0; else go_rc=$?; fi
  CURRENT_CHILD=""
  CURRENT_CHILD_DESC=""
  rm -f "$RUN_DIR/child-go_spike.pid"
  [ "$go_rc" -eq 0 ] || FAIL=1
else
  echo "=== go_spike SKIPPED (flag or no go toolchain) ==="
fi

# Resolve only the orchestration CLI. Never invoke bare `orca` on Linux.
probe_orca_version() {
  if [ -n "${ORCA_CLI_COMMAND:-}" ]; then
    timeout 10 "$ORCA_CLI_COMMAND" --version 2>&1 | head -1 || echo "NOT PROBED"
  elif command -v orca-ide >/dev/null 2>&1; then
    timeout 10 orca-ide --version 2>&1 | head -1 || echo "NOT PROBED"
  else
    echo "NOT PROBED (no ORCA_CLI_COMMAND or orca-ide)"
  fi
}

{
  echo "host: $(uname -srm)"
  echo "date_utc: $(date -u +%FT%TZ)"
  echo "go: $(go version 2>/dev/null || echo MISSING)"
  echo "python: $(python3 --version 2>&1)"
  echo "tmux: $(tmux -V 2>/dev/null || echo MISSING)"
  echo "terminfo: $(infocmp xterm-256color 2>/dev/null | head -1 || echo MISSING)"
  echo "opencode: $(timeout 10 opencode --version 2>&1 | head -1 || echo 'NOT PROBED')"
  echo "omp: $(timeout 10 omp --version 2>&1 | head -1 || echo 'NOT PROBED')"
  echo "orca: $(probe_orca_version)"
} > "$EV/versions.txt"

echo "=== versions ==="
cat "$EV/versions.txt"
echo "=== evidence in $EV ==="
ls -la "$EV"

if [ "$FAIL" = 0 ]; then echo "SPIKE RESULT: ALL PROBES PASSED"; else echo "SPIKE RESULT: FAILURES PRESENT (see evidence/*.json)"; fi
exit "$FAIL"
