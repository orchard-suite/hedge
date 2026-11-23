package register

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/orchard-suite/hedge/internal/match"
)

// The register is a folder:
//
//	register.yaml        the salt, the hand-kept terms and networks per
//	                     category, and the words never to flag
//	generated/<name>.yaml the terms one harvest source found
const (
	FileName     = "register.yaml"
	GeneratedDir = "generated"
)

// File is register.yaml.
type File struct {
	Salt       string               `yaml:"salt"`
	Categories map[string]*Category `yaml:"categories"`
	// Allow lists words a harvest may never add, such as the names of
	// the estate-neutral tools themselves.
	Allow []string `yaml:"allow,omitempty"`
}

// Category is one category's hand-kept entries.
type Category struct {
	Terms []TermSpec `yaml:"terms,omitempty"`
	Nets  []string   `yaml:"nets,omitempty"`
}

// TermSpec is a term: a plain string, or {term, case: exact}.
type TermSpec struct {
	Term  string
	Exact bool
}

// UnmarshalYAML reads either form.
func (t *TermSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		t.Term = n.Value
		return nil
	}
	var v struct {
		Term string `yaml:"term"`
		Case string `yaml:"case"`
	}
	if err := n.Decode(&v); err != nil {
		return err
	}
	if v.Case != "" && v.Case != "exact" && v.Case != "any" {
		return fmt.Errorf("line %d: case is exact or any, not %q", n.Line, v.Case)
	}
	t.Term, t.Exact = v.Term, v.Case == "exact"
	return nil
}

// MarshalYAML writes the short form when it can.
func (t TermSpec) MarshalYAML() (any, error) {
	if !t.Exact {
		return t.Term, nil
	}
	return map[string]string{"term": t.Term, "case": "exact"}, nil
}

// Generated is one harvest source's terms.
type Generated struct {
	Source    string    `yaml:"source"`
	Category  string    `yaml:"category"`
	Harvested time.Time `yaml:"harvested"`
	Terms     []string  `yaml:"terms"`
}

// Register is a loaded register folder.
type Register struct {
	Dir       string
	File      File
	Generated []Generated
}

// Load reads a register folder.
func Load(dir string) (*Register, error) {
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	r := &Register{Dir: dir}
	if err := yaml.Unmarshal(b, &r.File); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if len(r.File.Categories) == 0 {
		return nil, fmt.Errorf("%s has no categories", FileName)
	}
	if len(r.File.Categories) > match.MaxCategories {
		return nil, fmt.Errorf("%s has %d categories; hedge holds at most %d", FileName, len(r.File.Categories), match.MaxCategories)
	}
	gens, _ := filepath.Glob(filepath.Join(dir, GeneratedDir, "*.yaml"))
	sort.Strings(gens)
	for _, g := range gens {
		gb, err := os.ReadFile(g)
		if err != nil {
			return nil, err
		}
		var gen Generated
		if err := yaml.Unmarshal(gb, &gen); err != nil {
			return nil, fmt.Errorf("%s: %w", g, err)
		}
		if _, ok := r.File.Categories[gen.Category]; !ok {
			return nil, fmt.Errorf("%s: category %q is not in %s", g, gen.Category, FileName)
		}
		r.Generated = append(r.Generated, gen)
	}
	return r, nil
}

// Init writes a new register with the named categories and a fresh salt.
func Init(dir string, categories []string) error {
	if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
		return fmt.Errorf("%s already has a register", dir)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	f := File{Salt: hex.EncodeToString(salt), Categories: map[string]*Category{}}
	for _, c := range categories {
		f.Categories[c] = &Category{}
	}
	if err := os.MkdirAll(filepath.Join(dir, GeneratedDir), 0o755); err != nil {
		return err
	}
	return writeYAML(filepath.Join(dir, FileName), f)
}

// Names are the register's categories in the order of their bits.
func (r *Register) Names() []string {
	var names []string
	for n := range r.File.Categories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (r *Register) bits() map[string]uint8 {
	m := map[string]uint8{}
	for i, n := range r.Names() {
		m[n] = 1 << i
	}
	return m
}

func setOf(bits map[string]uint8, names []string, where string) (uint8, error) {
	var s uint8
	for _, n := range names {
		b, ok := bits[n]
		if !ok {
			return 0, fmt.Errorf("%s: no category %q in the register", where, n)
		}
		s |= b
	}
	return s, nil
}

// Spec compiles the register and an estate's zones into an index spec.
func (r *Register) Spec(c *Config, source string) (match.Spec, error) {
	bits := r.bits()
	s := match.Spec{Categories: r.Names(), Fixtures: c.Fixtures, Source: source, Built: time.Now().Unix()}
	salt, err := hex.DecodeString(r.File.Salt)
	if err != nil || len(salt) < 8 {
		return s, fmt.Errorf("%s: salt must be at least 8 bytes of hex", FileName)
	}
	s.Salt = salt
	for _, name := range r.Names() {
		cat := r.File.Categories[name]
		if cat == nil {
			continue
		}
		for _, t := range cat.Terms {
			if strings.TrimSpace(t.Term) == "" {
				continue
			}
			s.Terms = append(s.Terms, match.Term{Text: t.Term, Exact: t.Exact, Cats: bits[name]})
		}
		for _, n := range cat.Nets {
			p, err := netip.ParsePrefix(n)
			if err != nil || !p.Addr().Is4() {
				return s, fmt.Errorf("%s: %s: %q is not an IPv4 network", FileName, name, n)
			}
			a := p.Masked().Addr().As4()
			mask := uint32(0)
			if p.Bits() > 0 {
				mask = ^uint32(0) << (32 - p.Bits())
			}
			s.Nets = append(s.Nets, match.Net{Addr: uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3]), Mask: mask, Cats: bits[name]})
		}
	}
	for _, g := range r.Generated {
		for _, t := range g.Terms {
			s.Terms = append(s.Terms, match.Term{Text: t, Cats: bits[g.Category]})
		}
	}
	if s.Default, err = setOf(bits, c.Zones.Default, "zones.default"); err != nil {
		return s, err
	}
	if s.Authors, err = setOf(bits, c.Zones.Authors, "zones.authors"); err != nil {
		return s, err
	}
	for _, z := range c.Zones.Rules {
		allow, err := setOf(bits, z.Allow, "zone "+z.Match)
		if err != nil {
			return s, err
		}
		s.Zones = append(s.Zones, match.Zone{Pattern: strings.ToLower(z.Match), Allow: allow})
	}
	return s, nil
}

