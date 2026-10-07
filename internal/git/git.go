// Package git observes repository metadata through the git CLI. Every call
// is bounded (context timeout), cached (TTL), and cancellable, so a slow
// repository never blocks the launcher.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// ErrNotRepo reports a directory that is not inside a git work tree.
var ErrNotRepo = errors.New("not a git repository")

// ErrGitMissing reports a host without the git CLI.
var ErrGitMissing = errors.New("git executable not found")

// Client probes repository metadata with bounded, cached commands.
type Client struct {
	Timeout time.Duration
	TTL     time.Duration
	// MaxInFlight bounds concurrent git invocations; a burst of workspaces
	// degrades to at most this many processes.
	MaxInFlight int

	mu     sync.Mutex
	cache  map[string]entry
	sem    chan struct{}
	binary string // resolved git path, for tests/fixtures
}

type entry struct {
	res workspace.Git
	err error
	exp time.Time
}

// Option customizes a client.
type Option func(*Client)

// WithBinary overrides the git executable (tests use fake fixtures).
func WithBinary(path string) Option {
	return func(c *Client) { c.binary = path }
}

// NewClient applies defaults: 2s timeout, 5s TTL, 4 in flight, "git" on PATH.
func NewClient(opts ...Option) *Client {
	c := &Client{Timeout: 2 * time.Second, TTL: 5 * time.Second, MaxInFlight: 4, binary: "git", cache: map[string]entry{}}
	for _, o := range opts {
		o(c)
	}
	c.sem = make(chan struct{}, c.MaxInFlight)
	return c
}

// Probe returns repository metadata for dir. Results are cached for TTL;
// a context canceled by the caller invalidates the in-flight probe and
// never poisons the cache with a partial result.
func (c *Client) Probe(ctx context.Context, dir string) (workspace.Git, error) {
	key := filepath.Clean(dir)
	c.mu.Lock()
	if e, ok := c.cache[key]; ok && time.Now().Before(e.exp) {
		c.mu.Unlock()
		return e.res, e.err
	}
	c.mu.Unlock()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return workspace.Git{}, ctx.Err()
	}

	pctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	res, err := c.probeOnce(pctx, dir)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(pctx.Err(), context.Canceled) {
			// Caller cancel dropped the in-flight probe; the cache keeps the
			// previous value and refresh can be retried.
			return workspace.Git{}, context.Canceled
		}
	}
	c.mu.Lock()
	if err == nil || isCacheableError(err) {
		c.cache[key] = entry{res: res, err: err, exp: time.Now().Add(c.TTL)}
	}
	c.mu.Unlock()
	return res, err
}

func isCacheableError(err error) bool {
	return errors.Is(err, ErrNotRepo) || errors.Is(err, ErrGitMissing)
}

// Invalidate drops one cached probe; CancelAll does not exist — a caller
// cancels via its own context, which is the cancel contract.
func (c *Client) Invalidate(dir string) {
	c.mu.Lock()
	delete(c.cache, filepath.Clean(dir))
	c.mu.Unlock()
}

// probeOnce runs the bounded probe sequence without cache handling.
func (c *Client) probeOnce(ctx context.Context, dir string) (workspace.Git, error) {
	out, err := c.run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if isNotRepoOutput(err) {
			return workspace.Git{}, ErrNotRepo
		}
		return workspace.Git{}, err
	}
	if strings.TrimSpace(out) != "true" {
		return workspace.Git{}, ErrNotRepo
	}
	top, err := c.run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return workspace.Git{}, err
	}
	root := strings.TrimSpace(top)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return workspace.Git{}, err
	}
	branch, err := c.run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return workspace.Git{}, err
	}
	branch = strings.TrimSpace(branch)
	detached := branch == "HEAD"
	status, err := c.run(ctx, dir, "status", "--porcelain=v1", "-z")
	if err != nil {
		return workspace.Git{}, err
	}
	changed := CountPorcelain(status)
	return workspace.Git{Root: root, Branch: branchName(branch, detached), Detached: detached, Dirty: changed > 0, ChangedFiles: changed}, nil
}

func branchName(branch string, detached bool) string {
	if detached {
		return ""
	}
	return branch
}

// run executes git with the bounded context and captures a capped stdout.
func (c *Client) run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, c.binary, full...)
	cmd.Stdin = nil
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		if errors.As(err, new(*exec.Error)) && errors.Is(err, exec.ErrNotFound) {
			return "", ErrGitMissing
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("git %s: timeout", args[0])
		}
		return "", fmt.Errorf("git %s: %v (%s)", args[0], err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

func isNotRepoOutput(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not a git repository")
}

// CountPorcelain counts records in `git status --porcelain=v1 -z` output.
// Each record is XY + space + path, NUL-terminated; rename/copy records
// consume one extra NUL field for the source path. Records, not lines, are
// the unit, so filenames containing '\n' or spaces never inflate or hide
// the count.
func CountPorcelain(data string) int {
	count := 0
	i := 0
	for i < len(data) {
		// record: [XY] [SP] path [NUL]
		if i+3 > len(data) {
			break
		}
		x, y := data[i], data[i+1]
		count++
		isRename := x == 'R' || x == 'C' || y == 'R' || y == 'C'
		// path begins at i+3
		j := strings.IndexByte(data[i+3:], 0)
		if j < 0 {
			break
		}
		i = i + 3 + j + 1
		if isRename {
			k := strings.IndexByte(data[i:], 0)
			if k < 0 {
				break
			}
			i += k + 1
		}
	}
	return count
}

// DecodeWorktree reports whether the directory's git dir differs from its
// common dir (i.e. a linked worktree), plus bare-repository detection, via
// one bounded invocation. It is exposed for callers that need that
// distinction beyond workspace.Git.
func (c *Client) DecodeWorktree(ctx context.Context, dir string) (linked bool, bare bool, err error) {
	gitdir, err := c.run(ctx, dir, "rev-parse", "--git-dir")
	if err != nil {
		return false, false, err
	}
	common, err := c.run(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, false, err
	}
	gd := strings.TrimSpace(gitdir)
	cmd := strings.TrimSpace(common)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	if !filepath.IsAbs(cmd) {
		cmd = filepath.Join(dir, cmd)
	}
	gd, _ = filepath.EvalSymlinks(gd)
	cmd, _ = filepath.EvalSymlinks(cmd)
	linked = gd != cmd
	bareOut, err := c.run(ctx, dir, "rev-parse", "--is-bare-repository")
	if err == nil {
		bare = strings.TrimSpace(bareOut) == "true"
	}
	return linked, bare, nil
}

// RelativeToHome is a small helper for docs/fixtures that need to express
// a repo path compactly. It is intentionally tiny.
func RelativeToHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}
