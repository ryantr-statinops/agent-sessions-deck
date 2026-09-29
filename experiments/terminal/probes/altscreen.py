#!/usr/bin/env python3
"""Stage 00 terminal spike: alternate screen + cursor addressing transport.

Verifies the PTY layer passes smcup/rmcup/cup/ED/EL byte sequences through
unmodified (raw slave), i.e. the transport never interprets them. Screen
semantics belong to an emulator layer (see go/ spike + compat doc).

Usage: python3 altscreen.py [--out evidence/altscreen.json]
"""
import json
import os
import pty
import select
import signal
import subprocess
import sys
import time
import tty
import fcntl


def terminfo_cap(name):
    try:
        out = subprocess.run(["infocmp", "xterm-256color"], capture_output=True,
                             text=True, timeout=10).stdout
    except Exception:
        return None
    # crude parse: ,cap=...,
    for chunk in out.replace("\n\t", "").split(","):
        if chunk.strip().startswith(name + "="):
            return chunk.split("=", 1)[1]
    return None


def roundtrip(payload, timeout=2.0):
    pid, master = pty.fork()
    if pid == 0:
        try:
            os.execvp("bash", ["bash", "-c", "stty raw -echo; printf READY; exec cat"])
        except BaseException:
            os._exit(127)
    try:
        flags = fcntl.fcntl(master, fcntl.F_GETFL)
        fcntl.fcntl(master, fcntl.F_SETFL, flags | os.O_NONBLOCK)
        # Handshake: wait for READY so the slave is deterministically raw
        # before the payload arrives (avoids line-discipline echo races).
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
        os.write(master, payload)
        deadline = time.time() + timeout
        got = bytearray()
        while time.time() < deadline and len(got) < len(payload):
            r, _, _ = select.select([master], [], [], max(0.0, deadline - time.time()))
            if not r:
                break
            try:
                chunk = os.read(master, 65536)
            except OSError:
                break
            if not chunk:
                break
            got += chunk
        return bytes(got)
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


CASES = {
    # smcup/rmcup resolved at runtime from terminfo when available,
    # with xterm-256color fallbacks.
    "smcup": None,
    "rmcup": None,
    "cup_10_20": b"\x1b[10;20H",
    "ed_full": b"\x1b[2J",
    "el_line": b"\x1b[2K",
    "cup_then_text": b"\x1b[5;5Hhello",
}


def main():
    out_path = sys.argv[sys.argv.index("--out") + 1] if "--out" in sys.argv else None
    smcup = terminfo_cap("smcup") or "\\E[?1049h\\E[22;0;0t"
    rmcup = terminfo_cap("rmcup") or "\\E[?1049l\\E[23;0;0t"

    def unescape(s):
        return s.replace("\\E", "\x1b").encode("latin1")

    CASES["smcup"] = unescape(smcup)
    CASES["rmcup"] = unescape(rmcup)
    results = []
    for name, payload in CASES.items():
        got = roundtrip(payload)
        passed = got == payload
        results.append({"name": name, "passed": passed,
                        "detail": "sent %d bytes, got %d back, identical=%s"
                                  % (len(payload), len(got), passed)})
        print(("PASS" if passed else "FAIL"), name, "-", results[-1]["detail"], flush=True)
    if out_path:
        with open(out_path, "w") as f:
            json.dump({"probe": "altscreen", "results": results}, f, indent=2)
    failed = [r for r in results if not r["passed"]]
    print("altscreen: %d/%d passed" % (len(results) - len(failed), len(results)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
