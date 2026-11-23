// Package register reads an estate's hedge config and its register of
// terms, harvests terms from the estate's own systems, and compiles both
// into the index the hooks read.
package register

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is an estate's rules for hedge. It lives outside every
// repository, in the estate's own toolkit.
type Config struct {
	Register struct {
		// Repo is a git remote holding the register; it is cloned to
		// Path, or to hedge's data folder when Path is empty.
		Repo string `yaml:"repo"`
		Path string `yaml:"path"`
	} `yaml:"register"`
	// Fixtures is where an author finds names to use instead, shown with
	// every finding.
	Fixtures string `yaml:"fixtures"`
	Zones    struct {
		// Default is what a repository in no rule may name: none, unless
		// the estate says otherwise.
		Default []string `yaml:"default"`
		// Authors are the categories a commit's author and committer may
		// name anywhere, such as the category that holds the owner's own
		// name.
		Authors []string `yaml:"authors"`
		Rules   []struct {
			Match string   `yaml:"match"`
			Allow []string `yaml:"allow"`
		} `yaml:"rules"`
	} `yaml:"zones"`
	// Publish is what hedge publish sets on a repository it makes public.
	Publish struct {
		// Contributions is "collaborators" to let only collaborators open
		// pull requests and issues, or empty to leave the forge's default.
		Contributions string `yaml:"contributions"`
	} `yaml:"publish"`
	Harvest struct {
		// Every is how old the last harvest may be before refresh
		// harvests again, such as 24h. Empty never harvests on its own.
		Every string `yaml:"every"`
		// Dictionary is a word list; a harvested term that is a plain
		// word is dropped.
		Dictionary string   `yaml:"dictionary"`
		Sources    []Source `yaml:"sources"`
	} `yaml:"harvest"`
}

// Source is a program that prints terms of one category, one per line.
type Source struct {
	Name     string   `yaml:"name"`
	Category string   `yaml:"category"`
	Run      []string `yaml:"run"`
	// Timeout bounds the program, such as 2m.
	Timeout string `yaml:"timeout"`
}

// Paths are where hedge keeps its files on a machine.
type Paths struct {
	Config, Data, Index, State string
}

// DefaultPaths honours HEDGE_CONFIG, HEDGE_DATA, and HEDGE_INDEX, then the
// XDG folders.
func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	get := func(env, xdg, fallback string, rest ...string) string {
		if v := os.Getenv(env); v != "" {
			return v
		}
		base := os.Getenv(xdg)
		if base == "" {
			base = filepath.Join(home, fallback)
		}
		return filepath.Join(append([]string{base}, rest...)...)
	}
	p := Paths{
		Config: get("HEDGE_CONFIG", "XDG_CONFIG_HOME", ".config", "hedge", "config.yaml"),
		Data:   get("HEDGE_DATA", "XDG_DATA_HOME", filepath.Join(".local", "share"), "hedge"),
		Index:  get("HEDGE_INDEX", "XDG_CACHE_HOME", ".cache", "hedge", "index"),
	}
	p.State = filepath.Join(filepath.Dir(p.Index), "state.yaml")
	return p
}

// ErrNoConfig means the machine has no hedge config.
var ErrNoConfig = errors.New("no hedge config")

// LoadConfig reads a config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoConfig, path)
		}
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Register.Repo == "" && c.Register.Path == "" {
		return nil, fmt.Errorf("%s names no register: set register.repo or register.path", path)
	}
	for _, s := range c.Harvest.Sources {
		if s.Name == "" || s.Category == "" || len(s.Run) == 0 {
			return nil, fmt.Errorf("%s: every harvest source needs a name, a category, and a program to run", path)
		}
	}
	if c.Harvest.Every != "" {
		if _, err := time.ParseDuration(c.Harvest.Every); err != nil {
			return nil, fmt.Errorf("%s: harvest.every: %w", path, err)
		}
	}
	return &c, nil
}

// RegisterDir is where the register's files are on this machine.
func (c *Config) RegisterDir(p Paths) string {
	if c.Register.Path != "" {
		return expand(c.Register.Path)
	}
	return filepath.Join(p.Data, "register")
}

func expand(path string) string {
	if len(path) > 1 && path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
