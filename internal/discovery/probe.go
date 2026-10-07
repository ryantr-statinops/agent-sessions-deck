package discovery

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Status is the honest outcome of a bounded probe. "available" requires a
// successful probe with output; a name match alone never yields it.
type Status string

const (
	StatusAvailable Status = "available"
	StatusNotFound  Status = "not-found"
	StatusInvalid   Status = "invalid"
	StatusUncertain Status = "uncertain"
)

// Result is the observed provenance of one probe. OutputCap bytes are kept;
// longer vendor banners are truncated, never trusted fully.
type Result struct {
	Path   string
	Status Status
	// Output is the capped, first-line-joined probe output.
	Output   string
	Reason   string
	ProbedAt time.Time
}

// ProbeOptions bounds every probe: context timeout, output cap, concurrency
// and cache TTL. Defaults keep a slow or chatty binary from stalling the
// launcher.
type ProbeOptions struct {
	Timeout     time.Duration
	MaxOutput   int64
	Concurrency int
	TTL         time.Duration
	Args        []string
}

func (o ProbeOptions) withDefaults() ProbeOptions {
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Second
	}
	if o.MaxOutput <= 0 {
		o.MaxOutput = 4096
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.TTL <= 0 {
		o.TTL = 30 * time.Second
	}
	if len(o.Args) == 0 {
		o.Args = []string{"--version"}
	}
	return o
}

// Prober runs bounded, cached, non-interactive probes. Stdin is null and the
// process never gets a terminal, so an interactive-only binary fails fast
// instead of hanging the launcher.
type Prober struct {
	opts ProbeOptions
	sem  chan struct{}

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	result Result
	exp    time.Time
}

// NewProber builds a prober with defaults applied.
func NewProber(opts ProbeOptions) *Prober {
	opts = opts.withDefaults()
	return &Prober{opts: opts, sem: make(chan struct{}, opts.Concurrency), cache: map[string]cacheEntry{}}
}

// Probe reports the status of one executable. Results are cached for TTL;
// Invalidate drops one entry, and a ctx cancellation never leaves a cached
// half-result.
func (p *Prober) Probe(ctx context.Context, path string) Result {
	opts := p.opts
	key := path + "\x00" + strings.Join(opts.Args, "\x00")
	p.mu.Lock()
	if e, ok := p.cache[key]; ok && time.Now().Before(e.exp) {
		p.mu.Unlock()
		return e.result
	}
	p.mu.Unlock()

	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	case <-ctx.Done():
		return Result{Path: path, Status: StatusUncertain, Reason: ctx.Err().Error(), ProbedAt: time.Now()}
	}

	pctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, path, opts.Args...)
	cmd.Stdin = nil // never interactive
	var out bytes.Buffer
	cmd.Stdout = &cappedWriter{buf: &out, max: opts.MaxOutput}
	cmd.Stderr = &cappedWriter{buf: &out, max: opts.MaxOutput}
	err := cmd.Run()
	res := Result{Path: path, Output: strings.TrimSpace(out.String()), ProbedAt: time.Now()}
	switch {
	case errors.Is(pctx.Err(), context.DeadlineExceeded):
		res.Status, res.Reason = StatusUncertain, "probe timeout"
	case err != nil:
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.Status, res.Reason = StatusInvalid, "exit "+exitErr.ProcessState.String()
		} else {
			res.Status, res.Reason = StatusUncertain, err.Error()
		}
	default:
		res.Status = StatusAvailable
	}
	p.mu.Lock()
	p.cache[key] = cacheEntry{result: res, exp: time.Now().Add(opts.TTL)}
	p.mu.Unlock()
	return res
}

// Invalidate drops one cached probe.
func (p *Prober) Invalidate(path string) {
	p.mu.Lock()
	for k := range p.cache {
		if strings.HasPrefix(k, path+"\x00") {
			delete(p.cache, k)
		}
	}
	p.mu.Unlock()
}

// RequireMarker reports Available only when an observed output contains the
// vendor identity marker. Every adapter must check identity positively;
// a missing marker means Uncertain, never Available. This is what stops ASD
// from treating an arbitrary `orca`/`omp` binary (e.g. the Linux screen
// reader) as a coding agent.
func RequireMarker(r Result, marker string) Result {
	if r.Status != StatusAvailable {
		return r
	}
	if strings.Contains(r.Output, marker) {
		return r
	}
	r.Status = StatusUncertain
	r.Reason = "identity marker not observed"
	return r
}

type cappedWriter struct {
	buf *bytes.Buffer
	max int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.max - int64(w.buf.Len())
	if remaining <= 0 {
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		w.buf.Write(p[:remaining])
		return len(p), nil
	}
	return w.buf.Write(p)
}
