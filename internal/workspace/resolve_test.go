package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathResolverTildeAndNestedCwd(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "proj")
	nested := filepath.Join(proj, "sub", "dir")
	os.MkdirAll(nested, 0o755)
	r := NewPathResolver(home, nil)
	req, _ := NewResolveRequest("~/proj", SourceExplicit)
	w, err := r.Resolve(context.Background(), req)
	if err != nil || w.Path() != proj {
		t.Fatalf("tilde expand: %v %q", err, w.Path())
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(nested)
	req2, _ := NewResolveRequest("../dir", SourceCurrentDirectory)
	w2, err := r.Resolve(context.Background(), req2)
	if err != nil || w2.Path() != nested {
		t.Fatalf("nested cwd: %v %q", err, w2.Path())
	}
}

func TestPathResolverSymlinkCanonical(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.MkdirAll(real, 0o755)
	link := filepath.Join(root, "link")
	os.Symlink(real, link)
	r := NewPathResolver("", nil)
	req, _ := NewResolveRequest(link, SourceExplicit)
	w, err := r.Resolve(context.Background(), req)
	if err != nil || w.Path() != real || w.ID().Path() != real {
		t.Fatalf("symlink: %v %q", err, w.Path())
	}
}

func TestPathResolverInvalidDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f")
	os.WriteFile(file, []byte("x"), 0o644)
	r := NewPathResolver("", nil)
	req, _ := NewResolveRequest(file, SourceExplicit)
	if _, err := r.Resolve(context.Background(), req); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file: %v", err)
	}
	req2, _ := NewResolveRequest(filepath.Join(root, "missing"), SourceExplicit)
	if _, err := r.Resolve(context.Background(), req2); err == nil {
		t.Errorf("missing should fail")
	}
	noread := filepath.Join(root, "noread")
	os.Mkdir(noread, 0o000)
	defer os.Chmod(noread, 0o755)
	req3, _ := NewResolveRequest(noread, SourceExplicit)
	if _, err := r.Resolve(context.Background(), req3); err == nil {
		t.Errorf("unreadable dir should fail")
	}
}

func TestPathResolverPriorityOrder(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	r := NewPathResolver("", nil)
	explicit, _ := NewResolveRequest(a, SourceExplicit)
	configured, _ := NewResolveRequest(b, SourceConfigured)
	w, errs := r.ResolveFirst(context.Background(), []ResolveRequest{configured, explicit})
	if len(errs) != 0 {
		t.Fatalf("errs %v", errs)
	}
	if w.Path() != a {
		t.Fatalf("priority: got %q", w.Path())
	}
	bad, _ := NewResolveRequest(filepath.Join(root, "nope"), SourceExplicit)
	_, errs2 := r.ResolveFirst(context.Background(), []ResolveRequest{bad})
	if len(errs2) != 1 {
		t.Fatalf("errs2 %v", errs2)
	}
}

func TestPathResolverGitProbe(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "d")
	os.MkdirAll(d, 0o755)
	r := NewPathResolver("", func(ctx context.Context, dir string) (Git, error) {
		return Git{Root: dir, Branch: "main"}, nil
	})
	req, _ := NewResolveRequest(d, SourceExplicit)
	w, err := r.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := w.Git()
	if !ok || g.Branch != "main" {
		t.Fatalf("git probe not attached: %v %+v", ok, g)
	}
}
