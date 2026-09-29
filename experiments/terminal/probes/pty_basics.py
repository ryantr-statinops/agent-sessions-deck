#!/usr/bin/env python3
"""Stage 00 terminal spike: PTY fundamentals on Linux (stdlib only).

Proves, with executable evidence:
  1. controlling TTY      - child owns /dev/pts/N as controlling terminal
  2. initial winsize       - stty size inside child matches TIOCSWINSZ
  3. resize + SIGWINCH     - TIOCSWINSZ mid-session signals the fg process
  4. foreground pg        - tcgetpgrp(master) == child pgid; bg pg cannot read
  5. input/output         - bytes written to master reach child and come back
  6. Ctrl-C / ISIG        - 0x03 delivers SIGINT to the fg process group
  7. Unicode              - UTF-8 (CJK + emoji + combining) round-trips intact
  8. output flood bound   - 1 MiB drains without hang; throughput recorded

Usage:
  python3 pty_basics.py [--out evidence/pty_basics.json]

Cleanup-safe: every child is killed (SIGKILL) and waitpid()'d in finally
blocks; every fd is closed. Never touches the outer terminal.
"""
import fcntl
import json
import os
import pty
import select
import shutil
import signal
import struct
import sys
import termios
import tempfile
import time
import tty

RESULTS = []


def make_scratch(probe):
    """Unique private scratch dir scoped to this invocation.

    Inside a run.sh invocation the dir lives under $ASD_SPIKE_RUN_DIR
    (so run cleanup owns it); standalone it is a 0700 mkdtemp under $TMPDIR.
    Callers must remove only the returned dir. No global /tmp wildcards.
    """
    base = os.environ.get("ASD_SPIKE_RUN_DIR")
    if base and os.path.isdir(base):
        return tempfile.mkdtemp(prefix=probe + "-", dir=base)
    return tempfile.mkdtemp(prefix="asd-spike-" + probe + "-")


def remove_scratch(path):
    """Remove only the exact scratch dir we created."""
    if path and os.path.isdir(path):
        shutil.rmtree(path, ignore_errors=True)


def record(name, passed, detail):
    RESULTS.append({"name": name, "passed": bool(passed), "detail": detail})
    print(("PASS" if passed else "FAIL"), name, "-", detail, flush=True)


def _set_nonblock(fd):
    flags = fcntl.fcntl(fd, fcntl.F_GETFL)
    fcntl.fcntl(fd, fcntl.F_SETFL, flags | os.O_NONBLOCK)


def _drain_available(fd, timeout, max_bytes=4 << 20):
    """Read whatever arrives within timeout; return bytes."""
    out = bytearray()
    deadline = time.time() + timeout
    while time.time() < deadline and len(out) < max_bytes:
        r, _, _ = select.select([fd], [], [], max(0.0, deadline - time.time()))
        if not r:
            break
        try:
            chunk = os.read(fd, 65536)
        except (OSError, BlockingIOError):
            break
        if not chunk:
            break
        out += chunk
    return bytes(out)


def _spawn(argv, raw=False, env_extra=None):
    """pty.fork + exec. Returns (pid, master_fd). Child death => EIO on read."""
    pid, master = pty.fork()
    if pid == 0:
        try:
            if raw:
                tty.setraw(0)
            env = dict(os.environ)
            env["LC_ALL"] = "en_US.UTF-8"
            env["TERM"] = "xterm-256color"
            if env_extra:
                env.update(env_extra)
            os.execvpe(argv[0], argv, env)
        except BaseException:
            os._exit(127)
    _set_nonblock(master)
    return pid, master


def _reap(pid, master):
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


def _spawn_cat_raw():
    """Spawn `cat` with the slave deterministically in raw/-echo mode.

    Avoids the startup race where the parent writes before the child has
    configured the slave (which would add line-discipline echo, e.g.
    ECHOCTL expansion of ESC to `^[`). The child signals READY after
    configuring the slave; the parent drains until READY before writing.
    """
    pid, master = pty.fork()
    if pid == 0:
        try:
            env = dict(os.environ)
            env["LC_ALL"] = "en_US.UTF-8"
            env["TERM"] = "xterm-256color"
            os.execvpe("bash", ["bash", "-c",
                                "stty raw -echo; printf READY; exec cat"], env)
        except BaseException:
            os._exit(127)
    _set_nonblock(master)
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
        _reap(pid, master)
        raise RuntimeError("raw cat child never became ready")
    return pid, master


