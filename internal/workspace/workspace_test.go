package workspace

import (
	"errors"
	"slices"
	"testing"
)

// TestWorkspaceIdentityIsTheCanonicalDirectory keeps one directory one workspace,
// however the user spelled the path.
func TestWorkspaceIdentityIsTheCanonicalDirectory(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{input: "/home/tester/kestrel", want: "/home/tester/kestrel"},
		{input: "/home/tester/kestrel/", want: "/home/tester/kestrel"},
		{input: "/home/tester/kestrel//", want: "/home/tester/kestrel"},
		{input: "/home/tester/./kestrel", want: "/home/tester/kestrel"},
		{input: "/home/tester/observability/../kestrel", want: "/home/tester/kestrel"},
		{input: "/", want: "/"},
	}
	for _, tc := range cases {
		got, err := Canonical(tc.input)
		if err != nil {
			t.Errorf("Canonical(%q): %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.input, got, tc.want)
		}
		first, err := New(tc.input)
		if err != nil {
			t.Errorf("New(%q): %v", tc.input, err)
			continue
		}
		second, err := New(tc.want)
		if err != nil {
			t.Fatalf("New(%q): %v", tc.want, err)
		}
		if !first.Equal(second) {
			t.Errorf("%q and %q resolved to different workspaces", tc.input, tc.want)
		}
		if first.ID().Path() != tc.want {
			t.Errorf("identity = %q, want %q", first.ID(), tc.want)
		}
	}
}

// TestWorkspacePathValidationRefusesUnverifiedInput documents what Stage 02 cannot
// promise without touching the filesystem: tilde, relative and NUL-bearing paths
// are the resolver's job, and they are refused rather than guessed.
func TestWorkspacePathValidationRefusesUnverifiedInput(t *testing.T) {
	invalid := []struct {
		path string
	}{
		{path: ""},
		{path: "kestrel"},
		{path: "./kestrel"},
		{path: "~/kestrel"},
		{path: "/home/tester/kestrel\x00"},
		{path: " /home/tester/kestrel"},
		{path: "/home/tester/kestrel "},
	}
	for _, tc := range invalid {
		if _, err := Canonical(tc.path); err == nil {
			t.Errorf("Canonical(%q) was accepted", tc.path)
		} else {
			var typed *InvalidPathError
			if !errors.As(err, &typed) {
				t.Errorf("Canonical(%q) produced an untyped error %v", tc.path, err)
			}
		}
	}

	if (ID("kestrel")).Valid() {
		t.Fatalf("a relative workspace id was accepted")
	}
	if (ID("/home/tester/kestrel/")).Valid() {
		t.Fatalf("a non-canonical workspace id was accepted")
	}
	if _, err := NewID("/home/tester/./kestrel"); err != nil {
		t.Fatalf("NewID refused a normalizable path: %v", err)
	}
}

// TestGitMetadataNeverReplacesTheSelectedDirectory keeps a session in the
// directory the user chose, even when the repository root is higher up.
func TestGitMetadataNeverReplacesTheSelectedDirectory(t *testing.T) {
	subdirectory, err := New("/home/tester/kestrel/internal/session")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	withGit, err := subdirectory.WithGit(Git{
		Root:         "/home/tester/kestrel",
		Branch:       "feature/executor",
		Dirty:        true,
		ChangedFiles: 3,
	})
	if err != nil {
		t.Fatalf("WithGit: %v", err)
	}
	if withGit.Path() != "/home/tester/kestrel/internal/session" {
		t.Fatalf("path = %q, want the selected subdirectory", withGit.Path())
	}
	git, ok := withGit.Git()
	if !ok {
		t.Fatalf("git metadata is missing")
	}
	if git.Root != "/home/tester/kestrel" {
		t.Fatalf("git root = %q, want the repository root", git.Root)
	}
	if !git.Dirty || git.ChangedFiles != 3 {
		t.Fatalf("dirty state was lost: %+v", git)
	}
	if subdirectory.HasGit() {
		t.Fatalf("WithGit mutated the receiver")
	}
}

