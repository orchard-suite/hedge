// Package cli is hedge's command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchard-suite/hedge/internal/check"
	"github.com/orchard-suite/hedge/internal/claude"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/register"
	"github.com/orchard-suite/hedge/internal/repo"
)

// Version is set by the release.
var Version = "0.1.0"

const usage = `hedge keeps an estate's own names out of repositories that may not hold them.

Hooks (installed by hedge install and the hedge plugin):
  hedge claude                     Claude Code PreToolUse hook, reads stdin
  hedge git pre-commit             check the staged change and the author
  hedge git commit-msg <file>      check a commit message
  hedge git pre-push <remote> <url>  check every commit a push sends

Checking:
  hedge check [file...]            check files in their repositories' zones
  hedge audit [--history] [--opaque] [--facts] [--zone-of <url>] [dir]
                                   check a working tree, and with --history
                                   every commit on every ref
  hedge audit --facts --mirrors <dir> <url>...
                                   keep mirrors of remotes and print one
                                   conform fact per repository
  hedge publish <owner/name>       check the remote's whole history, then
                                   make the repository public
  hedge zone [path]                say what a repository may name

The register:
  hedge refresh [--harvest]        pull the register, harvest when due, and
                                   build the index
  hedge add <category> <term> [--exact]
  hedge ignore <id> [path-glob]    waive one finding in this repository
  hedge init-register <dir> <category>...

Setup:
  hedge install                    set hedge's git hooks for every repository
  hedge doctor                     prove the hooks block on this machine
  hedge version
`

// Main runs one command and returns its exit code.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	p := register.DefaultPaths()
	ctx := context.Background()
	switch args[0] {
	case "claude":
		return claude.Run(stdin, stderr, p.Index)
	case "git":
		return gitHook(ctx, args[1:], stdin, stderr, p)
	case "check":
		return checkFiles(args[1:], stdout, stderr, p)
	case "audit":
		return audit(ctx, args[1:], stdout, stderr, p)
	case "publish":
		return publish(ctx, args[1:], stdout, stderr, p)
	case "zone":
		return zone(args[1:], stdout, stderr, p)
	case "refresh":
		return refresh(ctx, args[1:], stdout, stderr, p)
	case "add":
		return add(ctx, args[1:], stdout, stderr, p)
	case "ignore":
		return ignore(args[1:], stdin, stdout, stderr, p)
	case "init-register":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "usage: hedge init-register <dir> <category>...")
			return 2
		}
		if err := register.Init(args[1], args[2:]); err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote a register with %s at %s.\n", strings.Join(args[2:], ", "), args[1])
		return 0
	case "install":
		return install(ctx, stdout, stderr)
	case "doctor":
		return doctor(ctx, stdout, stderr, p)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "hedge "+Version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "hedge: no command %q\n\n%s", args[0], usage)
	return 2
}

func openIndex(stderr io.Writer, p register.Paths) *match.Index {
	x, err := match.Open(p.Index)
	if err != nil {
		if err == match.ErrNoIndex {
			fmt.Fprintf(stderr, "hedge: there is no index at %s. Run `hedge refresh`.\n", p.Index)
		} else {
			fmt.Fprintln(stderr, "hedge:", err)
		}
		return nil
	}
	return x
}

func checkerFor(x *match.Index, key string) *check.Checker {
	allow, rule := x.Allow(key)
	return check.New(x, check.Zone{Key: key, Allow: allow, Rule: rule})
}

// refuse prints findings for a person or an agent at a terminal, with
// what to do next.
func refuse(w io.Writer, c *check.Checker, what string, fs []check.Finding) {
	fmt.Fprintf(w, "hedge: %s puts estate content where it may not go. %s.\n", what, c.Describe())
	check.Print(w, fs, false)
	fmt.Fprint(w, "Replace each with a made-up name, or leave it out.")
	if fx := c.X.Fixtures(); fx != "" {
		fmt.Fprint(w, " Names to use instead: "+fx)
	}
	fmt.Fprintln(w, "\nWhen hedge has one wrong, the owner waives it with `hedge ignore <id> [path]`.")
}

func checkFiles(args []string, stdout, stderr io.Writer, p register.Paths) int {
	x := openIndex(stderr, p)
	if x == nil {
		return 1
	}
	bad := 0
	for _, f := range args {
		abs, _ := filepath.Abs(f)
		r, ok := repo.Find(abs)
		if !ok {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		c := checkerFor(x, r.Key)
		_ = c.LoadIgnore(r.Root)
		rel, _ := filepath.Rel(r.Root, abs)
		fs := c.Text("file", filepath.ToSlash(rel), "", b, 1, nil)
		if len(fs) > 0 {
			bad += len(fs)
			refuse(stdout, c, rel, fs)
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

func zone(args []string, stdout, stderr io.Writer, p register.Paths) int {
	x := openIndex(stderr, p)
	if x == nil {
		return 1
	}
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	key := ""
	if strings.Contains(dir, "://") || strings.Contains(dir, "@") {
		key = repo.KeyOf(dir)
	} else if r, ok := repo.Find(dir); ok {
		key = r.Key
	} else {
		fmt.Fprintln(stdout, dir+" is in no repository: hedge does not guard it.")
		return 0
	}
	c := checkerFor(x, key)
	rule := c.Zone.Rule
	if rule == "" {
		rule = "the default"
	}
	fmt.Fprintf(stdout, "%s (by %s).\n", c.Describe(), rule)
	return 0
}
