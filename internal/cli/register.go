package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orchard-suite/hedge/internal/gitscan"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/register"
	"github.com/orchard-suite/hedge/internal/repo"
)

type state struct {
	Harvested time.Time `yaml:"harvested"`
}

func loadState(p register.Paths) state {
	var s state
	if b, err := os.ReadFile(p.State); err == nil {
		_ = yaml.Unmarshal(b, &s)
	}
	return s
}

func saveState(p register.Paths, s state) error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.State), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.State, b, 0o600)
}

// pull brings the register up to date, cloning it the first time. A pull
// that fails offline keeps the copy already here.
func pull(ctx context.Context, c *register.Config, p register.Paths, stderr io.Writer) (string, error) {
	dir := c.RegisterDir(p)
	if c.Register.Repo == "" {
		return dir, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return dir, err
		}
		if _, err := (gitscan.Git{Dir: filepath.Dir(dir)}).Out(ctx, "clone", "--quiet", c.Register.Repo, dir); err != nil {
			return dir, err
		}
		return dir, nil
	}
	if _, err := (gitscan.Git{Dir: dir}).Out(ctx, "pull", "--quiet", "--ff-only"); err != nil {
		fmt.Fprintln(stderr, "hedge: could not pull the register; using the copy on this machine:", err)
	}
	return dir, nil
}

func revision(ctx context.Context, dir string) string {
	out, err := (gitscan.Git{Dir: dir}).Out(ctx, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return "local"
	}
	return strings.TrimSpace(string(out))
}

// save commits and pushes the register's changed files, when it is a
// repository with a remote.
func save(ctx context.Context, c *register.Config, dir, message string, paths ...string) error {
	if c.Register.Repo == "" {
		return nil
	}
	g := gitscan.Git{Dir: dir}
	if _, err := g.Out(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	if out, _ := g.Out(ctx, "diff", "--cached", "--name-only"); len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	if _, err := g.Out(ctx, "commit", "--quiet", "-m", message); err != nil {
		return err
	}
	_, err := g.Out(ctx, "push", "--quiet")
	return err
}

// build compiles the register into the index, replacing it in one rename
// so a hook never reads half a file.
func build(ctx context.Context, c *register.Config, p register.Paths, dir string) (*match.Index, error) {
	r, err := register.Load(dir)
	if err != nil {
		return nil, err
	}
	spec, err := r.Spec(c, revision(ctx, dir))
	if err != nil {
		return nil, err
	}
	b, err := match.Build(spec)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Index), 0o700); err != nil {
		return nil, err
	}
	tmp := p.Index + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, p.Index); err != nil {
		return nil, err
	}
	return match.Load(b)
}

