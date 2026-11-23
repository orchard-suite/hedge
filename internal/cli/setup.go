package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/orchard-suite/hedge/internal/claude"
	"github.com/orchard-suite/hedge/internal/gitscan"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/register"
)

// hooks are hedge's git hooks, set in the global config. Git runs a hook
// set in config beside a repository's own hooks folder, so a repository
// that sets core.hooksPath (husky and the like) cannot hide them.
var hooks = []struct{ name, event, args string }{
	{"hedge-pre-commit", "pre-commit", "git pre-commit"},
	{"hedge-commit-msg", "commit-msg", "git commit-msg"},
	{"hedge-pre-push", "pre-push", "git pre-push"},
}

func self() string {
	exe, err := os.Executable()
	if err != nil {
		return "hedge"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	// A path from an installed link stays stable across upgrades.
	if link := filepath.Join(filepath.Dir(exe), "hedge"); link != exe {
		if _, err := os.Stat(link); err == nil {
			return link
		}
	}
	return exe
}

func install(ctx context.Context, stdout, stderr io.Writer) int {
	g := gitscan.Git{Dir: "."}
	exe := self()
	if home, err := os.UserHomeDir(); err == nil {
		if bin := filepath.Join(home, ".local", "bin", "hedge"); fileExists(bin) {
			exe = bin
		}
	}
	for _, h := range hooks {
		cmd := fmt.Sprintf("%q %s", exe, h.args)
		for _, kv := range [][2]string{{"hook." + h.name + ".event", h.event}, {"hook." + h.name + ".command", cmd}} {
			if _, err := g.Out(ctx, "config", "--global", kv[0], kv[1]); err != nil {
				fmt.Fprintln(stderr, "hedge:", err)
				return 1
			}
		}
	}
	fmt.Fprintf(stdout, "hedge: git runs %s before every commit, on every commit message, and before every push, in every repository.\n", exe)
	return 0
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// doctor proves the guard works here, end to end: it makes a throwaway
// repository whose remote is estate-neutral, and checks that a commit and
// a push that name a term are both stopped, and that the Claude hook
// stops the same write.
func doctor(ctx context.Context, stdout, stderr io.Writer, p register.Paths) int {
	bad := 0
	say := func(ok bool, format string, a ...any) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			bad++
		}
		fmt.Fprintf(stdout, "%s %s\n", mark, fmt.Sprintf(format, a...))
	}
	x, err := match.Open(p.Index)
	if err != nil {
		say(false, "index at %s: %v; run hedge refresh", p.Index, err)
		return 1
	}
	age := time.Since(time.Unix(x.Built(), 0)).Round(time.Minute)
	say(x.Terms() > 0, "index: %d terms from register %s, built %s ago", x.Terms(), x.Source(), age)
	say(age < 8*24*time.Hour, "index is less than a week old")

	g := gitscan.Git{Dir: "."}
	out, _ := g.Out(ctx, "config", "--global", "--get-regexp", `^hook\.hedge-`)
	for _, h := range hooks {
		say(bytes.Contains(out, []byte("hook."+h.name+".command")), "git hook %s is set", h.event)
	}

	probe, term := probeTerm(x)
	if term == "" {
		say(false, "no term in the register is guarded in an estate-neutral repository, so there is nothing to probe with")
		return 1
	}
	dir, err := os.MkdirTemp("", "hedge-doctor-")
	if err != nil {
		say(false, "%v", err)
		return 1
	}
	defer os.RemoveAll(dir)
	work, remote := filepath.Join(dir, "work"), filepath.Join(dir, "remote.git")
	run := func(d string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = d
		var b bytes.Buffer
		cmd.Stdout, cmd.Stderr = &b, &b
		err := cmd.Run()
		return b.String(), err
	}
	_ = os.MkdirAll(work, 0o755)
	steps := [][]string{
		{dir, "init", "--quiet", "--bare", remote},
		{work, "init", "--quiet", "-b", "main"},
		{work, "remote", "add", "origin", "https://github.com/" + probe},
		{work, "config", "remote.origin.pushurl", remote},
		{work, "config", "user.name", "Hedge Doctor"},
		{work, "config", "user.email", "doctor@example.com"},
	}
	for _, s := range steps {
		if o, err := run(s[0], s[1:]...); err != nil {
			say(false, "git %s: %s", strings.Join(s[1:], " "), o)
			return 1
		}
	}
	file := filepath.Join(work, "probe.txt")
	_ = os.WriteFile(file, []byte("probe: "+term+"\n"), 0o644)
	_, _ = run(work, "add", "probe.txt")
	o, err := run(work, "commit", "--quiet", "-m", "probe")
	say(err != nil && strings.Contains(o, "hedge:"), "a commit that names a term is stopped")
	o, err = run(work, "commit", "--quiet", "--no-verify", "-m", "probe")
	if err == nil {
		o, err = run(work, "push", "--quiet", "origin", "main")
		say(err != nil && strings.Contains(o, "hedge:"), "a push that names a term is stopped")
	} else {
		say(false, "could not make a commit to probe the push: %s", o)
	}
	in := claude.Input{Cwd: work, ToolName: "Write"}
	in.ToolInput.FilePath = filepath.Join(work, "new.txt")
	in.ToolInput.Content = term
	say(claude.Decide(in, x, nil).Block, "the Claude hook stops a write that names a term")
	in = claude.Input{Cwd: work, ToolName: "Bash"}
	in.ToolInput.Command = "git commit --no-verify -m x"
	say(claude.Decide(in, x, nil).Block, "the Claude hook stops --no-verify")
	if bad > 0 {
		return 1
	}
	return 0
}

// probeTerm finds a repository key in no zone rule, and a term that key
// may not name.
func probeTerm(x *match.Index) (key, term string) {
	key = "hedge-doctor/probe"
	allow, _ := x.Allow("github.com/" + key)
	s := match.NewScanner(x)
	for i := 0; i < x.Terms(); i++ {
		t := x.Text(i)
		if hits := s.Scan([]byte(t), allow, nil); len(hits) > 0 && hits[0].Cats != 0 {
			return key, t
		}
	}
	return key, ""
}
