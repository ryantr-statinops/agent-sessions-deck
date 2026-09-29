#!/usr/bin/env python3
"""Stage 00 terminal spike: bounded ANSI corpus vs the PTY transport.

The corpus lives in fixtures/corpus/*.ansi (written deterministically by
this script so reruns are identical). Each case is replayed through a bare
`cat` behind a PTY with a raw slave and must come back byte-identical:
the PTY layer is transparent, interpretation belongs to an emulator layer
(compare go/ spike results for emulator candidates).

One extra case (cooked_nl) uses default termios and PASSES when ONLCR
translates \\n -> \\r\\n, documenting cooked-mode behavior ASD must avoid
for faithful rendering.

Usage: python3 corpus.py [--out evidence/corpus.json]
"""
import fcntl
import json
import os
import pty
import select
import signal
import sys
import time
import tty

HERE = os.path.dirname(os.path.abspath(__file__))
FIXDIR = os.path.normpath(os.path.join(HERE, "..", "fixtures", "corpus"))

ESC = "\x1b"
CORPUS = {
    "sgr_colors.ansi":
        ESC + "[1;31mRED" + ESC + "[0m " + ESC + "[32mGREEN" + ESC + "[0m " +
        ESC + "[38;5;200m256" + ESC + "[0m " + ESC + "[38;2;10;20;30mTRUE" +
        ESC + "[0m " + ESC + "[1;4;7mATTR" + ESC + "[0m\n",
    "cursor_addressing.ansi":
        ESC + "[H" + ESC + "[10;20H" + "X" + ESC + "[2C" + "Y" + ESC + "[1D" +
        "Z" + ESC + "[A" + "U" + ESC + "[B" + "D" + ESC + "[s" + ESC + "[99;1H" +
        "S" + ESC + "[u" + "R",
    "erase.ansi":
        "abcdef" + ESC + "[2K" + ESC + "[1K" + ESC + "[0K" + ESC + "[2J" +
        ESC + "[0J" + ESC + "[1J",
    "alt_screen.ansi":
        ESC + "[?1049h" + ESC + "[22;0;0t" + "ALT-CONTENT" + ESC + "[?1049l" +
        ESC + "[23;0;0t",
    "unicode_mixed.ansi":
        "tiếng việt 日本語 한국어 🎉👍 café\u0301 Z̧͑ͫ̓ͪ̂ͫ̽͜Q «Ω≈ç√∫»\n",
    "osc.ansi":
        ESC + "]0;asd-title\x07" + ESC + "]8;;https://example.invalid\x07" +
        "link" + ESC + "]8;;\x07",
    "queries.ansi":
        ESC + "[c" + ESC + "[6n" + ESC + "[?2004$p",
    "bracketed_paste.ansi":
        ESC + "[200~pasted {\n multiline \t text}" + ESC + "[201~",
    "scroll_region.ansi":
        ESC + "[5;20r" + ESC + "[M" + ESC + "[L" + ESC + "[r",
}

COOKED_SEND = b"a\nb\n"
COOKED_EXPECT = b"a\r\nb\r\n"  # ONLCR under default termios


def roundtrip(payload, raw=True, chunk=4096, delay=0.0, timeout=3.0):
    pid, master = pty.fork()
    if pid == 0:
        try:
            if raw:
                os.execvp("bash", ["bash", "-c", "stty raw -echo; printf READY; exec cat"])
            else:
                # Cooked mode: no handshake possible without polluting the
                # echo record; sleep instead so `cat` is exec'd before we
                # write (startup race would add pre-exec line echo).
                os.execvp("cat", ["cat"])
        except BaseException:
            os._exit(127)
    try:
        flags = fcntl.fcntl(master, fcntl.F_GETFL)
        fcntl.fcntl(master, fcntl.F_SETFL, flags | os.O_NONBLOCK)
        if raw:
            deadline = time.time() + 5.0
            buf = bytearray()
            while time.time() < deadline and b"READY" not in buf:
                r, _, _ = select.select([master], [], [], max(0.0, deadline - time.time()))
                if not r:
                    break
                try:
                    chunk0 = os.read(master, 65536)
                except OSError:
                    break
                if not chunk0:
                    break
                buf += chunk0
        else:
            time.sleep(0.5)
            _discard(master)
        # write (optionally in small chunks to split multibyte sequences)
        for i in range(0, len(payload), chunk):
            os.write(master, payload[i:i + chunk])
            if delay:
                time.sleep(delay)
        deadline = time.time() + timeout
        want = len(COOKED_EXPECT) if not raw else len(payload)
        got = bytearray()
        while time.time() < deadline:
            r, _, _ = select.select([master], [], [], max(0.0, deadline - time.time()))
            if not r:
                break
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            got += data
            if len(got) >= want:
                # Settle briefly so chunked/slow echoes fully arrive,
                # then stop as soon as the line goes quiet.
                q0 = time.time() + 0.3
                while time.time() < q0 and time.time() < deadline:
                    r2, _, _ = select.select([master], [], [], max(0.0, q0 - time.time()))
                    if not r2:
                        break
                    try:
                        more = os.read(master, 65536)
                    except OSError:
                        break
                    if not more:
                        break
                    got += more
                    q0 = time.time() + 0.3
                break
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


def _discard(fd, timeout=0.3):
    deadline = time.time() + timeout
    while time.time() < deadline:
        r, _, _ = select.select([fd], [], [], max(0.0, deadline - time.time()))
        if not r:
            break
        try:
            if not os.read(fd, 65536):
                break
        except OSError:
            break


def main():
    out_path = sys.argv[sys.argv.index("--out") + 1] if "--out" in sys.argv else None
    os.makedirs(FIXDIR, exist_ok=True)
    results = []
    for name, text in CORPUS.items():
        payload = text.encode("utf-8")
        with open(os.path.join(FIXDIR, name), "wb") as f:
            f.write(payload)
        if name == "unicode_mixed.ansi":
            # also store the split-write variant fixture
            with open(os.path.join(FIXDIR, "split_multibyte.ansi"), "wb") as f:
                f.write(payload)
            got = roundtrip(payload, chunk=1, delay=0.001, timeout=8.0)
            label = "split_multibyte.ansi"
        else:
            got = roundtrip(payload)
            label = name
        passed = got == payload
        results.append({"name": label, "passed": passed,
                        "detail": "sent %d bytes, got %d, identical=%s"
                                  % (len(payload), len(got), passed)})
        print(("PASS" if passed else "FAIL"), label, "-", results[-1]["detail"], flush=True)
    got = roundtrip(COOKED_SEND, raw=False)
    # Cooked echo returns our own input chars plus cat's ONLCR output, so
    # assert the translated suffix rather than byte identity.
    passed = got.endswith(COOKED_EXPECT)
    results.append({"name": "cooked_nl", "passed": passed,
                    "detail": "cooked mode output ends with %r (got %r): ONLCR documented"
                              % (COOKED_EXPECT, got[:16])})
    print(("PASS" if passed else "FAIL"), "cooked_nl", "-", results[-1]["detail"], flush=True)

    if out_path:
        with open(out_path, "w") as f:
            json.dump({"probe": "corpus", "fixture_dir": "fixtures/corpus",
                       "results": results}, f, indent=2)
    failed = [r for r in results if not r["passed"]]
    print("corpus: %d/%d passed" % (len(results) - len(failed), len(results)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
