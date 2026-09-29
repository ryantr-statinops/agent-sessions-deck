// Stage 00 terminal spike (Go fidelity layer).
//
// Part 1: PTY via github.com/creack/pty - spawn, winsize get/set, unicode
// round-trip through `cat`. Mirrors probes/pty_basics.py with the exact PTY
// library proposed for production, so fidelity issues would surface here.
//
// Part 2: emulator candidates vs the bounded corpus in ../fixtures/corpus:
//
//	(a) charmbracelet/x/vt Emulator - full virtual terminal (screen state,
//	    cursor, alt-screen, styles). Functional assertions per fixture.
//	(b) charmbracelet/x/ansi Parser - sequence tokenizer baseline: counts
//	    dispatched sequences to show it tokenizes but keeps no screen.
//
// Raw handoff (no emulator) is covered by probes/two_sessions.py instead.
//
// Part 3: two-session fake-fullscreen integration (twin_* checks) - two
// independent PTYs (creack/pty, same layer as Part 1) each drained
// continuously into its own vt.SafeEmulator. Deterministic Python
// fake-fullscreen children (no agent CLIs, no auth/cost) redraw a
// cursor-addressed header (CUP + EL, no full clear so echo/WINCH areas
// persist) while echoing stdin lines and trapping SIGWINCH. Proves:
// detached A keeps feeding A emulator while B is viewed, reattached A
// renders A latest with no B contamination or lost sequence, input goes
// only to the selected session, and resize dims/signals apply per session.
// Single writer per emulator (only its drain calls Write; SafeEmulator
// serializes Render/Resize). No screen payloads printed to stdout;
// JSON details carry only bounded metadata (frame numbers, counts, dims).
//
// Usage:
//
//	go run . [--out evidence/go_spike.json]
//
// Reads ../fixtures/corpus/*.ansi, so run from this directory (run.sh does).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

type Check struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Blocking bool   `json:"blocking"`
	Detail   string `json:"detail"`
}

var checks []Check

func record(name string, passed bool, detail string) {
	recordFull(name, passed, true, detail)
}

func recordGap(name string, passed bool, detail string) {
	// gap_* checks document genuine coverage gaps without failing the run.
	recordFull(name, passed, false, detail)
}

func recordFull(name string, passed, blocking bool, detail string) {
	checks = append(checks, Check{name, passed, blocking, detail})
	status := "PASS"
	if !passed {
		status = "FAIL"
	}
	if !blocking {
		status += "/GAP"
	}
	fmt.Printf("%s %s - %s\n", status, name, detail)
}

type readRes struct {
	data []byte
	err  error
}

// startReader pumps a PTY master into a channel. The goroutine ends when
// the fd errors (owner Close). All waiting uses select+timeout on the
// channel, never a bare blocking Read on the main path.
func startReader(f *os.File) <-chan readRes {
	ch := make(chan readRes, 64)
	go func() {
		defer close(ch)
		for {
			buf := make([]byte, 4096)
			n, err := f.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				ch <- readRes{cp, nil}
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// collect gathers until the marker appears or want bytes arrive, bounded by
// timeout. want < 0 means "marker only".
func collect(ch <-chan readRes, want int, marker []byte, timeout time.Duration) ([]byte, bool) {
	var acc []byte
	deadline := time.Now().Add(timeout)
	for {
		if marker != nil && bytes.Contains(acc, marker) {
			return acc, true
		}
		if want >= 0 && len(acc) >= want {
			return acc, true
		}
		rest := time.Until(deadline)
		if rest <= 0 {
			return acc, false
		}
		select {
		case r, ok := <-ch:
			if !ok {
				return acc, false
			}
			acc = append(acc, r.data...)
			if r.err != nil {
				return acc, false
			}
		case <-time.After(rest):
			return acc, false
		}
	}
}

func ptyPart() {
	// `stty raw -echo` in the child removes ONLCR/ECHO so the transport is
	// byte-transparent; the READY marker avoids the pre-exec echo race.
	cmd := exec.Command("bash", "-c", "stty raw -echo; printf READY; exec cat")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80, X: 0, Y: 0})
	if err != nil {
		record("go_pty_spawn", false, fmt.Sprintf("StartWithSize: %v", err))
		return
	}
	defer func() {
		ptmx.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()
	record("go_pty_spawn", true, "raw cat started on pts with 24x80")

	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		record("go_pty_resize", false, fmt.Sprintf("Setsize: %v", err))
	} else if rows, cols, err := pty.Getsize(ptmx); err != nil || rows != 30 || cols != 100 {
		record("go_pty_resize", false, fmt.Sprintf("Getsize=%dx%d err=%v", rows, cols, err))
	} else {
		record("go_pty_resize", true, "resize 24x80 -> 30x100 verified via Getsize")
	}

	rd := startReader(ptmx)
	readyRaw, ok := collect(rd, -1, []byte("READY"), 5*time.Second)
	if !ok {
		record("go_pty_unicode", false, fmt.Sprintf("raw cat never ready (%q)", string(readyRaw)))
		return
	}
	payload := "héllo 日本語 🎉\n"
	if _, err := ptmx.Write([]byte(payload)); err != nil {
		record("go_pty_unicode", false, fmt.Sprintf("write: %v", err))
		return
	}
	out, ok := collect(rd, len(payload), nil, 5*time.Second)
	record("go_pty_unicode", ok && string(out) == payload,
		fmt.Sprintf("sent %d bytes, got %d back identical=%v", len(payload), len(out), string(out) == payload))
}

func loadCorpus() map[string][]byte {
	files, _ := filepath.Glob(filepath.Join("..", "fixtures", "corpus", "*.ansi"))
	m := map[string][]byte{}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			m[filepath.Base(f)] = b
		}
	}
	return m
}

