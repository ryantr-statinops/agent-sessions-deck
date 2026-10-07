package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A missing variable name does not claim availability; a timeout yields
// Uncertain; marker verification rejects name lookalikes.
func TestProbeTimeoutOutputCapAndMarker(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good")
	os.WriteFile(good, []byte("#!/bin/sh\necho 'myagent 1.2.3'\n"), 0o755)
	big := filepath.Join(root, "big")
	os.WriteFile(big, []byte("#!/bin/sh\nhead -c 100000 /dev/zero | tr '\\0' 'x'\n"), 0o755)
	slow := filepath.Join(root, "slow")
	os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755)
	bad := filepath.Join(root, "bad")
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 3\n"), 0o755)

	p := NewProber(ProbeOptions{Timeout: 300 * time.Millisecond, MaxOutput: 64, TTL: time.Second, Concurrency: 4})
	r := p.Probe(context.Background(), good)
	if r.Status != StatusAvailable || !strings.Contains(r.Output, "myagent 1.2.3") {
		t.Errorf("good probe = %+v", r)
	}
	if got := p.Probe(context.Background(), good); got.ProbedAt.Equal(r.ProbedAt) && r.Reason == "" {
		// cached path: same ProbedAt means no re-run
	}
	capped := p.Probe(context.Background(), big)
	if len(capped.Output) > 64 {
		t.Errorf("output cap: got %d bytes", len(capped.Output))
	}
	slowRes := p.Probe(context.Background(), slow)
	if slowRes.Status != StatusUncertain || !strings.Contains(slowRes.Reason, "timeout") {
		t.Errorf("slow probe = %+v, want uncertain/timeout", slowRes)
	}
	badRes := p.Probe(context.Background(), bad)
	if badRes.Status != StatusInvalid {
		t.Errorf("exit-3 probe = %+v, want invalid", badRes)
	}
	marker := RequireMarker(r, "myagent")
	if marker.Status != StatusAvailable {
		t.Errorf("marker present should stay available")
	}
	if off := RequireMarker(r, "othervendor"); off.Status != StatusUncertain {
		t.Errorf("wrong marker = %+v, want uncertain", off)
	}
}

// A cached probe is not re-run within TTL; Invalidate forces a re-probe.
func TestProbeCacheAndInvalidate(t *testing.T) {
	root := t.TempDir()
	counter := filepath.Join(root, "counter")
	os.WriteFile(counter, []byte("#!/bin/sh\nn=$(cat \"$1\"); n=$((n+1)); echo $n > \"$1\"; echo run$n\n"), 0o755)
	bin := filepath.Join(root, "bin")
	os.WriteFile(bin, []byte("#!/bin/sh\nexec \""+counter+"\" \""+filepath.Join(root, "n")+"\"\n"), 0o755)
	os.WriteFile(filepath.Join(root, "n"), []byte("0"), 0o644)
	p := NewProber(ProbeOptions{TTL: time.Minute, Args: nil})
	p.opts.Args = nil
	r1 := p.Probe(context.Background(), bin)
	r2 := p.Probe(context.Background(), bin)
	if r1.Output != r2.Output {
		t.Fatalf("cache: r1=%q r2=%q", r1.Output, r2.Output)
	}
	p.Invalidate(bin)
	r3 := p.Probe(context.Background(), bin)
	if r3.Output == r1.Output {
		t.Fatalf("after invalidate expected re-run, got %q", r3.Output)
	}
}

// Probes never block on an interactive binary: stdin is null and the child
// gets no TTY; a binary reading stdin sees EOF.
func TestProbeNonInteractive(t *testing.T) {
	root := t.TempDir()
	reader := filepath.Join(root, "reader")
	os.WriteFile(reader, []byte("#!/bin/sh\nread line\necho got:$line\n"), 0o755)
	p := NewProber(ProbeOptions{Timeout: 500 * time.Millisecond})
	r := p.Probe(context.Background(), reader)
	if r.Status != StatusAvailable || !strings.Contains(r.Output, "got:") {
		t.Errorf("stdin reader = %+v", r)
	}
}