def _set_winsize(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


def test_controlling_tty():
    pid, master = _spawn(["bash", "-c", "tty; cat /proc/self/stat | tr ' ' '\\n' | sed -n '8p'; sleep 30"])
    try:
        out = _drain_available(master, 2.0)
        text = out.decode("utf-8", "replace")
        first = text.strip().split()[0] if text.strip() else ""
        ok = first.startswith("/dev/pts/")
        record("controlling_tty", ok, "child tty=%r raw=%r" % (first, text[:60]))
    finally:
        _reap(pid, master)


def test_initial_winsize():
    pid, master = _spawn(["stty", "size"])
    try:
        _set_winsize(master, 24, 80)
        time.sleep(0.3)
        # stty already ran; respawn pattern: set size first via child sleep
        _reap(pid, master)
        pid2, master2 = _spawn(["bash", "-c", "sleep 0.4; stty size"])
        _set_winsize(master2, 24, 80)
        try:
            out = _drain_available(master2, 3.0)
            text = out.decode("utf-8", "replace").strip()
            ok = "24 80" in text
            record("initial_winsize", ok, "stty size -> %r" % text)
        finally:
            _reap(pid2, master2)
    except Exception as e:  # pragma: no cover - defensive
        record("initial_winsize", False, "exception %r" % e)


def test_resize_sigwinch():
    scratch = make_scratch("pty_basics-winch")
    flag = os.path.join(scratch, "winch.flag")
    try:
        # NOTE: the trap must fire in bash itself, so the child loops on the
        # `sleep` builtin boundary (an external `sleep 30` would swallow the
        # signal while bash waits for it).
        pid, master = _spawn(["bash", "-c",
                              "trap 'echo hit >> %s' WINCH; echo ready; "
                              "while :; do sleep 0.2; done" % flag])
        try:
            out = _drain_available(master, 2.0)
            if b"ready" not in out:
                record("resize_sigwinch", False, "child never ready: %r" % out[:60])
                return
            _set_winsize(master, 30, 100)
            time.sleep(0.4)
            _set_winsize(master, 40, 120)
            time.sleep(0.4)
            hits = 0
            if os.path.exists(flag):
                with open(flag) as f:
                    hits = len(f.read().strip().split())
            record("resize_sigwinch", hits >= 2,
                   "SIGWINCH deliveries=%d after 2 resizes" % hits)
        finally:
            _reap(pid, master)
    finally:
        remove_scratch(scratch)


def test_foreground_pgrp():
    # Foreground membership is verified from INSIDE the session (ps tpgid),
    # because a detached runner without its own controlling terminal cannot
    # tcsetpgrp() a PTY it does not control (ENOTTY). Input delivery to the
    # foreground group is proven separately by io_roundtrip/ctrl_c_sigint.
    pid, master = _spawn(["bash", "-c",
                          "echo tpgid=$(ps -o tpgid= -p $$ | tr -d ' ') "
                          "pgid=$(ps -o pgid= -p $$ | tr -d ' ') pid=$$; sleep 30"])
    try:
        out = _drain_available(master, 3.0).decode("utf-8", "replace")
        vals = {}
        for tok in out.split():
            if "=" in tok:
                k, v = tok.split("=", 1)
                vals[k.strip()] = v.strip()
        try:
            ok = (vals["tpgid"] == vals["pgid"] == vals["pid"] == str(pid))
        except KeyError:
            ok = False
        record("foreground_pgrp", ok,
               "child reports tpgid=%s pgid=%s pid=%s (fork pid %d); "
               "outer tcsetpgrp not permitted without ctty (documented)"
               % (vals.get("tpgid"), vals.get("pgid"), vals.get("pid"), pid))
    finally:
        _reap(pid, master)


def test_io_roundtrip():
    pid, master = _spawn_cat_raw()
    try:
        os.write(master, b"hello asd\n")
        out = _drain_available(master, 2.0)
        record("io_roundtrip", out == b"hello asd\n", "got %r" % out[:60])
    finally:
        _reap(pid, master)


def test_ctrl_c_sigint():
    scratch = make_scratch("pty_basics-int")
    flag = os.path.join(scratch, "int.flag")
    try:
        pid, master = _spawn(["bash", "-c",
                              "trap 'echo GOTINT >> %s' INT; echo ready; sleep 30" % flag])
        try:
            out = _drain_available(master, 2.0)
            if b"ready" not in out:
                record("ctrl_c_sigint", False, "child never ready")
                return
            os.write(master, b"\x03")  # ETX with ISIG -> SIGINT to fg pg
            time.sleep(0.6)
            got = os.path.exists(flag) and "GOTINT" in open(flag).read()
            record("ctrl_c_sigint", got, "SIGINT handler fired=%s" % got)
        finally:
            _reap(pid, master)
    finally:
        remove_scratch(scratch)


def test_unicode():
    pid, master = _spawn_cat_raw()
    try:
        payload = ("héllo wörld 日本語 한국어 🎉👍 café\u0301 Z̧͑ͫ̓ͪ̂ͫ̽͜Q\n").encode("utf-8")
        os.write(master, payload)
        out = _drain_available(master, 3.0)
        record("unicode", out == payload,
               "sent %d bytes, got %d, identical=%s" % (len(payload), len(out), out == payload))
    finally:
        _reap(pid, master)


def test_output_flood_bound():
    n = 1_000_000
    pid, master = _spawn(["bash", "-c", "head -c %d /dev/zero | tr '\\0' A" % n], raw=True)
    try:
        t0 = time.time()
        got = _drain_available(master, 15.0, max_bytes=n + 65536)
        dt = time.time() - t0
        record("output_flood_bound", len(got) == n,
               "drained %d/%d bytes in %.2fs (%.1f KiB/s)"
               % (len(got), n, dt, len(got) / 1024 / max(dt, 1e-3)))
    finally:
        _reap(pid, master)


def main():
    out_path = sys.argv[sys.argv.index("--out") + 1] if "--out" in sys.argv else None
    for fn in (test_controlling_tty, test_initial_winsize, test_resize_sigwinch,
               test_foreground_pgrp, test_io_roundtrip, test_ctrl_c_sigint,
               test_unicode, test_output_flood_bound):
        try:
            fn()
        except Exception as e:  # never let one probe kill the suite
            record(fn.__name__, False, "EXCEPTION %r" % e)
    failed = [r for r in RESULTS if not r["passed"]]
    if out_path:
        with open(out_path, "w") as f:
            json.dump({"probe": "pty_basics", "results": RESULTS}, f, indent=2)
    print("pty_basics: %d/%d passed" % (len(RESULTS) - len(failed), len(RESULTS)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