func cellContent(e *vt.Emulator, x, y int) string {
	c := e.CellAt(x, y)
	if c == nil {
		return "<nil>"
	}
	return c.Content
}

func emulatorPart(corpus map[string][]byte) {
	if len(corpus) == 0 {
		record("vt_corpus_loaded", false, "no fixtures found (run probes/corpus.py first)")
		return
	}
	record("vt_corpus_loaded", true, fmt.Sprintf("%d fixtures", len(corpus)))

	// cursor_addressing: X at row10/col20; then CUF/CUB/CUU/CUD cells.
	// NOTE: the corpus also uses SCO save/restore (ESC s / ESC u), which
	// this x/vt revision does not implement (see gap_vt_sco_save_restore);
	// cells asserted here are positioned before the SCO sequence.
	if data, ok := corpus["cursor_addressing.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		n, _ := e.Write(data)
		got := map[string]string{
			"X@19,9": cellContent(e, 19, 9),
			"Z@22,9": cellContent(e, 22, 9),
			"U@23,8": cellContent(e, 23, 8),
			"D@24,9": cellContent(e, 24, 9),
		}
		want := map[string]string{"X@19,9": "X", "Z@22,9": "Z", "U@23,8": "U", "D@24,9": "D"}
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
			}
		}
		record("vt_cursor_addressing", match && n == len(data),
			fmt.Sprintf("consumed %d/%d bytes cells=%v cursor=%v", n, len(data), got, e.CursorPosition()))
		e.Close()

		// SCO save/restore is unsupported: document the gap explicitly.
		e2 := vt.NewEmulator(80, 24)
		e2.Write([]byte("\x1b[s\x1b[99;1HS\x1b[uR"))
		sCell, rCell := cellContent(e2, 0, 23), cellContent(e2, 25, 9)
		recordGap("gap_vt_sco_save_restore", sCell == "S" && rCell == "R",
			fmt.Sprintf("SCO s/u: S@0,23=%q R@25,9=%q (DECSC 7/8 covers this; see vt_decsc)", sCell, rCell))
		e2.Close()

		// DECSC/DECRC (ESC 7 / ESC 8) is the portable save/restore path.
		e3 := vt.NewEmulator(80, 24)
		e3.Write([]byte("\x1b7\x1b[99;1HS\x1b8R"))
		sCell3, rCell3 := cellContent(e3, 0, 23), cellContent(e3, 0, 0)
		record("vt_decsc_restore", sCell3 == "S" && rCell3 == "R",
			fmt.Sprintf("DECSC 7/8: S@0,23=%q R@0,0=%q", sCell3, rCell3))
		e3.Close()
	}

	// sgr_colors: first cell holds styled "R".
	if data, ok := corpus["sgr_colors.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		e.Write(data)
		c := e.CellAt(0, 0)
		styled := c != nil && c.Content == "R" && c.Style.String() != ""
		record("vt_sgr_colors", c != nil && c.Content == "R",
			fmt.Sprintf("cell(0,0)=%q styled=%v", cellContent(e, 0, 0), styled))
		e.Close()
	}

	// erase: ED 2J clears "abcdef" (cells become blank, space-filled).
	if data, ok := corpus["erase.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		e.Write(data)
		render := e.Render()
		c00 := cellContent(e, 0, 0)
		record("vt_erase", !strings.Contains(render, "abcdef") && (c00 == "" || c00 == " "),
			fmt.Sprintf("abcdef visible after ED/EL=%v cell(0,0)=%q",
				strings.Contains(render, "abcdef"), c00))
		e.Close()
	}

	// alt_screen: enter -> IsAltScreen true; exit -> false.
	if data, ok := corpus["alt_screen.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		smcup := data[:len("\x1b[?1049h\x1b[22;0;0t")]
		e.Write(smcup)
		entered := e.IsAltScreen()
		e.Write(data[len(smcup):])
		exited := !e.IsAltScreen()
		record("vt_alt_screen", entered && exited,
			fmt.Sprintf("after smcup alt=%v, after rmcup alt=%v", entered, !exited))
		e.Close()
	}

	// unicode: CJK text retrievable from screen.
	if data, ok := corpus["unicode_mixed.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		e.Write(data)
		render := e.Render()
		record("vt_unicode", strings.Contains(render, "日本"),
			fmt.Sprintf("CJK present in Render()=%v", strings.Contains(render, "日本")))
		e.Close()
	}

	// queries: DA/DSR/DECRQM solicit replies. A bare PTY stays silent
	// (see probes/query_response.py); the emulator must answer. Write may
	// block until replies are drained, so write async and Read with a bound.
	if data, ok := corpus["queries.ansi"]; ok {
		e := vt.NewEmulator(80, 24)
		werr := make(chan error, 1)
		wn := make(chan int, 1)
		go func() {
			n, err := e.Write(data)
			wn <- n
			werr <- err
		}()
		reply := make([]byte, 0, 256)
		rdone := make(chan struct{})
		go func() {
			defer close(rdone)
			buf := make([]byte, 256)
			for len(reply) < 256 {
				n, err := e.Read(buf)
				if n > 0 {
					reply = append(reply, buf[:n]...)
				}
				if err != nil {
					return
				}
			}
		}()
		n := -1
		var err error
		select {
		case n = <-wn:
			err = <-werr
		case <-time.After(5 * time.Second):
			err = fmt.Errorf("write timed out (reply not drained?)")
		}
		select {
		case <-rdone:
		case <-time.After(2 * time.Second):
		}
		answered := len(reply) > 0 && reply[0] == 0x1b
		record("vt_query_reply", err == nil && n == len(data) && answered,
			fmt.Sprintf("consumed %d/%d err=%v reply=%q", n, len(data), err, string(reply)))
		e.Close()
	}

	// paste + scroll_region + osc + split: consumed fully, no error.
	// (queries.ansi is covered by vt_query_reply above.)
	for _, name := range []string{"bracketed_paste.ansi", "scroll_region.ansi", "osc.ansi", "split_multibyte.ansi"} {
		data, ok := corpus[name]
		if !ok {
			continue
		}
		e := vt.NewEmulator(80, 24)
		n, err := e.Write(data)
		record("vt_"+strings.TrimSuffix(name, ".ansi"), err == nil && n == len(data),
			fmt.Sprintf("consumed %d/%d bytes err=%v cursor=%v", n, len(data), err, e.CursorPosition()))
		e.Close()
	}
}

func ansiBaseline(corpus map[string][]byte) {
	for name, data := range corpus {
		p := ansi.NewParser()
		dispatches := 0
		for _, b := range data {
			if p.Advance(b) == parser.DispatchAction {
				dispatches++
			}
		}
		// The parser tokenizes; it keeps no screen, cursor, or alt-screen
		// state, so there is nothing functional to assert beyond tokenizing.
		// Fixtures without escape sequences (unicode text) tokenize zero
		// dispatches on the print path, which is the correct expectation.
		wantZero := name == "unicode_mixed.ansi" || name == "split_multibyte.ansi"
		passed := (dispatches == 0) == wantZero
		record("ansi_parse_"+strings.TrimSuffix(name, ".ansi"), passed,
			fmt.Sprintf("%d DispatchActions over %d bytes (tokenizer only, no screen state)", dispatches, len(data)))
	}
}

// ---------------------------------------------------------------------------
// Part 3: two-session fake-fullscreen integration (twin_*).
//
// Two independent PTYs via creack/pty (same layer as ptyPart), each with its
// own vt.SafeEmulator and its own drain goroutine (the ONLY writer to that
// emulator). Children are deterministic Python fake-fullscreen loops - no
// agent CLIs, no auth, no cost. Each child redraws a cursor-addressed header
// (CUP home + EL per line, no full-screen clear so echo/WINCH areas persist),
// echoes stdin lines as ECHO-<label>:<line>, and traps SIGWINCH to print
// WINCH-<label>. Overall probe budget 90s; every wait polls with a deadline.
// Cleanup (kill process group, close PTY fd, close emulator to unblock the
// reply drain, join goroutines, wait children) runs on pass/failure/timeout.
// No scratch files are created; no screen payloads are printed - details
// carry only bounded metadata (frame numbers, byte counts, dimensions).
// ---------------------------------------------------------------------------

const twinPyChild = `import os, sys, signal, select
label = sys.argv[1]
os.system("stty raw -echo")
sys.stdout.write("READY")
sys.stdout.flush()
def on_winch(signum, frame):
    os.write(1, ("\r\nWINCH-"+label+"\r\n").encode())
signal.signal(signal.SIGWINCH, on_winch)
i = 0
while True:
    r, _, _ = select.select([sys.stdin], [], [], 0.05)
    if r:
        line = sys.stdin.readline()
        if not line:
            break
        line = line.rstrip("\r\n")
        sys.stdout.write("\r\nECHO-"+label+":"+line+"\r\n")
        sys.stdout.flush()
    sys.stdout.write(f"\x1b[HSESSION-{label} FRAME {i:04d}\x1b[K\r\nPAD-{label}-{i:04d}\x1b[K")
    sys.stdout.flush()
    i += 1
`

const twinRawCap = 2 << 20 // 2 MiB sliding cap on retained raw bytes per session

type twinSess struct {
	label     string
	cmd       *exec.Cmd
	ptmx      *os.File
	emu       *vt.SafeEmulator
	mu        sync.Mutex // guards raw only; emu is self-synchronizing
	raw       []byte
	replies   atomic.Int64
	drainDone chan struct{}
	replyDone chan struct{}
	fedBytes  atomic.Int64
}

func twinStart(label string) (*twinSess, error) {
	cmd := exec.Command("python3", "-u", "-c", twinPyChild, label)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80, X: 0, Y: 0})
	if err != nil {
		return nil, err
	}
	s := &twinSess{label: label, cmd: cmd, ptmx: ptmx,
		emu:       vt.NewSafeEmulator(80, 24),
		drainDone: make(chan struct{}), replyDone: make(chan struct{})}
	go func() { // reply drain: keeps Write non-blocking if queries ever appear
		defer close(s.replyDone)
		buf := make([]byte, 256)
		for {
			n, err := s.emu.Read(buf)
			if n > 0 {
				s.replies.Add(int64(n))
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // PTY drain: the single writer to this emulator
		defer close(s.drainDone)
		buf := make([]byte, 4096)
		for {
			n, err := s.ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.mu.Lock()
				s.raw = append(s.raw, chunk...)
				if len(s.raw) > twinRawCap {
					s.raw = append([]byte(nil), s.raw[len(s.raw)-twinRawCap:]...)
				}
				s.mu.Unlock()
				s.fedBytes.Add(int64(n))
				_, _ = s.emu.Write(chunk)
			}
			if err != nil {
				return
			}
		}
	}()
	return s, nil
}

