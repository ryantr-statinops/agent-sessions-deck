//go:build linux

package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/ryantr-statinops/agent-sessions-deck/tests/testagent/control"
)

// Terminal control sequences used by the full-screen and query modes.
const (
	ansiEnterAltScreen = "\x1b[?1049h"
	ansiExitAltScreen  = "\x1b[?1049l"
	ansiHome           = "\x1b[H"
	ansiCursorRow      = "\x1b[%d;1H"
	ansiEraseLine      = "\x1b[2K"
)

// floodLine is the fixed-width record emitted by flood mode. The format is
// deterministic so a test can reconstruct the exact expected byte stream.
func floodLine(i int) string {
	return fmt.Sprintf("ASD-TESTAGENT-FLOOD-%08d-0123456789abcdefghijklmnopqrstuvwxyz\n", i)
}

// floodRecordLen is the byte length of every flood record. It is derived from
// the format rather than hardcoded so the fixture and its tests cannot drift.
var floodRecordLen = len(floodLine(0))

// unicodeSamples is the fixed payload emitted by unicode mode.
var unicodeSamples = []struct{ name, text string }{
	{"ascii", "plain-ascii-0123456789"},
	{"cjk", "\u4f60\u597d\u4e16\u754c"},
	{"hiragana", "\u3072\u3089\u304c\u306a"},
	{"hangul", "\ud55c\uad6d\uc5b4"},
	{"emoji-scalar", "\U0001F642\U0001F680"},
	{"emoji-zwj", "\U0001F469\u200D\U0001F4BB"},
	{"combining", "e\u0301a\u0300"},
	{"box-drawing", "\u250c\u2500\u252c\u2510\u2502\u251c\u253c\u2524\u2514\u2534\u2518"},
	{"rtl", "\u0645\u0631\u062d\u0628\u0627"},
	{"halfwidth-kana", "\uff8a\uff7d\uff98\uff91\uff99"},
}

// terminalQueries is the fixed query set written by query mode.
var terminalQueries = []struct{ kind, seq string }{
	{"DA-primary", "\x1b[c"},
	{"DA-secondary", "\x1b[>c"},
	{"DSR-cursor", "\x1b[6n"},
	{"DECRQM-2004", "\x1b[?2004$p"},
}

// runEcho echoes every input line to the terminal and reports it on the
// control channel. It is the shared body of several modes.
func (f *fixture) runEcho() error {
	f.started()
	for {
		select {
		case ln, ok := <-f.lines:
			if !ok {
				f.finish(0, "stdin-eof")
				return nil
			}
			if err := f.write("ECHO " + f.opt.label + ":" + ln.data + "\r\n"); err != nil {
				return err
			}
			if err := f.emit(&control.Line{N: ln.n, Data: ln.data}); err != nil {
				return err
			}
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		}
	}
}

// runLine reports input lines on the control channel without writing anything
// to the terminal, isolating control traffic from terminal traffic.
func (f *fixture) runLine() error {
	f.started()
	for {
		select {
		case ln, ok := <-f.lines:
			if !ok {
				f.finish(0, "stdin-eof")
				return nil
			}
			if err := f.emit(&control.Line{N: ln.n, Data: ln.data}); err != nil {
				return err
			}
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		}
	}
}

// runANSI drives a full-screen agent: it enters the alternate screen, paints
// one frame per size, and repaints on every window change.
func (f *fixture) runANSI() error {
	signal.Notify(f.winch, syscall.SIGWINCH)
	defer signal.Stop(f.winch)
	f.started()
	if err := f.write(ansiEnterAltScreen); err != nil {
		return err
	}
	rows, cols := f.winsize()
	f.drawFrame(rows, cols)
	leave := func(code int, reason string) {
		_ = f.write(ansiExitAltScreen)
		f.finish(code, reason)
	}
	for {
		select {
		case <-f.winch:
			rows, cols = f.winsize()
			_ = f.emit(&control.Size{Rows: rows, Cols: cols, Reason: "winch"})
			f.drawFrame(rows, cols)
		case ln, ok := <-f.lines:
			if !ok {
				leave(0, "stdin-eof")
				return nil
			}
			_ = f.write("\r\nECHO " + f.opt.label + ":" + ln.data + "\r\n")
			_ = f.emit(&control.Line{N: ln.n, Data: ln.data})
		case c, ok := <-f.cmds:
			if !ok {
				leave(0, "control-eof")
				return nil
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				leave(code, "control")
				return nil
			}
		}
	}
}

