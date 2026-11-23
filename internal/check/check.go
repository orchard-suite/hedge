// Package check turns hits in a text into findings with a place: a file
// and line, a path, a commit message, or an author. It applies a
// repository's .hedgeignore, and prints findings for a person, for an
// agent, or without the terms.
package check

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/orchard-suite/hedge/internal/match"
)

// IgnoreFile is the file at a repository's root that waives findings.
// Each line is an opaque id and an optional path pattern, so the file
// names no term:
//
//	hedge-3f9a1c0e22b4 docs/history/**
const IgnoreFile = ".hedgeignore"

// Finding is one term or address in a place it may not be.
type Finding struct {
	// Where is what was read: "file", "path", "message", "author", or
	// "body".
	Where string
	// Path is the file, for a file or a path.
	Path string
	// Line is the 1-based line, for a file or a message.
	Line int
	// Commit is the commit it was found in, when read from history.
	Commit string
	// Found is the text as it appears.
	Found string
	// Term is the register's own text for it.
	Term string
	// ID names it without naming it.
	ID string
	// Cats are the categories it belongs to that the zone does not allow.
	Cats []string
}

// Zone is what one repository may name.
type Zone struct {
	// Key is the repository's remote key, "" for none.
	Key   string
	Allow uint8
	// Rule is the zone rule that matched, "" for the default.
	Rule string
}

// Checker finds terms for one zone.
type Checker struct {
	X      *match.Index
	Zone   Zone
	ignore []ignoreRule
	s      *match.Scanner
	hits   []match.Hit
}

type ignoreRule struct{ id, glob string }

// New makes a checker.
func New(x *match.Index, z Zone) *Checker {
	s := match.NewScanner(x)
	s.KeepAllowed = true
	return &Checker{X: x, Zone: z, s: s}
}

// Skipped says a file's content is not read: a lock file holds only
// versions and hashes, and its hashes read as words by chance. Its path is
// still checked.
func Skipped(file string) bool {
	switch path.Base(file) {
	case "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock",
		"go.sum", "Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "Gemfile.lock",
		"composer.lock", "Package.resolved", "flake.lock", "packages.lock.json":
		return true
	}
	return false
}

// credit says a file is where a project names who made it, so the zone's
// author categories may appear in it as they may in a commit's author.
func credit(file string) bool {
	base := path.Base(file)
	for _, p := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "AUTHORS", "CONTRIBUTORS"} {
		if strings.HasPrefix(strings.ToUpper(base), p) {
			return true
		}
	}
	switch base {
	case "package.json", "pyproject.toml", "setup.cfg", "Cargo.toml", "plugin.json", "marketplace.json", "composer.json":
		return true
	}
	return false
}

// LoadIgnore reads a repository's .hedgeignore, if it has one.
func (c *Checker) LoadIgnore(root string) error {
	f, err := os.Open(filepath.Join(root, IgnoreFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return c.ReadIgnore(f)
}

// ReadIgnore reads .hedgeignore rules.
func (c *Checker) ReadIgnore(r io.Reader) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		rule := ignoreRule{id: f[0]}
		if len(f) > 1 {
			rule.glob = f[1]
		}
		c.ignore = append(c.ignore, rule)
	}
	return sc.Err()
}

func (c *Checker) ignored(id, p string) bool {
	for _, r := range c.ignore {
		if r.id != id {
			continue
		}
		if r.glob == "" {
			return true
		}
		if g, ok := strings.CutSuffix(r.glob, "/**"); ok {
			if p == g || strings.HasPrefix(p, g+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(r.glob, p); ok {
			return true
		}
	}
	return false
}

// Text finds terms in text, a place of the given kind. Lines count from
// firstLine.
func (c *Checker) Text(where, file, commit string, text []byte, firstLine int, out []Finding) []Finding {
	if where == "file" && Skipped(file) {
		return out
	}
	allow := c.Zone.Allow
	credits := where == "author" || where == "file" && credit(file)
	if credits {
		allow |= c.X.Authors()
	}
	c.hits = c.s.Scan(text, allow, c.hits[:0])
	if len(c.hits) == 0 {
		return out
	}
	if credits {
		// A person's own name is theirs to sign with, though another
		// category, such as a directory of people, holds it too.
		for i := range c.hits {
			if c.hits[i].All&c.X.Authors() != 0 {
				c.hits[i].Cats = 0
			}
		}
	}
	line, at := firstLine, 0
	lastEnd := -1
	for _, h := range c.hits {
		if h.Cats == 0 || h.Start < lastEnd || c.inside(h) {
			// Allowed, or a term within one already found or within an
			// allowed one: a surname inside a name that may appear.
			continue
		}
		var id, term string
		if h.Term >= 0 {
			id, term = c.X.ID(h.Term), c.X.Text(h.Term)
		} else {
			id, term = c.X.NetID(h.Net), "a network"
		}
		if c.ignored(id, file) {
			continue
		}
		line += bytes.Count(text[at:h.Start], []byte{'\n'})
		at = h.Start
		lastEnd = h.End
		out = append(out, Finding{
			Where: where, Path: file, Line: line, Commit: commit,
			Found: string(text[h.Start:h.End]), Term: term, ID: id,
			Cats: c.X.Categories(h.Cats),
		})
	}
	return out
}

func (c *Checker) inside(h match.Hit) bool {
	for _, a := range c.hits {
		if a.Cats == 0 && a.Start <= h.Start && h.End <= a.End && (a.End-a.Start) > (h.End-h.Start) {
			return true
		}
	}
	return false
}

// Describe says what a zone may name, for a person.
func (c *Checker) Describe() string {
	name := c.Zone.Key
	if name == "" {
		name = "this repository"
	}
	allowed := c.X.Categories(c.Zone.Allow)
	if len(allowed) == 0 {
		return name + " is estate-neutral: it names no estate"
	}
	return name + " may name " + strings.Join(allowed, " and ") + " only"
}

// Place says where a finding is.
func (f Finding) Place() string {
	var b strings.Builder
	if f.Commit != "" {
		short := f.Commit
		if len(short) > 12 {
			short = short[:12]
		}
		b.WriteString("commit " + short + " ")
	}
	switch f.Where {
	case "file":
		fmt.Fprintf(&b, "%s:%d", f.Path, f.Line)
	case "path":
		b.WriteString("path " + f.Path)
	case "message":
		fmt.Fprintf(&b, "message line %d", f.Line)
	default:
		b.WriteString(f.Where)
	}
	return b.String()
}

// Print writes findings for a person or an agent: each with the text and
// the register's term. opaque leaves both out and shows only the id.
func Print(w io.Writer, fs []Finding, opaque bool) {
	for _, f := range fs {
		if opaque {
			fmt.Fprintf(w, "%s: %s (%s)\n", f.Place(), f.ID, strings.Join(f.Cats, ", "))
			continue
		}
		fmt.Fprintf(w, "%s: %q names %s (%s, %s)\n", f.Place(), f.Found, strings.Join(f.Cats, " and "), f.Term, f.ID)
	}
}
