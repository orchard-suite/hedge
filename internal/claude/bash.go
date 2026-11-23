package claude

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/orchard-suite/hedge/internal/check"
	"github.com/orchard-suite/hedge/internal/match"
	"github.com/orchard-suite/hedge/internal/repo"
)

// bash guards a shell command. A file a command writes is checked when it
// is committed, so a command is read only for what a commit cannot catch:
// turning the guard off, making a repository public, and posting to a
// forge.
func bash(in Input, x *match.Index, indexErr error) Decision {
	cmd := in.ToolInput.Command
	for _, seg := range segments(cmd) {
		if d := guardOff(seg); d.Block {
			return d
		}
		if d := publish(seg); d.Block {
			return d
		}
	}
	for _, seg := range segments(cmd) {
		if d := forgePost(in, seg, x, indexErr); d.Block {
			return d
		}
	}
	return Decision{}
}

func block(why string) Decision {
	return Decision{Block: true, Reason: "hedge: " + why}
}

// guardOff blocks the ways a command could step around the hooks.
func guardOff(w []string) Decision {
	j := strings.Join(w, " ")
	git := indexOf(w, "git")
	switch {
	case git >= 0 && contains(w, "--no-verify"):
		return block("--no-verify skips the checks that keep estate content out of a repository. Fix what the check found instead.")
	case git >= 0 && after(w, git, "commit") && shortFlag(w, 'n'):
		return block("git commit -n skips the checks that keep estate content out of a repository. Fix what the check found instead.")
	case strings.Contains(j, "hook.hedge"):
		return block("hedge's git hooks are set by `hedge install`, and only a person changes them.")
	case strings.Contains(j, "GIT_CONFIG_GLOBAL") || git >= 0 && assigns(w, "HOME"):
		return block("a command may not swap git's global settings, which hold hedge's hooks.")
	case len(w) >= 2 && w[0] == "hedge" && w[1] == "ignore":
		return block("`hedge ignore` waives a finding, and only a person runs it, in their own terminal.")
	case strings.Contains(j, check.IgnoreFile) && writes(w):
		return block(check.IgnoreFile + " waives findings, and only a person edits it.")
	case (strings.Contains(j, ".cache/hedge") || strings.Contains(j, ".config/hedge") || strings.Contains(j, "share/hedge")) && writes(w):
		return block("hedge's own files change only through hedge refresh and hedge add.")
	}
	return Decision{}
}

// publish keeps every repository from going public around hedge publish,
// which checks its whole history first.
func publish(w []string) Decision {
	gh := indexOf(w, "gh")
	if gh < 0 {
		return Decision{}
	}
	j := strings.Join(w[gh:], " ")
	switch {
	case after(w, gh, "repo") && after(w, gh, "edit") && strings.Contains(j, "--visibility") && strings.Contains(j, "public"):
		return block("make a repository public with `hedge publish <owner/name>`: it checks the whole history first.")
	case after(w, gh, "repo") && after(w, gh, "create") && contains(w, "--public"):
		return block("create the repository private, push, then make it public with `hedge publish <owner/name>`.")
	case after(w, gh, "api") && strings.Contains(j, "visibility") && strings.Contains(j, "public"),
		after(w, gh, "api") && strings.Contains(j, "private=false"):
		return block("make a repository public with `hedge publish <owner/name>`: it checks the whole history first.")
	}
	return Decision{}
}

