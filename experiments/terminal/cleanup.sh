#!/usr/bin/env bash
# Stage 00 terminal spike - cleanup path.
#   bash cleanup.sh                 # reap stale per-invocation dirs (keeps evidence)
#   bash cleanup.sh --wipe-evidence # also remove generated evidence/*.json*
#
# Safety contract (hardened):
# - No `pkill -f`, no `pgrep -f` kills, no /tmp wildcards. Another user's or
#   another run's same-named process is never signaled and never deleted.
# - Each run.sh invocation owns exactly one private dir (.run/run-<pid>-*).
#   This script removes only such dirs whose recorded owner is verifiably
#   dead (PID + starttime + cmdline check). Anything live, reused, or
#   unrecognized is REFUSED with a report and a nonzero exit, never killed.
# - Orphaned probe children of a dead run are signaled ONLY by exact
#   recorded PID after the same identity check (never by name). Uncertain
#   PIDs are left alone and reported.
# - All waits are bounded; normal cleanup (dead owners, own dirs) succeeds.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
RUN_ROOT="$HERE/.run"

proc_starttime() {
  if [ ! -r "/proc/$1/stat" ]; then echo "UNKNOWN"; return 1; fi
  sed 's/^.*) //' "/proc/$1/stat" 2>/dev/null | cut -d' ' -f20 2>/dev/null || echo "UNKNOWN"
}
proc_cmdline() {
  tr '\0' ' ' < "/proc/$1/cmdline" 2>/dev/null || echo ""
}
pid_alive() { kill -0 "$1" 2>/dev/null; }

# owner_live <pid> <starttime> <token>
# Returns 0 only if PID is alive AND starttime matches AND cmdline contains
# the expected token (run.sh / probe marker). Anything else is uncertain.
owner_live() {
  local pid="$1" start="$2" token="$3"
  case "$pid" in ''|*[!0-9]*) return 1;; esac
  pid_alive "$pid" || return 1
  local cur
  cur="$(proc_starttime "$pid" 2>/dev/null || echo UNKNOWN)"
  [ "$cur" != "UNKNOWN" ] && [ "$start" != "UNKNOWN" ] && [ "$cur" = "$start" ] || return 1
  case "$(proc_cmdline "$pid")" in
    *"$token"*) return 0;;
    *) return 1;;
  esac
}

kill_exact_pid() {
  # Signal one exact PID only after identity was verified. Bounded wait.
  local pid="$1" desc="$2"
  kill -TERM "$pid" 2>/dev/null || return 0
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    pid_alive "$pid" || return 0
    sleep 0.5
  done
  kill -KILL "$pid" 2>/dev/null || true
}

REFUSED=0
REAPED=0

if [ ! -d "$RUN_ROOT" ]; then
  echo "cleanup: no .run/ directory, nothing to do"
else
  shopt -s nullglob
  # Legacy single-file layout (pre-hardening): .run/runner.pid.
  if [ -f "$RUN_ROOT/runner.pid" ] && [ ! -d "$RUN_ROOT/runner.pid" ]; then
    echo "cleanup: legacy $RUN_ROOT/runner.pid layout detected"
    read -r leg_pid leg_start < "$RUN_ROOT/runner.pid" 2>/dev/null || { leg_pid=""; leg_start=""; }
    if [ -n "${leg_pid:-}" ] && owner_live "$leg_pid" "${leg_start:-UNKNOWN}" "run.sh"; then
      echo "cleanup: REFUSING legacy runner PID $leg_pid (still active)" >&2
      REFUSED=1
    else
      echo "cleanup: legacy runner dead/uncertain; removing only $RUN_ROOT/runner.pid (no kills, no /tmp sweep)"
      rm -f "$RUN_ROOT/runner.pid" || true
      REAPED=$((REAPED+1))
    fi
  fi
  for dir in "$RUN_ROOT"/run-*; do
    [ -d "$dir" ] || continue
    case "$dir" in */run-*) ;; *) echo "cleanup: REFUSING unrecognized dir $dir" >&2; REFUSED=1; continue;; esac
    if [ ! -f "$dir/runner.pid" ]; then
      echo "cleanup: REFUSING $dir (no runner.pid, ownership uncertain)" >&2
      REFUSED=1
      continue
    fi
    read -r rpid rstart < "$dir/runner.pid" 2>/dev/null || { rpid=""; rstart=""; }
    if [ -n "${rpid:-}" ] && owner_live "$rpid" "${rstart:-UNKNOWN}" "run.sh"; then
      echo "cleanup: REFUSING $dir (runner PID $rpid still active)" >&2
      REFUSED=1
      continue
    fi
    # Stale owner (dead or PID reused with mismatched identity): check for
    # recorded orphans before removing the dir.
    for cf in "$dir"/child-*.pid; do
      [ -f "$cf" ] || continue
      read -r cpid cstart crest < "$cf" 2>/dev/null || continue
      case "$cpid" in ''|*[!0-9]*) continue;; esac
      if pid_alive "$cpid"; then
        cur="$(proc_starttime "$cpid" 2>/dev/null || echo UNKNOWN)"
        cmd="$(proc_cmdline "$cpid")"
        if [ "$cur" != "UNKNOWN" ] && [ "${cstart:-UNKNOWN}" != "UNKNOWN" ] \
           && [ "$cur" = "$cstart" ] \
           && { case "$cmd" in *timeout*|*pty_basics*|*query_response*|*altscreen*|*two_sessions*|*corpus*|*go*run*) true;; *) false;; esac; }; then
          echo "cleanup: reaping verified orphan PID $cpid ($crest) from stale $dir"
          kill_exact_pid "$cpid" "$crest" || true
        else
          echo "cleanup: REFUSING to signal PID $cpid from $cf (identity uncertain; cmdline: $cmd)" >&2
          REFUSED=1
        fi
      fi
    done
    if [ "$REFUSED" = 1 ]; then
      # Do not remove dirs while any refusal is pending for safety review.
      echo "cleanup: keeping $dir pending refusal review" >&2
      continue
    fi
    echo "cleanup: removing stale $dir"
    rm -rf "$dir" || true
    REAPED=$((REAPED+1))
  done
  shopt -u nullglob
rmdir "$RUN_ROOT" 2>/dev/null || true
fi

# Legacy global /tmp residue from pre-hardening runs is NEVER swept here:
# ownership is uncertain after a crash. Report the limit instead.
echo "cleanup: not sweeping /tmp (uncertain ownership by design). Pre-hardening residue like /tmp/asd-spike-* or /tmp/pty_basics-* left by a killed run needs manual inspection."

if [ "${1:-}" = "--wipe-evidence" ]; then
  rm -f "$HERE"/evidence/*.json "$HERE"/evidence/versions.txt
  echo "cleanup: reaped $REAPED stale run dir(s); evidence/ wiped"
else
  echo "cleanup: reaped $REAPED stale run dir(s); evidence/ kept"
fi

if [ "$REFUSED" = 1 ]; then
  echo "cleanup: REFUSED one or more active/uncertain entries; no broad kills performed. Re-run when those owners exit." >&2
  exit 1
fi

# Verify only recorded owners are gone (no broad pgrep kill decision).
leftover=0
if [ -d "$RUN_ROOT" ]; then
  shopt -s nullglob
  for dir in "$RUN_ROOT"/run-*; do
    [ -d "$dir" ] || continue
    leftover=1
  done
  shopt -u nullglob
fi
if [ "$leftover" = 1 ]; then
  echo "WARNING: per-invocation run dirs remain (see above); inspect runner.pid files" >&2
  exit 1
fi
echo "cleanup: OK"