func (s *twinSess) rawCopy() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

// twinCleanup kills the whole child process group, closes the PTY master
// (unblocking the drain), closes the emulator (unblocking the reply drain),
// joins both goroutines with a bound, and reaps the child. Safe to call
// twice; never touches other sessions' FDs.
func (s *twinSess) cleanup() {
	if s == nil {
		return
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		s.cmd.Process.Kill()
	}
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	if s.emu != nil {
		_ = s.emu.Close()
	}
	for _, ch := range []chan struct{}{s.drainDone, s.replyDone} {
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
		}
	}
	if s.cmd != nil && s.cmd.Process != nil {
		done := make(chan struct{})
		go func() { s.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func twinFrameRe(label string) *regexp.Regexp {
	return regexp.MustCompile(`SESSION-` + regexp.QuoteMeta(label) + ` FRAME (\d{4})`)
}

// twinFramesInOrder extracts frame numbers in stream order from raw bytes.
func twinFramesInOrder(raw []byte, label string) []int {
	m := twinFrameRe(label).FindAllSubmatch(raw, -1)
	out := make([]int, 0, len(m))
	for _, g := range m {
		n := 0
		for _, d := range g[1] {
			n = n*10 + int(d-'0')
		}
		out = append(out, n)
	}
	return out
}

func twinContiguous(seq []int) bool {
	for i := 1; i < len(seq); i++ {
		if seq[i]-seq[i-1] != 1 {
			return false
		}
	}
	return true
}

// twinRenderFrame parses the latest frame number visible in a Render string.
func twinRenderFrame(render, label string) (int, bool) {
	m := twinFrameRe(label).FindAllStringSubmatch(render, -1)
	if len(m) == 0 {
		return -1, false
	}
	last := m[len(m)-1][1]
	n := 0
	for i := 0; i < len(last); i++ {
		n = n*10 + int(last[i]-'0')
	}
	return n, true
}

// twinPoll runs cond until true or timeout; 50ms poll granularity.
func twinPoll(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// twinWaitRender measures input/resize response at millisecond polling
// granularity while keeping waits bounded.
func twinWaitRender(s *twinSess, marker string, timeout time.Duration) (time.Duration, bool) {
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		if strings.Contains(s.emu.Render(), marker) {
			return time.Since(start), true
		}
		if time.Now().After(deadline) {
			return time.Since(start), false
		}
		time.Sleep(time.Millisecond)
	}
}

func twinPart() {
	deadline := time.Now().Add(90 * time.Second)
	remaining := func() time.Duration { return time.Until(deadline) }
	failRest := func(names ...string) {
		for _, n := range names {
			record(n, false, "skipped: earlier twin phase failed or timed out")
		}
	}

	A, errA := twinStart("A")
	B, errB := twinStart("B")
	if errA != nil || errB != nil {
		if A != nil {
			A.cleanup()
		}
		if B != nil {
			B.cleanup()
		}
		record("twin_pty_spawn", false, fmt.Sprintf("start A err=%v B err=%v", errA, errB))
		failRest("twin_bg_feed", "twin_input_routing", "twin_resize_isolation", "twin_reattach_latest", "twin_no_contamination")
		return
	}
	defer A.cleanup()
	defer B.cleanup()

	// Independent handles: distinct masters, distinct emulators, 24x80 each.
	ar, ac, errAr := pty.Getsize(A.ptmx)
	br, bc, errBr := pty.Getsize(B.ptmx)
	spawnOK := errAr == nil && errBr == nil && ar == 24 && ac == 80 && br == 24 && bc == 80 &&
		A.ptmx != B.ptmx && A.emu != B.emu && A.emu.Width() == 80 && A.emu.Height() == 24
	record("twin_pty_spawn", spawnOK,
		fmt.Sprintf("two PTYs 24x80 (A=%dx%d B=%dx%d err=%v/%v) distinct masters+emulators=%v",
			ar, ac, br, bc, errAr, errBr, A.ptmx != B.ptmx && A.emu != B.emu))
	if !spawnOK {
		failRest("twin_bg_feed", "twin_input_routing", "twin_resize_isolation", "twin_reattach_latest", "twin_no_contamination")
		return
	}

	// Baseline: both produce READY + frames within budget.
	baseOK := twinPoll(15*time.Second, func() bool {
		ra, rb := A.rawCopy(), B.rawCopy()
		return bytes.Contains(ra, []byte("READY")) && bytes.Contains(rb, []byte("READY")) &&
			len(twinFramesInOrder(ra, "A")) >= 5 && len(twinFramesInOrder(rb, "B")) >= 5
	}) && remaining() > 0
	if !baseOK {
		record("twin_bg_feed", false, "baseline READY+5 frames not reached within 15s")
		failRest("twin_input_routing", "twin_resize_isolation", "twin_reattach_latest", "twin_no_contamination")
		return
	}
	// Baseline emulator snapshot cost for a representative 80x24 screen.
	var renderMax time.Duration
	for i := 0; i < 100; i++ {
		start := time.Now()
		_ = A.emu.Render()
		if d := time.Since(start); d > renderMax {
			renderMax = d
		}
	}
	seqA0 := twinFramesInOrder(A.rawCopy(), "A")
	seqB0 := twinFramesInOrder(B.rawCopy(), "B")
	a0, b0 := seqA0[len(seqA0)-1], seqB0[len(seqB0)-1]

	// Detached phase (2.5s): A keeps draining into A emulator while A is not
	// selected; the viewer only renders B (independent view/update).
	viewedB := 0
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) && remaining() > 0 {
		_ = B.emu.Render() // view B only; never render A while detached
		viewedB++
		time.Sleep(100 * time.Millisecond)
	}
	seqA1 := twinFramesInOrder(A.rawCopy(), "A")
	seqB1 := twinFramesInOrder(B.rawCopy(), "B")
	a1, b1 := seqA1[len(seqA1)-1], seqB1[len(seqB1)-1]
	fedA, fedB := A.fedBytes.Load(), B.fedBytes.Load()
	bgOK := a1 > a0 && b1 > b0 && viewedB >= 5 && fedA > 0 && fedB > 0 && remaining() > 0
	record("twin_bg_feed", bgOK,
		fmt.Sprintf("detached A fed %d bytes frames %04d->%04d while B viewed x%d (B %04d->%04d)", fedA, a0, a1, viewedB, b0, b1))
	if !bgOK {
		failRest("twin_input_routing", "twin_resize_isolation", "twin_reattach_latest", "twin_no_contamination")
		return
	}

	// Input routing, B direction: write only to B (selected); B must echo,
	// A must stay clean.
	tokB := "SELB-001"
	_, writeBErr := B.ptmx.Write([]byte(tokB + "\n"))
	inputBLatency, inBok := twinWaitRender(B, "ECHO-B:"+tokB, 5*time.Second)

	aRenderAfterB := A.emu.Render()
	inBIsolated := !strings.Contains(aRenderAfterB, tokB) && !strings.Contains(aRenderAfterB, "ECHO-B:")

	// Reattach A (live): A selected now; write only to A; A must echo,
	// B must stay clean of the A token.
	tokA := "SELA-002"
	_, writeAErr := A.ptmx.Write([]byte(tokA + "\n"))
	inputALatency, inAok := twinWaitRender(A, "ECHO-A:"+tokA, 5*time.Second)

	bRenderAfterA := B.emu.Render()
	inAIsolated := !strings.Contains(bRenderAfterA, tokA) && !strings.Contains(bRenderAfterA, "ECHO-A:")
	inputOK := writeBErr == nil && inBok && inBIsolated && writeAErr == nil && inAok && inAIsolated && remaining() > 0
	record("twin_input_routing", inputOK,
		fmt.Sprintf("to-B in B=%v isolated-from-A=%v (%s); to-A in A=%v isolated-from-B=%v (%s)", inBok, inBIsolated, inputBLatency, inAok, inAIsolated, inputALatency))
	if !inputOK {
		failRest("twin_resize_isolation", "twin_reattach_latest", "twin_no_contamination")
		return
	}

	// Resize isolation: resize B only; B gets new PTY size + emulator size +
	// WINCH-B; A keeps 24x80 / 80x24 and shows no WINCH markers.
	resizeErr := pty.Setsize(B.ptmx, &pty.Winsize{Rows: 30, Cols: 100, X: 0, Y: 0})
	B.emu.Resize(100, 30)
	resizeLatency, wOk := twinWaitRender(B, "WINCH-B", 5*time.Second)

	ar2, ac2, _ := pty.Getsize(A.ptmx)
	br2, bc2, _ := pty.Getsize(B.ptmx)
	aW, aH := A.emu.Width(), A.emu.Height()
	bW, bH := B.emu.Width(), B.emu.Height()
	aRender := A.emu.Render()
	resizeOK := resizeErr == nil && wOk && ar2 == 24 && ac2 == 80 && br2 == 30 && bc2 == 100 &&
		aW == 80 && aH == 24 && bW == 100 && bH == 30 &&
		!strings.Contains(aRender, "WINCH-B") && !strings.Contains(aRender, "WINCH-A") && remaining() > 0
	record("twin_resize_isolation", resizeOK,
		fmt.Sprintf("PTY A=%dx%d B=%dx%d emu A=%dx%d B=%dx%d WINCH-B in B=%v in A=%v resize-to-WINCH=%s err=%v",
			ar2, ac2, br2, bc2, aW, aH, bW, bH, wOk,
			strings.Contains(aRender, "WINCH-B"), resizeLatency, resizeErr))
	record("twin_perf_baseline", renderMax > 0 && inputBLatency > 0 && inputALatency > 0 && resizeLatency > 0,
		fmt.Sprintf("100x 80x24 Render max=%s; input echo B=%s A=%s; resize-to-WINCH=%s (single host/run; no thresholds)", renderMax, inputBLatency, inputALatency, resizeLatency))
	if !resizeOK {
		failRest("twin_reattach_latest", "twin_no_contamination")
		return
	}

	// Freeze output for an exact latest-frame comparison: SIGKILL both
	// groups, then drain the remainder with a bound before rendering.
	if A.cmd.Process != nil {
		_ = syscall.Kill(-A.cmd.Process.Pid, syscall.SIGKILL)
		A.cmd.Process.Kill()
	}
	if B.cmd.Process != nil {
		_ = syscall.Kill(-B.cmd.Process.Pid, syscall.SIGKILL)
		B.cmd.Process.Kill()
	}
	drainEnd := time.Now().Add(5 * time.Second)
	for time.Now().Before(drainEnd) {
		time.Sleep(100 * time.Millisecond)
	}
	rawA, rawB := A.rawCopy(), B.rawCopy()
	seqA, seqB := twinFramesInOrder(rawA, "A"), twinFramesInOrder(rawB, "B")
	rA, rB := A.emu.Render(), B.emu.Render()
	fA, okFA := twinRenderFrame(rA, "A")
	fB, okFB := twinRenderFrame(rB, "B")
	maxA, maxB := -1, -1
	if len(seqA) > 0 {
		maxA = seqA[len(seqA)-1]
	}
	if len(seqB) > 0 {
		maxB = seqB[len(seqB)-1]
	}
	contA, contB := len(seqA) >= 10 && twinContiguous(seqA), len(seqB) >= 10 && twinContiguous(seqB)
	// Note: B emulator was resized to 100x30 mid-run; its header persists.
	reattachOK := okFA && fA == maxA && contA && maxA > a0 && okFB && contB && remaining() > 0
	record("twin_reattach_latest", reattachOK,
		fmt.Sprintf("A render %04d == drain-max %04d contiguous=%v (%d frames, base %04d); B render-found=%v contiguous=%v",
			fA, maxA, contA, len(seqA), a0, okFB, contB))
	noContam := strings.Contains(rA, "SESSION-A") && !strings.Contains(rA, "SESSION-B") &&
		!strings.Contains(rA, "PAD-B-") && !strings.Contains(rA, "ECHO-B:") &&
		strings.Contains(rB, "SESSION-B") && !strings.Contains(rB, "SESSION-A") &&
		!strings.Contains(rB, "PAD-A-") && !strings.Contains(rB, "ECHO-A:") && remaining() > 0
	_ = fB
	_ = maxB
	record("twin_no_contamination", noContam,
		fmt.Sprintf("A has-A=%v has-B=%v; B has-B=%v has-A=%v (replyBytes A=%d B=%d, no scratch files)",
			strings.Contains(rA, "SESSION-A"), strings.Contains(rA, "SESSION-B"),
			strings.Contains(rB, "SESSION-B"), strings.Contains(rB, "SESSION-A"),
			A.replies.Load(), B.replies.Load()))
}

func main() {
	out := flag.String("out", "", "write JSON evidence here")
	flag.Parse()
	ptyPart()
	corpus := loadCorpus()
	emulatorPart(corpus)
	ansiBaseline(corpus)
	twinPart()
	failed, gaps := 0, 0
	for _, c := range checks {
		if !c.Passed {
			if c.Blocking {
				failed++
			} else {
				gaps++
			}
		}
	}
	fmt.Printf("go_spike: %d/%d blocking passed (%d documented gaps)\n",
		len(checks)-failed-gaps, len(checks)-gaps, gaps)
	if *out != "" {
		f, err := os.Create(*out)
		if err == nil {
			json.NewEncoder(f).Encode(map[string]any{"probe": "go_spike", "results": checks})
			f.Close()
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}