// forgePost checks the text of an issue, pull request, release, or API
// write against the zone of the repository it goes to.
func forgePost(in Input, w []string, x *match.Index, indexErr error) Decision {
	gh := indexOf(w, "gh")
	if gh < 0 || gh+1 >= len(w) {
		return Decision{}
	}
	sub := w[gh+1]
	posts := false
	switch sub {
	case "issue", "pr", "release", "gist", "discussion":
		for _, v := range []string{"create", "comment", "edit", "new", "review", "upload"} {
			if after(w, gh, v) {
				posts = true
			}
		}
	case "api":
		posts = true
	}
	if !posts {
		return Decision{}
	}
	key := ""
	for i := gh; i < len(w); i++ {
		v := w[i]
		switch {
		case (v == "-R" || v == "--repo") && i+1 < len(w):
			key = "github.com/" + strings.ToLower(w[i+1])
		case strings.HasPrefix(v, "--repo="):
			key = "github.com/" + strings.ToLower(strings.TrimPrefix(v, "--repo="))
		case sub == "api" && strings.HasPrefix(strings.TrimPrefix(v, "/"), "repos/"):
			parts := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(v, "/"), "repos/"), "/", 3)
			if len(parts) >= 2 {
				key = "github.com/" + strings.ToLower(parts[0]+"/"+parts[1])
			}
		}
	}
	if key == "" {
		if r, ok := cwdRepo(in); ok {
			key = r.Key
		}
	}
	if key == "" {
		return Decision{}
	}
	if x == nil {
		return noIndex(indexErr)
	}
	c := zoneChecker(x, repo.Repo{Key: key})
	var fs []check.Finding
	fs = c.Text("body", "", "", []byte(strings.Join(w, " ")), 1, fs)
	for i := gh; i < len(w); i++ {
		v := w[i]
		file := ""
		switch {
		case (v == "--body-file" || v == "--notes-file" || v == "--input" || v == "-F" && sub != "api") && i+1 < len(w):
			file = w[i+1]
		case strings.HasPrefix(v, "--body-file="):
			file = strings.TrimPrefix(v, "--body-file=")
		case sub == "api" && (v == "-F" || v == "--field") && i+1 < len(w):
			if _, f, ok := strings.Cut(w[i+1], "=@"); ok {
				file = f
			}
		}
		if file == "" || file == "-" {
			continue
		}
		if !filepath.IsAbs(file) && in.Cwd != "" {
			file = filepath.Join(in.Cwd, file)
		}
		if b, err := os.ReadFile(file); err == nil {
			fs = c.Text("body", filepath.Base(file), "", b, 1, fs)
		}
	}
	if len(fs) == 0 {
		return Decision{}
	}
	return Decision{Block: true, Reason: reason(c, fs, "This post to "+key)}
}

// segments cuts a command line into simple commands, each a list of
// words, following shell quotes well enough to find flags and file names.
func segments(cmd string) [][]string {
	var segs [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	var quote rune
	end := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endSeg := func() {
		end()
		if len(words) > 0 {
			segs = append(segs, words)
			words = nil
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else if r == '\\' && quote == '"' && i+1 < len(rs) {
				i++
				cur.WriteRune(rs[i])
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				cur.WriteRune(rs[i])
				inWord = true
			}
		case r == ';' || r == '&' || r == '|' || r == '\n' || r == '(' || r == ')' || r == '`':
			endSeg()
		case r == ' ' || r == '\t':
			end()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	endSeg()
	// A word that is an assignment or a wrapper (env, sudo, command) comes
	// before the program; keep it, the checks look past it.
	return segs
}

func indexOf(w []string, v string) int {
	for i, s := range w {
		if s == v || strings.HasSuffix(s, "/"+v) {
			return i
		}
	}
	return -1
}

func contains(w []string, v string) bool {
	for _, s := range w {
		if s == v {
			return true
		}
	}
	return false
}

func after(w []string, i int, v string) bool {
	for _, s := range w[i+1:] {
		if s == v {
			return true
		}
	}
	return false
}

func shortFlag(w []string, f byte) bool {
	for _, s := range w {
		if len(s) >= 2 && s[0] == '-' && s[1] != '-' && strings.IndexByte(s[1:], f) >= 0 && strings.IndexByte(s, '=') < 0 {
			return true
		}
	}
	return false
}

func assigns(w []string, name string) bool {
	for _, s := range w {
		if strings.HasPrefix(s, name+"=") {
			return true
		}
	}
	return false
}

func writes(w []string) bool {
	for _, s := range w {
		switch {
		case strings.HasPrefix(s, ">"), strings.Contains(s, ">"):
			return true
		case s == "tee", s == "mv", s == "cp", s == "rm", s == "ln", s == "truncate", s == "chmod":
			return true
		case s == "-i" || strings.HasPrefix(s, "-i") && len(s) <= 4:
			return true
		}
	}
	return false
}
