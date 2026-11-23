package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orchard-suite/hedge/internal/check"
	"github.com/orchard-suite/hedge/internal/gitscan"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/register"
	"github.com/orchard-suite/hedge/internal/repo"
)

type auditFlags struct {
	history, opaque, facts bool
	zoneOf, mirrors        string
	rest                   []string
}

func parseAudit(args []string) (auditFlags, error) {
	var f auditFlags
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--history":
			f.history = true
		case "--opaque":
			f.opaque = true
		case "--facts":
			f.facts, f.opaque = true, true
		case "--zone-of", "--mirrors":
			if i+1 >= len(args) {
				return f, fmt.Errorf("%s needs a value", a)
			}
			if a == "--zone-of" {
				f.zoneOf = args[i+1]
			} else {
				f.mirrors = args[i+1]
			}
			i++
		default:
			f.rest = append(f.rest, a)
		}
	}
	return f, nil
}

func audit(ctx context.Context, args []string, stdout, stderr io.Writer, p register.Paths) int {
	f, err := parseAudit(args)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 2
	}
	x := openIndex(stderr, p)
	if x == nil {
		return 1
	}
	if f.mirrors != "" {
		return auditMirrors(ctx, f, x, stdout, stderr)
	}
	dir := "."
	if len(f.rest) > 0 {
		dir = f.rest[0]
	}
	g := gitscan.Git{Dir: dir}
	root, err := gitscan.Root(ctx, g)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", dir, "is not in a git repository")
		return 1
	}
	g.Dir = root
	key := ""
	if f.zoneOf != "" {
		key = repo.KeyOf(f.zoneOf)
	} else if r, ok := repo.Find(root); ok {
		key = r.Key
	}
	c := checkerFor(x, key)
	_ = c.LoadIgnore(root)
	fs, files, err := gitscan.Files(ctx, g, root, c, func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, name))
	})
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	commits := 0
	if f.history {
		more, n, err := history(ctx, g, c)
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		fs, commits = append(fs, more...), n
	}
	if f.facts {
		fact(stdout, key, filepath.Base(root), fs, commits)
		return 0
	}
	if len(fs) == 0 {
		what := fmt.Sprintf("%d files", files)
		if f.history {
			what += fmt.Sprintf(" and %d commits", commits)
		}
		fmt.Fprintf(stdout, "hedge: clean. %s, %s.\n", what, c.Describe())
		return 0
	}
	fmt.Fprintf(stdout, "hedge: %d findings. %s.\n", len(fs), c.Describe())
	check.Print(stdout, fs, f.opaque)
	return 1
}

// history checks every commit on every ref, and every annotated tag.
func history(ctx context.Context, g gitscan.Git, c *check.Checker) ([]check.Finding, int, error) {
	fs, n, err := gitscan.Commits(ctx, g, c, nil, "--all")
	if err != nil {
		return nil, 0, err
	}
	out, err := g.Out(ctx, "for-each-ref", "--format=%(objecttype) %(objectname)", "refs/tags")
	if err != nil {
		return fs, n, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		t, sha, ok := strings.Cut(line, " ")
		if !ok || t != "tag" {
			continue
		}
		if body, err := g.Out(ctx, "cat-file", "-p", sha); err == nil {
			for _, fd := range c.Text("message", "", "", body, 1, nil) {
				fd.Commit = sha
				fs = append(fs, fd)
			}
		}
	}
	return fs, n, nil
}

// fact prints one conform fact: the repository and what hedge found,
// by id only.
func fact(w io.Writer, key, title string, fs []check.Finding, commits int) {
	ids := map[string]bool{}
	for _, f := range fs {
		ids[f.ID] = true
	}
	var list []string
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	if list == nil {
		list = []string{}
	}
	ckey := key
	if host, rest, ok := strings.Cut(key, "/"); ok && host == "github.com" {
		ckey = "github/" + rest
	}
	b, _ := json.Marshal(map[string]any{
		"kind": "repository", "key": ckey, "title": title, "fact": "hedge",
		"value": map[string]any{"findings": len(fs), "ids": list, "commits": commits},
	})
	fmt.Fprintln(w, string(b))
}

type mirrorState struct {
	Tips     []string        `yaml:"tips"`
	Commits  int             `yaml:"commits"`
	Findings []check.Finding `yaml:"findings"`
}