func refresh(ctx context.Context, args []string, stdout, stderr io.Writer, p register.Paths) int {
	force := len(args) > 0 && args[0] == "--harvest"
	c, err := register.LoadConfig(p.Config)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	dir, err := pull(ctx, c, p, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	st := loadState(p)
	due := force
	if every, err := time.ParseDuration(c.Harvest.Every); err == nil && c.Harvest.Every != "" && time.Since(st.Harvested) > every {
		due = true
	}
	if due && len(c.Harvest.Sources) > 0 {
		changed, err := harvest(ctx, c, dir, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
		if len(changed) > 0 {
			if err := save(ctx, c, dir, "chore: harvest "+strings.Join(changed, ", "), register.GeneratedDir); err != nil {
				fmt.Fprintln(stderr, "hedge: harvested, but could not save the register:", err)
				return 1
			}
		}
		st.Harvested = time.Now()
		if err := saveState(p, st); err != nil {
			fmt.Fprintln(stderr, "hedge:", err)
			return 1
		}
	}
	x, err := build(ctx, c, p, dir)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	fmt.Fprintf(stdout, "hedge: the index holds %d terms from register %s.\n", x.Terms(), x.Source())
	return 0
}

// harvest runs each source and saves what it found. It returns the
// sources whose terms changed.
func harvest(ctx context.Context, c *register.Config, dir string, stderr io.Writer) ([]string, error) {
	r, err := register.Load(dir)
	if err != nil {
		return nil, err
	}
	words, err := register.Dictionary(c.Harvest.Dictionary)
	if err != nil {
		return nil, fmt.Errorf("harvest.dictionary: %w", err)
	}
	allow := r.AllowSet()
	var changed []string
	for _, s := range c.Harvest.Sources {
		timeout := 2 * time.Minute
		if d, err := time.ParseDuration(s.Timeout); err == nil && s.Timeout != "" {
			timeout = d
		}
		sctx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(sctx, s.Run[0], s.Run[1:]...)
		var errb bytes.Buffer
		cmd.Stderr = &errb
		out, err := cmd.Output()
		cancel()
		if err != nil {
			// One source down keeps its last terms; the others still run.
			fmt.Fprintf(stderr, "hedge: harvest source %s failed, keeping its last terms: %v %s\n", s.Name, err, strings.TrimSpace(errb.String()))
			continue
		}
		var terms []string
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 1<<16), 1<<20)
		for sc.Scan() {
			t := strings.TrimSpace(sc.Text())
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if r.Keep(t, words, allow) {
				terms = append(terms, t)
			}
		}
		ch, err := r.WriteGenerated(register.Generated{Source: s.Name, Category: s.Category, Harvested: time.Now().UTC().Truncate(time.Second), Terms: terms})
		if err != nil {
			return changed, err
		}
		if ch {
			changed = append(changed, s.Name)
		}
	}
	return changed, nil
}

func add(ctx context.Context, args []string, stdout, stderr io.Writer, p register.Paths) int {
	exact := false
	var rest []string
	for _, a := range args {
		if a == "--exact" {
			exact = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		fmt.Fprintln(stderr, "usage: hedge add <category> <term> [--exact]")
		return 2
	}
	c, err := register.LoadConfig(p.Config)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	dir, err := pull(ctx, c, p, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	r, err := register.Load(dir)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	term := strings.Join(rest[1:], " ")
	added, err := r.Add(rest[0], term, exact)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	if added {
		// The message names the category, never the term.
		if err := save(ctx, c, dir, "feat: add a "+rest[0]+" term", register.FileName); err != nil {
			fmt.Fprintln(stderr, "hedge: added here, but could not save the register:", err)
			return 1
		}
	}
	if _, err := build(ctx, c, p, dir); err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	if added {
		fmt.Fprintf(stdout, "hedge: added to %s, and every hook on this machine knows it now.\n", rest[0])
	} else {
		fmt.Fprintf(stdout, "hedge: %s already holds it.\n", rest[0])
	}
	return 0
}

// ignore waives one finding in the repository here. Only a person at a
// terminal may: an agent's shell has none.
func ignore(args []string, stdin io.Reader, stdout, stderr io.Writer, p register.Paths) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "usage: hedge ignore <id> [path-glob]")
		return 2
	}
	if !isTerminal(stdin) {
		fmt.Fprintln(stderr, "hedge: ignore waives a finding, so a person runs it in their own terminal.")
		return 1
	}
	if !strings.HasPrefix(args[0], "hedge-") {
		fmt.Fprintln(stderr, "hedge: an id looks like hedge-3f9a1c0e22b4; a finding shows its id.")
		return 2
	}
	cwd, _ := os.Getwd()
	r, ok := repo.Find(cwd)
	if !ok {
		fmt.Fprintln(stderr, "hedge: ignore works inside a repository.")
		return 1
	}
	fmt.Fprintf(stdout, "Waive %s %s in %s? [y/N] ", args[0], strings.Join(args[1:], " "), r.Key)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Fprintln(stdout, "Not waived.")
		return 1
	}
	f, err := os.OpenFile(filepath.Join(r.Root, ".hedgeignore"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(stderr, "hedge:", err)
		return 1
	}
	defer f.Close()
	fmt.Fprintln(f, strings.Join(args, " "))
	fmt.Fprintln(stdout, "Waived. Commit .hedgeignore with the change it allows.")
	return 0
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
