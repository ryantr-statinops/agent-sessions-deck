#!/usr/bin/env python3
"""Stage 00 terminal spike: terminal query/response (DA/DSR) behavior.

Sends Device Attributes (\\e[c), cursor-position report (\\e[6n) and a
DECRQM probe to a bare `cat` behind a PTY and waits for replies.

Expected finding: NO reply (a PTY is a transparent byte pipe; only a real
terminal emulator answers these). The probe PASSES when it observes exactly
that, documenting that ASD must answer such queries in its own emulator
layer instead of relying on the PTY.

Usage: python3 query_response.py [--out evidence/query_response.json]
"""
import json
import os
import pty
import select
import signal
import sys
import time
import tty

QUERIES = {
    "DA_primary": b"\x1b[c",        # request Device Attributes
    "DA_secondary": b"\x1b[>c",
    "DSR_cursor": b"\x1b[6n",       # request cursor position report
    "DECRQM_bracketed_paste": b"\x1b[?2004$p",
}


def spawn_raw_cat():
    """Spawn `cat` with slave deterministically raw/-echo, handshake READY."""
    pid, master = pty.fork()
    if pid == 0:
        try:
            os.execvp("bash", ["bash", "-c", "stty raw -echo; printf READY; exec cat"])
        except BaseException:
            os._exit(127)
    import fcntl
    flags = fcntl.fcntl(master, fcntl.F_GETFL)
    fcntl.fcntl(master, fcntl.F_SETFL, flags | os.O_NONBLOCK)
    deadline = time.time() + 5.0
    buf = bytearray()
    while time.time() < deadline and b"READY" not in buf:
        r, _, _ = select.select([master], [], [], max(0.0, deadline - time.time()))
        if not r:
            break
        try:
            chunk = os.read(master, 65536)
        except OSError:
            break
        if not chunk:
            break
        buf += chunk
    if b"READY" not in buf:
        raise RuntimeError("raw cat child never became ready")
    return pid, master


def run_once(query, timeout=1.0):
    pid, master = spawn_raw_cat()
    try:
        os.write(master, query)
        deadline = time.time() + timeout
        echoed = bytearray()
        while time.time() < deadline:
            r, _, _ = select.select([master], [], [], max(0.0, deadline - time.time()))
            if not r:
                break
            try:
                chunk = os.read(master, 65536)
            except OSError:
                break
            if not chunk:
                break
            echoed += chunk
        # `cat` echoes our own query bytes; a real answer would be EXTRA bytes
        # beyond the echo (e.g. b"\x1b[?62;...c" or b"\x1b[24;1R").
        extra = bytes(echoed).replace(query, b"", 1)
        return bytes(echoed), extra
    finally:
        try:
            os.kill(pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass
        try:
            os.waitpid(pid, 0)
        except ChildProcessError:
            pass
        try:
            os.close(master)
        except OSError:
            pass


def main():
    out_path = sys.argv[sys.argv.index("--out") + 1] if "--out" in sys.argv else None
    results = []
    for name, q in QUERIES.items():
        echoed, extra = run_once(q)
        # PASS = transparency confirmed: nothing beyond our own echo came back.
        passed = (extra == b"")
        results.append({"name": name, "passed": passed,
                        "detail": "sent %r, echoBytes=%d, extraBytes=%r"
                                  % (q, len(echoed), extra[:40])})
        print(("PASS" if passed else "FAIL"), name, "-", results[-1]["detail"], flush=True)
    if out_path:
        with open(out_path, "w") as f:
            json.dump({"probe": "query_response",
                       "expectation": "no replies without a terminal emulator",
                       "results": results}, f, indent=2)
    failed = [r for r in results if not r["passed"]]
    print("query_response: %d/%d passed" % (len(results) - len(failed), len(results)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
