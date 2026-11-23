package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orchard-suite/hedge/internal/register"
)

// The test builds hedge once and drives real git, with git's global
// config and hedge's files all in a temporary folder.

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hedge-bin-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "hedge")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/hedge").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t   *testing.T
	dir string
}

func setup(t *testing.T) *env {
	t.Helper()
	d := t.TempDir()
	e := &env{t: t, dir: d}
	reg := filepath.Join(d, "register")
	if err := register.Init(reg, []string{"home", "self", "work"}); err != nil {
		t.Fatal(err)
	}
	r, err := register.Load(reg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ cat, term string }{{"work", "zorblax"}, {"work", "Globex Prime"}, {"home", "novaline"}, {"home", "kim@novaline.example"}, {"self", "Kim Novaline"}, {"work", "Kim Novaline"}} {
		if _, err := r.Add(tt.cat, tt.term, false); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(d, "config.yaml")
	write(t, cfg, "register:\n  path: "+reg+"\nfixtures: docs/fixtures.md\nzones:\n  authors: [self, home]\n  rules:\n    - match: github.com/acme-home/*\n      allow: [home]\n    - match: file:"+filepath.Join(d, "home")+"\n      allow: [home]\n")
	t.Setenv("HEDGE_CONFIG", cfg)
	t.Setenv("HEDGE_DATA", filepath.Join(d, "data"))
	t.Setenv("HEDGE_INDEX", filepath.Join(d, "cache", "index"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(d, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", d)
	e.git(d, "config", "--global", "user.name", "Ada Example")
	e.git(d, "config", "--global", "user.email", "ada@example.com")
	e.git(d, "config", "--global", "init.defaultBranch", "main")
	e.hedge("refresh")
	e.hedgeIn(d, "install")
	// install points the hooks at ~/.local/bin/hedge when there is one;
	// here it is the test build.
	for _, h := range hooks {
		e.git(d, "config", "--global", "hook."+h.name+".command", bin+" "+h.args)
	}
	return e
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

func (e *env) run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

func (e *env) git(dir string, args ...string) string {
	e.t.Helper()
	out, err := e.run(dir, "git", args...)
	if err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (e *env) hedge(args ...string) string { return e.hedgeIn(e.dir, args...) }

func (e *env) hedgeIn(dir string, args ...string) string {
	e.t.Helper()
	out, err := e.run(dir, bin, args...)
	if err != nil {
		e.t.Fatalf("hedge %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// repo makes a working repository whose origin is url.
func (e *env) repo(name, url string) string {
	d := filepath.Join(e.dir, name)
	e.git(e.dir, "init", "--quiet", d)
	e.git(d, "remote", "add", "origin", url)
	return d
}

func (e *env) commit(dir, file, text, msg string) (string, error) {
	write(e.t, filepath.Join(dir, file), text)
	e.git(dir, "add", file)
	return e.run(dir, "git", "commit", "--quiet", "-m", msg)
}

func TestACommitInANeutralRepositoryMayNameNoEstate(t *testing.T) {
	e := setup(t)
	d := e.repo("tool", "git@github.com:acme-tools/tool.git")
	out, err := e.commit(d, "a.go", "// talks to the Zorblax API\n", "feat: add a")
	if err == nil || !strings.Contains(out, `"Zorblax" names work`) || !strings.Contains(out, "docs/fixtures.md") {
		t.Fatalf("the commit was not stopped: %v\n%s", err, out)
	}
	if _, err := e.commit(d, "a.go", "// talks to the Acme API\n", "feat: add a"); err != nil {
		t.Fatal("a clean commit was stopped")
	}
	if out, err := e.commit(d, "b.go", "x\n", "fix: what novaline asked"); err == nil || !strings.Contains(out, "message") {
		t.Fatalf("a message that names a term was not stopped:\n%s", out)
	}
	// The owner signs with a home name anywhere, but never with work's.
	e.git(d, "config", "user.email", "kim@novaline.example")
	if out, err := e.commit(d, "c.go", "y\n", "fix: c"); err != nil {
		t.Fatalf("an author in an author category was stopped:\n%s", out)
	}
	e.git(d, "config", "user.email", "kim@zorblax.example")
	if out, err := e.commit(d, "d.go", "z\n", "fix: d"); err == nil || !strings.Contains(out, "author") {
		t.Fatalf("an author that names work was not stopped:\n%s", out)
	}
}

func TestARepositoryWithNoRemoteIsCheckedWhenItPushes(t *testing.T) {
	e := setup(t)
	d := filepath.Join(e.dir, "scratch")
	e.git(e.dir, "init", "--quiet", d)
	if out, err := e.commit(d, "a.md", "zorblax\n", "feat: a"); err != nil {
		t.Fatalf("a commit with no remote was stopped:\n%s", out)
	}
	bare := filepath.Join(e.dir, "out.git")
	e.git(e.dir, "init", "--quiet", "--bare", bare)
	if _, err := e.run(d, "git", "push", "--quiet", bare, "main"); err == nil {
		t.Fatal("its push was not stopped")
	}
}

func TestTheOwnersNameMayAppearInCreditsOnly(t *testing.T) {
	e := setup(t)
	d := e.repo("tool", "git@github.com:acme-tools/tool.git")
	if out, err := e.commit(d, "LICENSE", "Copyright 2026 Kim Novaline\n", "docs: license"); err != nil {
		t.Fatalf("a license could not credit its owner:\n%s", out)
	}
	if _, err := e.commit(d, "README.md", "Ask Kim Novaline.\n", "docs: readme"); err == nil {
		t.Fatal("a readme named a person")
	}
	e.git(d, "reset", "--quiet", "README.md")
	if out, err := e.commit(d, "go.sum", "example.com/x v1.0.0 h1:NovalineZorblaxAAAA=\n", "chore: sums"); err != nil {
		t.Fatalf("a lock file was read:\n%s", out)
	}
}

func TestAnEstateRepositoryMayNameItsOwnEstateOnly(t *testing.T) {
	e := setup(t)
	d := e.repo("house", "https://github.com/acme-home/house")
	if out, err := e.commit(d, "a.md", "the novaline box\n", "docs: a"); err != nil {
		t.Fatalf("a home repository could not name home:\n%s", out)
	}
	if _, err := e.commit(d, "b.md", "and the Globex Prime account\n", "docs: b"); err == nil {
		t.Fatal("a home repository named work")
	}
}

func TestARepositoryThatSetsItsOwnHooksStillRunsHedge(t *testing.T) {
	e := setup(t)
	d := e.repo("husky", "git@github.com:acme-tools/husky.git")
	write(t, filepath.Join(d, ".husky", "pre-commit"), "#!/bin/sh\nexit 0\n")
	_ = os.Chmod(filepath.Join(d, ".husky", "pre-commit"), 0o755)
	e.git(d, "config", "core.hooksPath", ".husky")
	if _, err := e.commit(d, "a.go", "zorblax\n", "feat: a"); err == nil {
		t.Fatal("core.hooksPath hid hedge's hook")
	}
}

func TestAPushIsCheckedInTheZoneItGoesTo(t *testing.T) {
	e := setup(t)
	d := e.repo("house", "https://github.com/acme-home/house")
	if out, err := e.commit(d, "a.md", "the novaline box\n", "docs: a"); err != nil {
		t.Fatal(out)
	}
	// The same history pushed to an estate-neutral remote is stopped,
	// though every commit was fine where it was made.
	bare := filepath.Join(e.dir, "public.git")
	e.git(e.dir, "init", "--quiet", "--bare", bare)
	e.git(d, "remote", "add", "public", "https://github.com/acme-tools/public")
	e.git(d, "config", "remote.public.pushurl", bare)
	out, err := e.run(d, "git", "push", "--quiet", "public", "main")
	if err == nil || !strings.Contains(out, "This push to github.com/acme-tools/public") {
		t.Fatalf("the push was not stopped: %v\n%s", err, out)
	}
	// Its own estate takes it: the remote and the folder git pushes to are
	// both home.
	home := filepath.Join(e.dir, "home.git")
	e.git(e.dir, "init", "--quiet", "--bare", home)
	e.git(d, "config", "remote.origin.pushurl", home)
	if out, err := e.run(d, "git", "push", "--quiet", "origin", "main"); err != nil {
		t.Fatalf("a push home was stopped:\n%s", out)
	}
}

func TestAuditFindsWhatHistoryHoldsAndAnIgnoreWaivesIt(t *testing.T) {
	e := setup(t)
	d := e.repo("house", "https://github.com/acme-home/house")
	if out, err := e.commit(d, "a.md", "the novaline box\n", "docs: a"); err != nil {
		t.Fatal(out)
	}
	e.git(d, "rm", "--quiet", "a.md")
	e.git(d, "commit", "--quiet", "-m", "docs: drop a")
	out, err := e.run(d, bin, "audit", "--history", "--zone-of", "git@github.com:acme-tools/public.git")
	if err == nil || !strings.Contains(out, "1 findings") || !strings.Contains(out, "commit ") {
		t.Fatalf("history was not audited:\n%s", out)
	}
	if out := e.hedgeIn(d, "audit"); !strings.Contains(out, "clean") {
		t.Fatalf("the tree has nothing:\n%s", out)
	}
	id := out[strings.Index(out, "hedge-") : strings.Index(out, "hedge-")+18]
	facts, err := e.run(d, bin, "audit", "--history", "--facts", "--zone-of", "git@github.com:acme-tools/public.git")
	if err != nil || !strings.Contains(facts, `"fact":"hedge"`) || !strings.Contains(facts, id) || strings.Contains(facts, "novaline") {
		t.Fatalf("facts: %v\n%s", err, facts)
	}
	write(t, filepath.Join(d, ".hedgeignore"), id+" a.md\n")
	if out, err := e.run(d, bin, "audit", "--history", "--zone-of", "git@github.com:acme-tools/public.git"); err != nil {
		t.Fatalf("the ignore did not waive it:\n%s", out)
	}
}

func TestDoctorProvesTheGuardEndToEnd(t *testing.T) {
	e := setup(t)
	out := e.hedge("doctor")
	if strings.Contains(out, "FAIL") {
		t.Fatal(out)
	}
}
