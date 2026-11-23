package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyOfNamesEveryRemoteFormTheSameWay(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:Acme/App.git":              "github.com/acme/app",
		"https://github.com/acme/app":              "github.com/acme/app",
		"https://user:tok@github.com/acme/app.git": "github.com/acme/app",
		"ssh://git@git.example.com:2222/g/sub/app": "git.example.com/g/sub/app",
		"github.com:acme/app":                      "github.com/acme/app",
		"/srv/mirror/app.git":                      "file:/srv/mirror/app",
		"":                                         "",
	} {
		if got := KeyOf(in); got != want {
			t.Errorf("KeyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindReadsOriginEvenFromAWorktreeAndANewFile(t *testing.T) {
	d := t.TempDir()
	main := filepath.Join(d, "app")
	write(t, filepath.Join(main, ".git", "config"), "[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = https://github.com/other/app\n[remote \"origin\"]\n\turl = git@github.com:acme/app.git\n")
	wt := filepath.Join(d, "wt")
	gd := filepath.Join(main, ".git", "worktrees", "wt")
	write(t, filepath.Join(gd, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gd+"\n")

	for _, p := range []string{filepath.Join(main, "a", "b", "new.go"), filepath.Join(wt, "x.go")} {
		r, ok := Find(p)
		if !ok || r.Key != "github.com/acme/app" {
			t.Errorf("Find(%s) = %+v %v", p, r, ok)
		}
	}
	if _, ok := Find(filepath.Join(d, "loose", "file")); ok {
		t.Error("a path outside any repository was in one")
	}
}
