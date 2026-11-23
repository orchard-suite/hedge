// Package repo finds the repository a path is in and names it by its
// remote, such as "github.com/acme/app". It reads git's own files and runs
// no git, because a hook asks on every write.
package repo

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Repo is a repository a path is in.
type Repo struct {
	// Root is the top of the working tree.
	Root string
	// Key names the repository by its remote, "" when it has none.
	Key string
}

// Find walks up from path to the repository that holds it. The path need
// not exist yet. ok is false outside any repository.
func Find(path string) (r Repo, ok bool) {
	dir, err := filepath.Abs(path)
	if err != nil {
		return Repo{}, false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		dotgit := filepath.Join(dir, ".git")
		if st, err := os.Stat(dotgit); err == nil {
			common := dotgit
			if !st.IsDir() {
				common = gitdirOf(dotgit, dir)
			}
			return Repo{Root: dir, Key: KeyOf(remoteURL(filepath.Join(common, "config")))}, true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return Repo{}, false
		}
		dir = up
	}
}

// gitdirOf follows a worktree's .git file to the repository's own folder.
func gitdirOf(dotgit, dir string) string {
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return dotgit
	}
	line := strings.TrimSpace(string(b))
	gd, found := strings.CutPrefix(line, "gitdir:")
	if !found {
		return dotgit
	}
	gd = strings.TrimSpace(gd)
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(dir, gd)
	}
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		cd := strings.TrimSpace(string(c))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gd, cd)
		}
		return filepath.Clean(cd)
	}
	return gd
}

// remoteURL reads origin's url from a git config file, or the first
// remote's when there is no origin.
func remoteURL(config string) string {
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	var first, origin, section string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			section = strings.ToLower(string(line))
			continue
		}
		if !strings.HasPrefix(section, "[remote ") {
			continue
		}
		k, v, ok := strings.Cut(string(line), "=")
		if !ok || strings.TrimSpace(strings.ToLower(k)) != "url" {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if section == `[remote "origin"]` {
			origin = v
		}
		if first == "" {
			first = v
		}
	}
	if origin != "" {
		return origin
	}
	return first
}

// KeyOf names a remote URL as host/path, lower case, without a scheme, a
// user, a port, or ".git": git@github.com:Acme/App.git and
// https://github.com/acme/app are both "github.com/acme/app". A folder is
// "file:" and its path, as in "file:/srv/mirror/app".
func KeyOf(url string) string {
	u := strings.TrimSpace(url)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if at := strings.LastIndex(strings.SplitN(u, "/", 2)[0], "@"); at >= 0 {
			u = u[at+1:]
		}
		host, rest, _ := strings.Cut(u, "/")
		if c := strings.LastIndex(host, ":"); c >= 0 {
			host = host[:c]
		}
		u = host + "/" + rest
	} else if at := strings.Index(u, "@"); at >= 0 && at < strings.Index(u+":", ":") {
		u = strings.Replace(u[at+1:], ":", "/", 1)
	} else if strings.HasPrefix(u, "/") || strings.HasPrefix(u, ".") {
		u = "file:" + u
	} else if c := strings.Index(u, ":"); c > 0 && !strings.Contains(u[:c], "/") {
		u = u[:c] + "/" + u[c+1:]
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	return strings.ToLower(u)
}