// Add puts one hand-kept term into a category. It reports false when the
// term was already there.
func (r *Register) Add(category, term string, exact bool) (bool, error) {
	cat, ok := r.File.Categories[category]
	if !ok {
		return false, fmt.Errorf("no category %q: the register has %s", category, strings.Join(r.Names(), ", "))
	}
	if cat == nil {
		cat = &Category{}
		r.File.Categories[category] = cat
	}
	key, _, _ := match.Key(term, exact)
	if len(key) == 0 {
		return false, errors.New("a term needs a letter or a digit")
	}
	for _, t := range cat.Terms {
		k, _, _ := match.Key(t.Term, t.Exact)
		if t.Exact == exact && string(k) == string(key) {
			return false, nil
		}
	}
	cat.Terms = append(cat.Terms, TermSpec{Term: term, Exact: exact})
	return true, writeYAML(filepath.Join(r.Dir, FileName), r.File)
}

func writeYAML(path string, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Dictionary reads a word list into a set of lower-case keys.
func Dictionary(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(expand(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	words := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			words[strings.ToLower(w)] = true
		}
	}
	return words, sc.Err()
}

// Keep says whether a harvested term is worth guarding: long enough, not
// a number, not on the register's allow list, and not made only of common
// words. A word is common when it is in the dictionary, is a word on the
// allow list, is a number, or is a short code such as "dc03" or "vm2".
func (r *Register) Keep(term string, words map[string]bool, allow map[string]bool) bool {
	key, _, _ := match.Key(term, false)
	if len(key) < 4 || allow[string(key)] {
		return false
	}
	digits := true
	for _, c := range key {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		return false
	}
	// A term of one plain word, or of plain words only, is dropped: it
	// would flag ordinary prose.
	plain := true
	for _, w := range match.Words(term) {
		if !common(w, words, allow) {
			plain = false
			break
		}
	}
	return !plain
}

// common is a dictionary or allow-list word, its plural, one followed by
// digits ("file0"), or a short code.
func common(w string, words, allow map[string]bool) bool {
	if words[w] || allow[w] || shortCode(w) {
		return true
	}
	for _, suffix := range []string{"s", "es", "ies"} {
		if stem, ok := strings.CutSuffix(w, suffix); ok && stem != "" {
			if suffix == "ies" {
				stem += "y"
			}
			if words[stem] || allow[stem] {
				return true
			}
		}
	}
	stem := strings.TrimRight(w, "0123456789")
	return stem != w && stem != "" && (words[stem] || allow[stem])
}

// shortCode is a number, or up to three letters then digits.
func shortCode(w string) bool {
	i := 0
	for i < len(w) && w[i] >= 'a' && w[i] <= 'z' {
		i++
	}
	if i > 3 || i == len(w) && i > 0 {
		return false
	}
	for _, c := range w[i:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// AllowSet is the register's allow list as keys.
func (r *Register) AllowSet() map[string]bool {
	m := map[string]bool{}
	for _, a := range r.File.Allow {
		k, _, _ := match.Key(a, false)
		m[string(k)] = true
	}
	return m
}

// WriteGenerated saves one source's terms, sorted and unique. It reports
// whether the terms changed.
func (r *Register) WriteGenerated(g Generated) (bool, error) {
	seen := map[string]bool{}
	var terms []string
	for _, t := range g.Terms {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			terms = append(terms, t)
		}
	}
	sort.Strings(terms)
	g.Terms = terms
	path := filepath.Join(r.Dir, GeneratedDir, g.Source+".yaml")
	if old, err := os.ReadFile(path); err == nil {
		var prev Generated
		if yaml.Unmarshal(old, &prev) == nil && prev.Category == g.Category && strings.Join(prev.Terms, "\n") == strings.Join(terms, "\n") {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, writeYAML(path, g)
}