func TestGitValidation(t *testing.T) {
	cases := []struct {
		name    string
		git     Git
		wantErr bool
	}{
		{name: "clean branch", git: Git{Root: "/repo", Branch: "main"}},
		{name: "dirty branch", git: Git{Root: "/repo", Branch: "main", Dirty: true, ChangedFiles: 2}},
		{name: "detached head", git: Git{Root: "/repo", Detached: true}},
		{name: "missing root", git: Git{Branch: "main"}, wantErr: true},
		{name: "relative root", git: Git{Root: "repo", Branch: "main"}, wantErr: true},
		{name: "non-canonical root", git: Git{Root: "/repo/", Branch: "main"}, wantErr: true},
		{name: "negative count", git: Git{Root: "/repo", Branch: "main", ChangedFiles: -1}, wantErr: true},
		{name: "unnamed branch", git: Git{Root: "/repo"}, wantErr: true},
		{name: "unnamed dirty branch", git: Git{Root: "/repo", Dirty: true, ChangedFiles: 3}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.git.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validation error = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil {
				return
			}
			var typed *InvalidGitError
			if !errors.As(err, &typed) {
				t.Fatalf("error %v is not typed", err)
			}
		})
	}

	if got := (Git{Root: "/repo", Detached: true}).String(); got != "/repo @ detached (clean)" {
		t.Fatalf("String = %q", got)
	}
	if got := (Git{Root: "/repo", Branch: "main", Dirty: true, ChangedFiles: 4}).String(); got != "/repo @ main (4 changed)" {
		t.Fatalf("String = %q", got)
	}
}

// TestResolutionOrderFollowsTheDocumentedPriority is the workspace-source priority:
// explicit path, current directory, configured, recent - and never a disk scan.
func TestResolutionOrderFollowsTheDocumentedPriority(t *testing.T) {
	want := []Source{SourceExplicit, SourceCurrentDirectory, SourceConfigured, SourceRecent}
	if !slices.Equal(AllSources(), want) {
		t.Fatalf("AllSources = %v, want %v", AllSources(), want)
	}

	recent, err := NewResolveRequest("/home/tester/recent", SourceRecent)
	if err != nil {
		t.Fatalf("NewResolveRequest: %v", err)
	}
	explicit, err := NewResolveRequest("/home/tester/explicit", SourceExplicit)
	if err != nil {
		t.Fatalf("NewResolveRequest: %v", err)
	}
	configured, err := NewResolveRequest("/home/tester/configured", SourceConfigured)
	if err != nil {
		t.Fatalf("NewResolveRequest: %v", err)
	}

	input := []ResolveRequest{recent, explicit, configured}
	ordered := ByPriority(input)
	if ordered[0] != explicit || ordered[1] != configured || ordered[2] != recent {
		t.Fatalf("resolution order = %v", ordered)
	}
	if input[0] != recent {
		t.Fatalf("ByPriority reordered the caller's slice")
	}
	for i, req := range ordered {
		if req.Path() == "" {
			t.Fatalf("request %d lost its path", i)
		}
		if i > 0 && ordered[i-1].Source().Priority() > req.Source().Priority() {
			t.Fatalf("request %d (%s) is out of resolution order", i, req.Source())
		}
	}

	if _, err := NewResolveRequest("/home/tester/kestrel", Source("somewhere-else")); err == nil {
		t.Fatalf("an unknown workspace source was accepted")
	}
	if _, err := NewResolveRequest("", SourceExplicit); err == nil {
		t.Fatalf("an empty candidate path was accepted")
	}
	if unknown := Source("somewhere-else"); unknown.Valid() || unknown.Priority() != len(AllSources()) {
		t.Fatalf("unknown source ranked %d", unknown.Priority())
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"/home/tester/kestrel":      "kestrel",
		"/home/tester/kestrel/":     "kestrel",
		"/":                         "/",
		"/home/tester/my project-2": "my project-2",
	}
	for path, want := range cases {
		w, err := New(path)
		if err != nil {
			t.Fatalf("New(%q): %v", path, err)
		}
		if got := w.DisplayName(); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", path, got, want)
		}
	}
	if (Workspace{}).DisplayName() != "" {
		t.Fatalf("the zero workspace has a display name")
	}
}
