package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/orchard-suite/hedge/internal/check"
	"github.com/orchard-suite/hedge/internal/gitscan"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/register"
	"github.com/orchard-suite/hedge/internal/repo"
)

const zeroSHA = "0000000000000000000000000000000000000000"

func gitHook(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer, p register.Paths) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hedge git pre-commit | commit-msg <file> | pre-push <remote> <url>")
		return 2
	}
	x := openIndex(stderr, p)
	if x == nil {
		fmt.Fprintln(stderr, "hedge: a commit or a push waits until hedge can check it.")
		return 1
	}
	cwd, _ := os.Getwd()
	g := gitscan.Git{Dir: cwd}
	root, err := gitscan.Root(ctx, g)
	if err != nil {
		root = cwd
	}
	r, _ := repo.Find(root)
	if r.Key == "" && args[0] != "pre-push" {
		// A repository with no remote sends nothing anywhere; what it
		// pushes is checked by the push, in the zone of where it goes.
		return 0
	}
	switch args[0] {
	case "pre-commit":
		c := checkerFor(x, r.Key)
		_ = c.LoadIgnore(root)
		fs, err := gitscan.Staged(ctx, gitscan.Git{Dir: root}, c)
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		if ident, err := g.Out(ctx, "var", "GIT_AUTHOR_IDENT"); err == nil {
			fs = append(fs, gitscan.Identity(identity(string(ident)), c)...)
		}
		if len(fs) > 0 {
			refuse(stderr, c, "This commit", fs)
			return 1
		}
		return 0
	case "commit-msg":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: hedge git commit-msg <file>")
			return 2
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		c := checkerFor(x, r.Key)
		_ = c.LoadIgnore(root)
		if fs := gitscan.Message(b, c); len(fs) > 0 {
			refuse(stderr, c, "This commit message", fs)
			return 1
		}
		return 0
	case "pre-push":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: hedge git pre-push <remote> <url>")
			return 2
		}
		return prePush(ctx, gitscan.Git{Dir: root}, root, args[1], args[len(args)-1], stdin, stderr, x)
	}
	fmt.Fprintf(stderr, "hedge: no git hook %q\n", args[0])
	return 2
}

// identity drops the date git var puts after the address.
func identity(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ">"); i >= 0 {
		return s[:i+1]
	}
	return s
}

// prePush checks what a push sends, in the zone of where it goes: every
// commit the remote does not have yet, and every annotated tag.
func prePush(ctx context.Context, g gitscan.Git, root, remote, url string, stdin io.Reader, stderr io.Writer, x *match.Index) int {
	// A push is judged by where it goes: the remote's own address and the
	// address git pushes to, and the stricter of the two when they differ.
	key := repo.KeyOf(url)
	c := checkerFor(x, key)
	if named, err := g.Out(ctx, "config", "--get", "remote."+remote+".url"); err == nil {
		if k := repo.KeyOf(strings.TrimSpace(string(named))); k != "" && k != key {
			allow, rule := x.Allow(k)
			if allow&c.Zone.Allow == c.Zone.Allow {
				key = k
				c.Zone.Key, c.Zone.Rule = k, rule
			}
			c.Zone.Allow &= allow
		}
	}
	_ = c.LoadIgnore(root)
	var pos, neg []string
	var fs []check.Finding
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[1] == zeroSHA {
			continue // a delete sends nothing
		}
		local, remoteSHA := f[1], f[3]
		if t, err := g.Out(ctx, "cat-file", "-t", local); err == nil && strings.TrimSpace(string(t)) == "tag" {
			if body, err := g.Out(ctx, "cat-file", "-p", local); err == nil {
				for _, fd := range c.Text("message", "", "", body, 1, nil) {
					fd.Commit = local
					fs = append(fs, fd)
				}
			}
		}
		pos = append(pos, local)
		if remoteSHA != zeroSHA {
			if _, err := g.Out(ctx, "cat-file", "-e", remoteSHA+"^{commit}"); err == nil {
				neg = append(neg, "^"+remoteSHA)
			}
		}
	}
	if len(pos) == 0 {
		return 0
	}
	revs := append(pos, neg...)
	if _, err := g.Out(ctx, "config", "--get", "remote."+remote+".url"); err == nil {
		revs = append(revs, "--not", "--remotes="+remote)
	}
	more, _, err := gitscan.Commits(ctx, g, c, nil, revs...)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	fs = append(fs, more...)
	if len(fs) > 0 {
		refuse(stderr, c, "This push to "+key, fs)
		return 1
	}
	return 0
}
