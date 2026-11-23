// Package gitscan reads what git is about to record or send: the staged
// change, a commit message, the commits a push sends, or a whole history,
// and checks each added line, path, message, and identity.
package gitscan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/orchard-suite/hedge/internal/check"
)

// Git runs git in a folder.
type Git struct{ Dir string }

// Out runs git and returns its output.
func (g Git) Out(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// stream runs git and hands its output to read as it comes.
func (g Git) stream(ctx context.Context, stdin io.Reader, read func(*bufio.Reader) error, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	rerr := read(bufio.NewReaderSize(out, 1<<16))
	if rerr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return rerr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

var diffArgs = []string{"-U0", "--no-color", "--no-ext-diff", "--no-renames", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"}

// Staged checks the change staged for the next commit.
func Staged(ctx context.Context, g Git, c *check.Checker) ([]check.Finding, error) {
	var fs []check.Finding
	args := append([]string{"diff", "--cached", "--diff-filter=ACMRT"}, diffArgs...)
	err := g.stream(ctx, nil, func(r *bufio.Reader) error {
		d := &diffReader{c: c}
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				d.line(line)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		fs = d.out
		return nil
	}, args...)
	return fs, err
}

// Message checks a commit message, leaving out git's comment lines.
func Message(text []byte, c *check.Checker) []check.Finding {
	var keep [][]byte
	for _, l := range bytes.Split(text, []byte{'\n'}) {
		if bytes.HasPrefix(l, []byte("# ------------------------ >8")) {
			break
		}
		if bytes.HasPrefix(l, []byte("#")) {
			l = nil
		}
		keep = append(keep, l)
	}
	return c.Text("message", "", "", bytes.Join(keep, []byte{'\n'}), 1, nil)
}

// Identity checks who a commit is by, such as "Ada <ada@example.com>".
func Identity(who string, c *check.Checker) []check.Finding {
	return c.Text("author", "", "", []byte(who), 1, nil)
}

const (
	commitMark = '\x1e'
	headEnd    = '\x1f'
)

// Commits checks every commit that rev-list arguments name, read from
// stdin when stdin is not nil: each one's author, committer, message, and
// added lines. A merge is checked against its first parent.
func Commits(ctx context.Context, g Git, c *check.Checker, stdin io.Reader, revs ...string) ([]check.Finding, int, error) {
	args := []string{"log", "-p", "--diff-merges=first-parent", "--format=" + string(commitMark) + "%H%n%an <%ae>%n%cn <%ce>%n%B" + string(headEnd)}
	args = append(args, diffArgs...)
	if stdin != nil {
		args = append(args, "--stdin")
	}
	args = append(args, revs...)
	args = append(args, "--")
	var fs []check.Finding
	n := 0
	err := g.stream(ctx, stdin, func(r *bufio.Reader) error {
		var d *diffReader
		var head []byte
		inHead := false
		flush := func() {
			if d != nil {
				fs = append(fs, d.out...)
			}
		}
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				switch {
				case line[0] == commitMark:
					flush()
					n++
					d = &diffReader{c: c}
					head = append(head[:0], line[1:]...)
					inHead = true
					if i := bytes.IndexByte(head, headEnd); i >= 0 {
						d.header(head[:i])
						inHead = false
					}
				case inHead:
					head = append(head, line...)
					if i := bytes.IndexByte(head, headEnd); i >= 0 {
						d.header(head[:i])
						inHead = false
					}
				case d != nil:
					d.line(line)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
		}
		flush()
		return nil
	}, args...)
	return fs, n, err
}

// diffReader reads one unified diff with no context lines.
type diffReader struct {
	c      *check.Checker
	commit string
	path   string
	next   int
	out    []check.Finding
	skip   bool
}

func (d *diffReader) header(h []byte) {
	lines := strings.SplitN(string(h), "\n", 4)
	for len(lines) < 4 {
		lines = append(lines, "")
	}
	d.commit = strings.TrimSpace(lines[0])
	d.out = append(d.out, stamp(Identity(lines[1], d.c), d.commit)...)
	if lines[2] != lines[1] {
		d.out = append(d.out, stamp(Identity(lines[2], d.c), d.commit)...)
	}
	d.out = append(d.out, stamp(d.c.Text("message", "", "", []byte(lines[3]), 1, nil), d.commit)...)
}

func stamp(fs []check.Finding, commit string) []check.Finding {
	for i := range fs {
		fs[i].Commit = commit
	}
	return fs
}

func (d *diffReader) line(l []byte) {
	switch {
	case bytes.HasPrefix(l, []byte("diff --git ")):
		d.path, d.skip = "", false
	case bytes.HasPrefix(l, []byte("+++ ")):
		p := strings.TrimRight(string(l[4:]), "\n")
		if p == "/dev/null" {
			d.skip = true
			return
		}
		p = unquote(p)
		d.path = strings.TrimPrefix(p, "b/")
		d.out = append(d.out, stamp(d.c.Text("path", d.path, "", []byte(d.path), 1, nil), d.commit)...)
	case bytes.HasPrefix(l, []byte("@@ ")):
		// @@ -a,b +c,d @@
		if i := bytes.Index(l, []byte(" +")); i >= 0 {
			rest := l[i+2:]
			j := 0
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			d.next, _ = strconv.Atoi(string(rest[:j]))
		}
	case bytes.HasPrefix(l, []byte("Binary files ")):
		// Binary content is not read.
	case len(l) > 0 && l[0] == '+' && !d.skip && d.path != "":
		text := bytes.TrimRight(l[1:], "\n")
		d.out = append(d.out, stamp(d.c.Text("file", d.path, "", text, d.next, nil), d.commit)...)
		d.next++
	}
}

// unquote reads a path git quoted because it has unusual bytes.
func unquote(p string) string {
	if len(p) < 2 || p[0] != '"' {
		return p
	}
	if u, err := strconv.Unquote(p); err == nil {
		return u
	}
	return p
}

// ErrNotARepository means a folder is outside git.
var ErrNotARepository = errors.New("not in a git repository")

// Root is the top of the working tree git is in.
func Root(ctx context.Context, g Git) (string, error) {
	out, err := g.Out(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotARepository
	}
	return strings.TrimSpace(string(out)), nil
}

// Files checks every tracked file in the working tree, and its path.
func Files(ctx context.Context, g Git, root string, c *check.Checker, read func(string) ([]byte, error)) ([]check.Finding, int, error) {
	out, err := g.Out(ctx, "ls-files", "-z")
	if err != nil {
		return nil, 0, err
	}
	var fs []check.Finding
	n := 0
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		name := string(p)
		fs = c.Text("path", name, "", p, 1, fs)
		b, err := read(name)
		if err != nil || isBinary(b) {
			continue
		}
		n++
		fs = c.Text("file", name, "", b, 1, fs)
	}
	return fs, n, nil
}

func isBinary(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	return bytes.IndexByte(head, 0) >= 0
}