func (f *fixture) drawFrame(rows, cols int) {
	var b strings.Builder
	b.WriteString(ansiHome)
	for r := 1; r <= rows; r++ {
		fmt.Fprintf(&b, ansiCursorRow, r)
		b.WriteString(ansiEraseLine)
		fmt.Fprintf(&b, "FRAME %s %d %dx%d\r\n", f.opt.label, r, rows, cols)
	}
	_ = f.write(b.String())
	f.frameN++
	_ = f.emit(&control.Frame{N: f.frameN, Rows: rows, Cols: cols})
}

// runExitCode exits immediately with the status requested on the command line.
func (f *fixture) runExitCode() error {
	f.started()
	f.finish(f.opt.exitCode, "requested")
	return nil
}

// runSlowStart withholds the started event until the test sends an explicit
// release command. There is no timer anywhere in this path.
func (f *fixture) runSlowStart() error {
	for {
		select {
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if c.Cmd == control.CmdGo {
				f.started()
				return f.runEcho()
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		case ln, ok := <-f.lines:
			if !ok {
				f.finish(0, "input-eof")
				return nil
			}
			_ = ln
		}
	}
}

// runIgnoredTerm traps the termination signals and reports each one, so a test
// can prove the process survives them. It exits only on an explicit command.
func (f *fixture) runIgnoredTerm() error {
	signal.Notify(f.sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(f.sigCh)
	f.started()
	for {
		select {
		case s := <-f.sigCh:
			f.reportSignal(s)
		case ln, ok := <-f.lines:
			if !ok {
				f.finish(0, "stdin-eof")
				return nil
			}
			_ = f.write("ECHO " + f.opt.label + ":" + ln.data + "\r\n")
			_ = f.emit(&control.Line{N: ln.n, Data: ln.data})
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		}
	}
}

// runChild spawns a direct child and, when spawn is non-empty, a grandchild
// under that child. Identities are resolved from the kernel before they are
// reported.
func (f *fixture) runChild(spawn string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	d, err := spawnRole(exe, "child", f.opt.label, spawn)
	if err != nil {
		return err
	}
	f.children = append(f.children, d)

	rep, err := d.ident(identTimeout)
	if err != nil {
		return err
	}
	if err := f.resolve("child", rep.PID); err != nil {
		return err
	}
	if spawn != "" {
		grand, err := d.ident(identTimeout)
		if err != nil {
			return err
		}
		if err := f.resolve(spawn, grand.PID); err != nil {
			return err
		}
	}
	f.started()
	return f.runEcho()
}

// runFlood writes a bounded, deterministic burst to the terminal and reports
// what it wrote. The burst blocks on a full PTY buffer when nobody is reading,
// which is the behaviour a draining session runtime must tolerate.
func (f *fixture) runFlood() error {
	f.started()
	total := f.opt.floodLines
	if total <= 0 {
		bytes := f.opt.floodBytes
		if bytes <= 0 {
			bytes = 1 << 20
		}
		total = bytes / floodRecordLen
	}
	buf := make([]byte, 0, 1<<16)
	var written int
	for i := 0; i < total; i++ {
		rec := floodLine(i)
		buf = append(buf, rec...)
		written += len(rec)
		if len(buf) >= 1<<16 {
			if _, err := f.out.Write(buf); err != nil {
				return err
			}
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		if _, err := f.out.Write(buf); err != nil {
			return err
		}
	}
	if err := f.emit(&control.Flood{Bytes: written, Lines: total, RecordLen: floodRecordLen}); err != nil {
		return err
	}
	for {
		c, ok := <-f.cmds
		if !ok {
			f.finish(0, "control-eof")
			return nil
		}
		if code, cont := f.handleCommand(c, 0); !cont {
			f.finish(code, "control")
			return nil
		}
	}
}

// runUnicode writes a fixed multi-byte payload to the terminal, reporting each
// sample, and then echoes input lines byte for byte.
func (f *fixture) runUnicode() error {
	f.started()
	for _, s := range unicodeSamples {
		if err := f.write(s.text + "\r\n"); err != nil {
			return err
		}
		if err := f.emit(&control.Sample{Name: s.name, Hex: hexOf(s.text), Size: len(s.text)}); err != nil {
			return err
		}
	}
	return f.runEcho()
}

// rawChunk is one read from the terminal, delivered off the main loop so the
// query mode stays responsive to control commands while stdin is idle.
type rawChunk struct {
	data []byte
	err  error
}

func (f *fixture) runQuery() error {
	f.started()
	for _, q := range terminalQueries {
		if err := f.write(q.seq); err != nil {
			return err
		}
	}
	chunks := make(chan rawChunk, 16)
	go func() {
		defer close(chunks)
		tmp := make([]byte, 4096)
		for {
			n, err := f.in.Read(tmp)
			if n > 0 {
				buf := make([]byte, n)
				copy(buf, tmp[:n])
				chunks <- rawChunk{data: buf}
			}
			if err != nil {
				chunks <- rawChunk{err: err}
				return
			}
		}
	}()
	var pending []byte
	for {
		select {
		case ch, ok := <-chunks:
			if !ok {
				f.finish(0, "stdin-eof")
				return nil
			}
			if ch.err != nil {
				f.finish(0, "stdin-eof")
				return nil
			}
			pending = append(pending, ch.data...)
			for {
				reply, rest, found := extractReply(pending)
				if !found {
					break
				}
				pending = rest
				if err := f.emit(&control.Query{Kind: classifyReply(reply), Raw: string(reply)}); err != nil {
					return err
				}
			}
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if c.Cmd == control.CmdQuery {
				if err := f.write(c.Data); err != nil {
					return err
				}
				continue
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		}
	}
}

// extractReply pulls one terminal reply off the front of buf. A reply is a CSI
// sequence terminated by a DA, DSR or DECRPM final byte.
func extractReply(buf []byte) (reply, rest []byte, ok bool) {
	if len(buf) < 3 || buf[0] != 0x1b || buf[1] != '[' {
		return nil, buf, false
	}
	for i := 2; i < len(buf); i++ {
		switch buf[i] {
		case 'c', 'R', 'y', 'n':
			return buf[:i+1], buf[i+1:], true
		}
	}
	return nil, buf, false
}

func classifyReply(reply []byte) string {
	switch reply[len(reply)-1] {
	case 'c':
		return "DA"
	case 'R':
		return "DSR"
	default:
		return "DECRQM"
	}
}

// runSize reports the terminal size at start-up, on every window change, and
// on demand. Start-up is reported by run before dispatch.
func (f *fixture) runSize() error {
	signal.Notify(f.winch, syscall.SIGWINCH)
	defer signal.Stop(f.winch)
	f.started()
	for {
		select {
		case <-f.winch:
			rows, cols := f.winsize()
			_ = f.emit(&control.Size{Rows: rows, Cols: cols, Reason: "winch"})
			_ = f.write("SIZE " + strconv.Itoa(rows) + " " + strconv.Itoa(cols) + "\r\n")
		case ln, ok := <-f.lines:
			if !ok {
				f.finish(0, "stdin-eof")
				return nil
			}
			_ = f.write("ECHO " + f.opt.label + ":" + ln.data + "\r\n")
			_ = f.emit(&control.Line{N: ln.n, Data: ln.data})
		case c, ok := <-f.cmds:
			if !ok {
				f.finish(0, "control-eof")
				return nil
			}
			if code, cont := f.handleCommand(c, 0); !cont {
				f.finish(code, "control")
				return nil
			}
		}
	}
}
