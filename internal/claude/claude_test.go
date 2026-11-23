package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orchard-suite/hedge/internal/match"
)

const (
	home uint8 = 1 << iota
	work
)

func index(t *testing.T) (*match.Index, string) {
	t.Helper()
	b, err := match.Build(match.Spec{
		Categories: []string{"home", "work"},
		Terms:      []match.Term{{Text: "zorblax", Cats: work}, {Text: "novaline", Cats: home}},
		Zones:      []match.Zone{{Pattern: "github.com/acme-home/*", Allow: home}},
		Salt:       []byte("pepper"),
		Fixtures:   "docs/fixtures.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	x, err := match.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "index")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return x, path
}

func repoAt(t *testing.T, url string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, ".git", "config"), []byte("[remote \"origin\"]\n\turl = "+url+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func call(tool, cwd string, in map[string]any) Input {
	b, _ := json.Marshal(map[string]any{"tool_name": tool, "cwd": cwd, "tool_input": in})
	var i Input
	_ = json.Unmarshal(b, &i)
	return i
}

func TestAWriteIsCheckedInItsRepositorysZone(t *testing.T) {
	x, _ := index(t)
	tool := repoAt(t, "git@github.com:acme-tools/tool.git")
	house := repoAt(t, "https://github.com/acme-home/house")
	for _, tt := range []struct {
		name  string
		in    Input
		block bool
	}{
		{"write a term", call("Write", tool, map[string]any{"file_path": filepath.Join(tool, "a.go"), "content": "// the Zorblax API"}), true},
		{"edit a term", call("Edit", tool, map[string]any{"file_path": filepath.Join(tool, "a.go"), "new_string": "novaline"}), true},
		{"multi-edit a term", call("MultiEdit", tool, map[string]any{"file_path": filepath.Join(tool, "a.go"), "edits": []map[string]string{{"new_string": "fine"}, {"new_string": "zorblax"}}}), true},
		{"a notebook", call("NotebookEdit", tool, map[string]any{"notebook_path": filepath.Join(tool, "n.ipynb"), "new_source": "zorblax"}), true},
		{"a term in the path", call("Write", tool, map[string]any{"file_path": filepath.Join(tool, "examples", "zorblax", "a.yaml"), "content": "ok"}), true},
		{"a relative path", call("Write", tool, map[string]any{"file_path": "a.go", "content": "zorblax"}), true},
		{"a clean write", call("Write", tool, map[string]any{"file_path": filepath.Join(tool, "a.go"), "content": "// the Acme API"}), false},
		{"home names home", call("Write", house, map[string]any{"file_path": filepath.Join(house, "a.md"), "content": "novaline"}), false},
		{"home names work", call("Write", house, map[string]any{"file_path": filepath.Join(house, "a.md"), "content": "zorblax"}), true},
		{"outside every repository", call("Write", t.TempDir(), map[string]any{"file_path": filepath.Join(t.TempDir(), "notes.md"), "content": "zorblax"}), false},
		{"the ignore file", call("Write", tool, map[string]any{"file_path": filepath.Join(tool, ".hedgeignore"), "content": "hedge-0"}), true},
		{"a read", call("Read", tool, map[string]any{"file_path": filepath.Join(tool, "a.go")}), false},
	} {
		d := Decide(tt.in, x, nil)
		if d.Block != tt.block {
			t.Errorf("%s: block %v, want %v: %s", tt.name, d.Block, tt.block, d.Reason)
		}
		if d.Block && tt.name == "write a term" && (!strings.Contains(d.Reason, "Zorblax") || !strings.Contains(d.Reason, "docs/fixtures.md")) {
			t.Errorf("the reason does not say what and where to look: %s", d.Reason)
		}
	}
}

func TestWithoutAnIndexAWriteIntoARepositoryWaits(t *testing.T) {
	tool := repoAt(t, "git@github.com:acme-tools/tool.git")
	d := Decide(call("Write", tool, map[string]any{"file_path": filepath.Join(tool, "a.go"), "content": "x"}), nil, match.ErrNoIndex)
	if !d.Block || !strings.Contains(d.Reason, "hedge refresh") {
		t.Errorf("%+v", d)
	}
	if d := Decide(call("Write", t.TempDir(), map[string]any{"file_path": filepath.Join(t.TempDir(), "a"), "content": "x"}), nil, match.ErrNoIndex); d.Block {
		t.Error("a write outside every repository waited")
	}
}

func TestAShellCommandMayNotTurnTheGuardOff(t *testing.T) {
	x, _ := index(t)
	tool := repoAt(t, "git@github.com:acme-tools/tool.git")
	for cmd, block := range map[string]bool{
		`git commit --no-verify -m "x"`:                              true,
		`git commit -nm "x"`:                                         true,
		`git push --no-verify`:                                       true,
		`git -c hook.hedge-pre-commit.command=true commit -m x`:      true,
		`git config --global --unset hook.hedge-pre-push.command`:    true,
		`GIT_CONFIG_GLOBAL=/dev/null git commit -m x`:                true,
		`HOME=/tmp git commit -m x`:                                  true,
		`hedge ignore hedge-123 a.md`:                                true,
		`echo hedge-1 >> .hedgeignore`:                               true,
		`rm ~/.cache/hedge/index`:                                    true,
		`gh repo edit acme-tools/tool --visibility public`:           true,
		`gh repo create acme-tools/new --public`:                     true,
		`gh api -X PATCH repos/acme-tools/tool -f visibility=public`: true,
		`hedge publish acme-tools/tool`:                              false,
		`git commit -m "fix: the -n flag"`:                           false,
		`git commit -am "feat: add a"`:                               false,
		`cat .hedgeignore`:                                           false,
		`ls -la && go test ./...`:                                    false,
		`git push --dry-run -n`:                                      false,
	} {
		d := Decide(call("Bash", tool, map[string]any{"command": cmd}), x, nil)
		if d.Block != block {
			t.Errorf("%s: block %v, want %v: %s", cmd, d.Block, block, d.Reason)
		}
	}
}

func TestAForgePostIsCheckedInTheZoneOfItsRepository(t *testing.T) {
	x, _ := index(t)
	tool := repoAt(t, "git@github.com:acme-tools/tool.git")
	body := filepath.Join(tool, "body.md")
	if err := os.WriteFile(body, []byte("found while fixing zorblax"), 0o644); err != nil {
		t.Fatal(err)
	}
	for cmd, block := range map[string]bool{
		`gh issue create --title "x" --body "the zorblax box"`:            true,
		`gh issue create -R acme-tools/other --title "zorblax"`:           true,
		`gh pr create --body-file body.md`:                                true,
		`gh api repos/acme-tools/tool/issues -F body=@body.md`:            true,
		`gh issue create -R acme-home/house --title "the novaline box"`:   false,
		`gh issue create -R acme-home/house --title "the zorblax box"`:    true,
		`gh issue create --title "a clean title" --body "nothing to see"`: false,
		`gh issue list`: false,
		`GH_TOKEN="$(fetch-token --vault zorblax)" gh api -X PATCH repos/acme-tools/tool -f has_wiki=false`: false,
		`gh issue create --title "x" --body "$(cat notes) zorblax"`:                                         true,
	} {
		d := Decide(call("Bash", tool, map[string]any{"command": cmd}), x, nil)
		if d.Block != block {
			t.Errorf("%s: block %v, want %v: %s", cmd, d.Block, block, d.Reason)
		}
	}
}

func TestRunExitsTwoToBlock(t *testing.T) {
	_, path := index(t)
	tool := repoAt(t, "git@github.com:acme-tools/tool.git")
	in, _ := json.Marshal(map[string]any{"tool_name": "Write", "cwd": tool, "tool_input": map[string]any{"file_path": filepath.Join(tool, "a"), "content": "zorblax"}})
	var stderr bytes.Buffer
	if code := Run(bytes.NewReader(in), &stderr, path); code != 2 || !strings.Contains(stderr.String(), "hedge:") {
		t.Errorf("exit %d: %s", code, stderr.String())
	}
	in, _ = json.Marshal(map[string]any{"tool_name": "Bash", "cwd": tool, "tool_input": map[string]any{"command": "ls"}})
	if code := Run(bytes.NewReader(in), &stderr, filepath.Join(t.TempDir(), "missing")); code != 0 {
		t.Errorf("a plain command needs no index, exit %d", code)
	}
}