// auditMirrors keeps a bare mirror of each remote and checks only what is
// new since the last run, keeping what earlier runs found: a term in
// history stays a finding until it is waived.
func auditMirrors(ctx context.Context, f auditFlags, x *match.Index, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(f.mirrors, 0o700); err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	failed := 0
	for _, url := range f.rest {
		key := repo.KeyOf(url)
		dir := filepath.Join(f.mirrors, strings.NewReplacer("/", "_", ":", "_").Replace(key)+".git")
		g := gitscan.Git{Dir: f.mirrors}
		if _, err := os.Stat(dir); err != nil {
			if _, err := g.Out(ctx, "clone", "--quiet", "--mirror", url, dir); err != nil {
				fmt.Fprintln(stderr, "hedge:", err)
				failed++
				continue
			}
		} else if _, err := (gitscan.Git{Dir: dir}).Out(ctx, "remote", "update", "--prune"); err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			failed++
			continue
		}
		g.Dir = dir
		var st mirrorState
		statePath := filepath.Join(dir, "hedge-state.yaml")
		if b, err := os.ReadFile(statePath); err == nil {
			_ = yaml.Unmarshal(b, &st)
		}
		c := checkerFor(x, key)
		if ig, err := g.Out(ctx, "show", "HEAD:"+check.IgnoreFile); err == nil {
			_ = c.ReadIgnore(bytes.NewReader(ig))
		}
		out, err := g.Out(ctx, "for-each-ref", "--format=%(objectname)")
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			failed++
			continue
		}
		tips := strings.Fields(string(out))
		revs := append([]string{}, tips...)
		for _, t := range st.Tips {
			if _, err := g.Out(ctx, "cat-file", "-e", t+"^{commit}"); err == nil {
				revs = append(revs, "^"+t)
			}
		}
		if len(tips) > 0 {
			fs, n, err := gitscan.Commits(ctx, g, c, nil, revs...)
			if err != nil {
				fmt.Fprintln(stderr, "hedge:", err)
				failed++
				continue
			}
			st.Findings = append(st.Findings, fs...)
			st.Commits += n
		}
		st.Tips = tips
		// A finding waived since an earlier run drops out.
		var kept []check.Finding
		for _, fd := range st.Findings {
			if len(c.Text(fd.Where, fd.Path, "", []byte(fd.Found), 1, nil)) > 0 || fd.Found == "" {
				kept = append(kept, fd)
			}
		}
		st.Findings = kept
		if b, err := yaml.Marshal(st); err == nil {
			_ = os.WriteFile(statePath, b, 0o600)
		}
		fact(stdout, key, filepath.Base(strings.TrimSuffix(url, ".git")), st.Findings, st.Commits)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func publish(ctx context.Context, args []string, stdout, stderr io.Writer, p register.Paths) int {
	checkOnly := false
	var rest []string
	for _, a := range args {
		if a == "--check" {
			checkOnly = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 || strings.Count(rest[0], "/") != 1 {
		fmt.Fprintln(stderr, "usage: hedge publish <owner/name> [--check]")
		return 2
	}
	name := rest[0]
	x := openIndex(stderr, p)
	if x == nil {
		return 1
	}
	key := "github.com/" + strings.ToLower(name)
	c := checkerFor(x, key)
	tmp, err := os.MkdirTemp("", "hedge-publish-")
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	mirror := filepath.Join(tmp, "repo.git")
	g := gitscan.Git{Dir: tmp}
	if _, err := g.Out(ctx, "clone", "--quiet", "--mirror", "git@github.com:"+name+".git", mirror); err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	g.Dir = mirror
	if ig, err := g.Out(ctx, "show", "HEAD:"+check.IgnoreFile); err == nil {
		_ = c.ReadIgnore(bytes.NewReader(ig))
	}
	fs, commits, err := history(ctx, g, c)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	posts, err := forge(ctx, name, c)
	if err != nil {
		fmt.Fprintln(stderr, "hedge: could not read the repository's issues, pull requests, and releases:", err)
		return 1
	}
	fs = append(fs, posts...)
	if len(fs) > 0 {
		refuse(stdout, c, name+" going public", fs)
		return 1
	}
	fmt.Fprintf(stdout, "hedge: %s is clean: %d commits, every ref and tag, and every issue, pull request, and release. %s.\n", name, commits, c.Describe())
	if checkOnly {
		return 0
	}
	cmd := exec.CommandContext(ctx, "gh", "repo", "edit", name, "--visibility", "public", "--accept-visibility-change-consequences")
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stderr, "hedge: gh repo edit:", err)
		return 1
	}
	fmt.Fprintf(stdout, "hedge: %s is public.\n", name)
	if c, err := register.LoadConfig(p.Config); err == nil && c.Publish.Contributions == "collaborators" {
		// The forge can drop a setting made in the moment a repository
		// turns public, so read it back, and set it again until it holds.
		held := false
		for try := 0; try < 5 && !held; try++ {
			if try > 0 {
				time.Sleep(time.Duration(try) * 2 * time.Second)
			}
			set := exec.CommandContext(ctx, "gh", "api", "-X", "PATCH", "repos/"+name, "-f", "pull_request_creation_policy=collaborators_only", "--silent")
			set.Stderr = stderr
			if err := set.Run(); err != nil {
				continue
			}
			got, err := exec.CommandContext(ctx, "gh", "api", "repos/"+name, "--jq", ".pull_request_creation_policy").Output()
			held = err == nil && strings.TrimSpace(string(got)) == "collaborators_only"
		}
		if !held {
			fmt.Fprintln(stderr, "hedge: could not limit pull requests to collaborators; set it at https://github.com/"+name+"/settings")
			return 1
		}
		fmt.Fprintf(stdout, "hedge: only collaborators may open pull requests. The forge sets the same for issues only on its settings page: https://github.com/%s/settings, Issues, Creation allowed by, Collaborators only.\n", name)
	}
	return 0
}

// forge checks the text on the forge that goes public with a repository:
// issues, pull requests, their comments, and releases.
func forge(ctx context.Context, name string, c *check.Checker) ([]check.Finding, error) {
	var fs []check.Finding
	for _, q := range []struct{ path, jq string }{
		{"issues?state=all&per_page=100", `.[] | "#\(.number) \(.title)\n\(.body // "")"`},
		{"issues/comments?per_page=100", `.[] | .body // ""`},
		{"pulls/comments?per_page=100", `.[] | .body // ""`},
		{"releases?per_page=100", `.[] | "\(.tag_name) \(.name // "")\n\(.body // "")"`},
	} {
		cmd := exec.CommandContext(ctx, "gh", "api", "--paginate", "repos/"+name+"/"+q.path, "--jq", q.jq)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("%s: %w %s", q.path, err, strings.TrimSpace(errb.String()))
		}
		fs = c.Text("body", strings.SplitN(q.path, "?", 2)[0], "", out, 1, fs)
	}
	return fs, nil
}
