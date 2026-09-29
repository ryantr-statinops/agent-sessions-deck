#!/usr/bin/env python3
"""Stage 00 terminal spike: two concurrent sessions, detach, screen restore.

Models the ASD requirement: session A keeps running while the user views
session B, and A's screen is intact when the user returns.

What is proven with executable evidence:
  T1 detached-progress: while A is not drained (detached), its process keeps
     producing output (logfile grows, tick numbers advance).
  T2 screen-restore: after re-attach, draining A yields the LATEST ticks, and
     the source tick sequence has no gaps (nothing lost at this volume).
  T3 view-B: B advances independently the whole time (sessions isolated).
  T4 detach-stall-bound: under output flood with no reader, the detached
     child blocks on a full PTY buffer (timestamp gap >> attached baseline),
     so a production runtime must keep draining detached sessions in the
     background. Raw handoff (read only the viewed session) stalls agents.

Usage: python3 two_sessions.py [--out evidence/two_sessions.json]
"""
import fcntl
import json
import os
import pty
import re
import select
import shutil
import signal
import sys
import tempfile
import time

TICK_RE = re.compile(rb"tick (\d+) ts=(\d+)")
BUF_CAP = 256 * 1024


class Session:
    def __init__(self, label, logpath, fast=False):
        self.label = label
        self.logpath = logpath
        if os.path.exists(logpath):
            os.unlink(logpath)
        if fast:
            loop = ("i=0; while true; do printf '%s tick %%d ts=%%s\\n' $i \"$(date +%%s%%N)\"; "
                    "head -c 2048 /dev/zero | tr '\\0' P; echo; "
                    "echo \"%s tick $i ts=$(date +%%s%%N)\" >> %s; i=$((i+1)); sleep 0.02; done"
                    % (label, label, logpath))
        else:
            loop = ("i=0; while true; do L='%s tick '\"$i\"' ts='\"$(date +%%s%%N)\"; "
                    "echo \"$L\"; echo \"$L\" >> %s; i=$((i+1)); sleep 0.15; done"
                    % (label, logpath))
        pid, master = pty.fork()
        if pid == 0:
            try:
                import tty as _tty
                _tty.setraw(0)
                os.execvp("bash", ["bash", "-c", loop])
            except BaseException:
                os._exit(127)
        self.pid = pid
        self.master = master
        flags = fcntl.fcntl(master, fcntl.F_GETFL)
        fcntl.fcntl(master, fcntl.F_SETFL, flags | os.O_NONBLOCK)
        self.buf = bytearray()

    def drain(self, duration):
        deadline = time.time() + duration
        while time.time() < deadline:
            r, _, _ = select.select([self.master], [], [],
                                    max(0.0, deadline - time.time()))
            if not r:
                break
            try:
                chunk = os.read(self.master, 65536)
            except OSError:
                break
            if not chunk:
                break
            self.buf += chunk
            if len(self.buf) > BUF_CAP:
                del self.buf[:len(self.buf) - BUF_CAP]

    def ticks(self):
        return [(int(n), int(ts)) for n, ts in TICK_RE.findall(bytes(self.buf))]

    def last_tick(self):
        t = self.ticks()
        return t[-1][0] if t else -1

    def log_ticks(self):
        try:
            with open(self.logpath, "rb") as f:
                return [(int(n), int(ts)) for n, ts in TICK_RE.findall(f.read())]
        except FileNotFoundError:
            return []

    def close(self):
        try:
            os.kill(self.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        try:
            os.waitpid(self.pid, 0)
        except ChildProcessError:
            pass
        try:
            os.close(self.master)
        except OSError:
            pass


def max_gap_ms(ticks):
    gaps = [(b[1] - a[1]) // 1_000_000 for a, b in zip(ticks, ticks[1:])]
    return (max(gaps) if gaps else 0), len(ticks)


def make_scratch():
    """Unique private scratch dir for this probe invocation.

    Under run.sh it lives inside $ASD_SPIKE_RUN_DIR; standalone it is a
    0700 mkdtemp under $TMPDIR. Only the returned dir is ever removed.
    Children are pty.fork direct children reaped by exact PID (no pkill).
    """
    base = os.environ.get("ASD_SPIKE_RUN_DIR")
    if base and os.path.isdir(base):
        return tempfile.mkdtemp(prefix="two_sessions-", dir=base)
    return tempfile.mkdtemp(prefix="asd-spike-two_sessions-")


def main():
    out_path = sys.argv[sys.argv.index("--out") + 1] if "--out" in sys.argv else None
    tmp = make_scratch()
    results = []
    A = B = C = None
    try:
        A = Session("A", tmp + "/A.log")
        B = Session("B", tmp + "/B.log")
        # Phase 1: both attached.
        A.drain(1.0); B.drain(1.0)
        a0, b0 = A.last_tick(), B.last_tick()

        # Phase 2: A detached (not drained), B viewed.
        a_buf_before = len(A.buf)
        B.drain(2.5)
        a_log = A.log_ticks()
        b1 = B.last_tick()
        t1 = (len(a_log) > 0 and a_log[-1][0] > a0
              and len(A.buf) == a_buf_before)
        results.append({"name": "detached_progress", "passed": bool(t1),
                        "detail": "A tick at detach=%d, A log latest=%d (grew while "
                                  "undrained=%s), B advanced %d->%d"
                                  % (a0, a_log[-1][0] if a_log else -1,
                                     len(A.buf) == a_buf_before, b0, b1)})

        # Phase 3+4: re-attach A, screen restores to latest output.
        A.drain(1.5)
        a2 = A.last_tick()
        a_log2 = A.log_ticks()
        seq = [n for n, _ in a_log2]
        contiguous = all(b - a == 1 for a, b in zip(seq, seq[1:])) if len(seq) > 1 else True
        t2 = a2 > a0 and contiguous
        results.append({"name": "screen_restore", "passed": bool(t2),
                        "detail": "A tick %d -> %d after re-attach, source sequence "
                                  "contiguous=%s (%d ticks)" % (a0, a2, contiguous, len(seq))})

        t3 = b1 > b0
        results.append({"name": "session_isolation", "passed": bool(t3),
                        "detail": "B advanced independently %d -> %d" % (b0, b1)})

        # Phase 5: flood while detached -> writer stalls (must drain in bg).
        C = Session("C", tmp + "/C.log", fast=True)
        C.drain(1.0)
        base = C.log_ticks()
        base_gap, base_n = max_gap_ms(base)
        C.buf.clear()
        time.sleep(2.0)  # detached: nobody reads; PTY buffer fills, child blocks
        C.drain(2.0)     # re-attach and catch up
        after = C.log_ticks()
        stall_gap, after_n = max_gap_ms(after)
        t4 = base_n >= 2 and stall_gap > max(500, base_gap * 5)
        results.append({"name": "detach_stall_bound", "passed": bool(t4),
                        "detail": "attached n=%d max gap=%dms; with 2s undrained "
                                  "flood n=%d max gap=%dms (child blocked on full PTY buffer)"
                                  % (base_n, base_gap, after_n, stall_gap)})
    finally:
        for s in (A, B, C):
            if s is not None:
                s.close()
        # Remove only the exact scratch dir this invocation created.
        # Inside run.sh the dir lives under $ASD_SPIKE_RUN_DIR and the
        # runner trap removes any remainder; standalone we remove it here.
        try:
            shutil.rmtree(tmp, ignore_errors=True)
        except OSError:
            pass

    for r in results:
        print(("PASS" if r["passed"] else "FAIL"), r["name"], "-", r["detail"], flush=True)
    if out_path:
        with open(out_path, "w") as f:
            json.dump({"probe": "two_sessions", "results": results}, f, indent=2)
    failed = [r for r in results if not r["passed"]]
    print("two_sessions: %d/%d passed" % (len(results) - len(failed), len(results)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
