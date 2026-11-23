// Package claude is hedge's PreToolUse hook for Claude Code. It reads the
// tool call on stdin and blocks a write that would put a term into a
// repository whose zone does not allow it, a shell command that would
// turn the guard off, a repository made public around hedge publish, and
// a forge post (an issue, a pull request, a release) that names a term
// its repository may not.
package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchard-suite/hedge/internal/check"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/repo"
)

// Input is the part of a hook call hedge reads.
type Input struct {
	Cwd       string    `json:"cwd"`
	ToolName  string    `json:"tool_name"`
	ToolInput toolInput `json:"tool_input"`
}

type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Content      string `json:"content"`
	NewString    string `json:"new_string"`
	NewSource    string `json:"new_source"`
	Edits        []struct {
		NewString string `json:"new_string"`
	} `json:"edits"`
	Command string `json:"command"`
}

// Decision is what the hook says: allow, or block with a reason the agent
// reads.
type Decision struct {
	Block  bool
	Reason string
}

// Decide reads one hook call. An index of nil means hedge has none: every
// write into a repository is blocked until hedge refresh runs, because a
// guard that cannot see must not wave things through.
func Decide(in Input, x *match.Index, indexErr error) Decision {
	switch in.ToolName {
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return write(in, x, indexErr)
	case "Bash":
		return bash(in, x, indexErr)
	}
	return Decision{}
}

func write(in Input, x *match.Index, indexErr error) Decision {
	p := in.ToolInput.FilePath
	if p == "" {
		p = in.ToolInput.NotebookPath
	}
	if p == "" {
		return Decision{}
	}
	if !filepath.IsAbs(p) && in.Cwd != "" {
		p = filepath.Join(in.Cwd, p)
	}
	r, ok := repo.Find(p)
	if !ok || r.Key == "" {
		// Outside every repository, or in one with no remote: nothing
		// leaves it but by a push, which is checked where it goes.
		return Decision{}
	}
	rel, _ := filepath.Rel(r.Root, p)
	rel = filepath.ToSlash(rel)
	if rel == check.IgnoreFile {
		return Decision{Block: true, Reason: "hedge: " + check.IgnoreFile + " waives findings, and only a person edits it. Ask the person to run `hedge ignore` in their own terminal."}
	}
	if x == nil {
		return noIndex(indexErr)
	}
	c := zoneChecker(x, r)
	_ = c.LoadIgnore(r.Root)
	var fs []check.Finding
	fs = c.Text("path", rel, "", []byte(rel), 1, fs)
	add := func(s string) {
		if s != "" {
			fs = c.Text("file", rel, "", []byte(s), 1, fs)
		}
	}
	add(in.ToolInput.Content)
	add(in.ToolInput.NewString)
	add(in.ToolInput.NewSource)
	for _, e := range in.ToolInput.Edits {
		add(e.NewString)
	}
	if len(fs) == 0 {
		return Decision{}
	}
	return Decision{Block: true, Reason: reason(c, fs, "This write")}
}

func zoneChecker(x *match.Index, r repo.Repo) *check.Checker {
	allow, rule := x.Allow(r.Key)
	return check.New(x, check.Zone{Key: r.Key, Allow: allow, Rule: rule})
}

func noIndex(err error) Decision {
	msg := "hedge has no index on this machine, so it cannot check this"
	if err != nil && err != match.ErrNoIndex {
		msg = "hedge cannot read its index (" + err.Error() + ")"
	}
	return Decision{Block: true, Reason: "hedge: " + msg + ". Run `hedge refresh`, then try again."}
}

func reason(c *check.Checker, fs []check.Finding, what string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "hedge: %s puts estate content where it may not go. %s.\n", what, c.Describe())
	for i, f := range fs {
		if i == 10 {
			fmt.Fprintf(&b, "...and %d more.\n", len(fs)-10)
			break
		}
		fmt.Fprintf(&b, "- %s: %q names %s (%s)\n", f.Place(), f.Found, strings.Join(f.Cats, " and "), f.Term)
	}
	b.WriteString("Replace each with a made-up name, or leave it out. Never copy names or data from a real system into this repository.")
	if fx := c.X.Fixtures(); fx != "" {
		b.WriteString(" Names to use instead: " + fx)
	}
	return b.String()
}

// Run is the hook: it reads stdin and exits 2 to block, with the reason on
// stderr for the agent.
func Run(stdin io.Reader, stderr io.Writer, indexPath string) int {
	var in Input
	b, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "hedge: cannot read the hook call:", err)
		return 2
	}
	if err := json.Unmarshal(b, &in); err != nil {
		fmt.Fprintln(stderr, "hedge: cannot read the hook call:", err)
		return 2
	}
	if !needsIndex(in) {
		return 0
	}
	x, xerr := match.Open(indexPath)
	d := Decide(in, x, xerr)
	if d.Block {
		fmt.Fprintln(stderr, d.Reason)
		return 2
	}
	return 0
}

// needsIndex is the fast way out: most shell commands touch nothing hedge
// guards.
func needsIndex(in Input) bool {
	if in.ToolName != "Bash" {
		return true
	}
	cmd := in.ToolInput.Command
	return strings.Contains(cmd, "git") || strings.Contains(cmd, "gh ") || strings.Contains(cmd, "hedge") || strings.Contains(cmd, "GIT_")
}

func cwdRepo(in Input) (repo.Repo, bool) {
	dir := in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return repo.Find(dir)
}
