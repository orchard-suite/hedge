package register

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orchard-suite/hedge/internal/match"
)

func TestAHarvestKeepsNamesAndDropsWordsNumbersAndTheAllowList(t *testing.T) {
	r := &Register{File: File{Allow: []string{"Orbit Tool", "k8s"}}}
	words := map[string]bool{"web": true, "api": true, "billing": true, "service": true, "worker": true}
	allow := r.AllowSet()
	for term, keep := range map[string]bool{
		"zorblax-api":     true,
		"Billing Service": false,
		"web":             false,
		"api":             false,
		"803":             false,
		"abc":             false,
		"orbit-tool":      false,
		"Globex Prime":    true,
		"dc03":            false,
		"k8s-worker-1":    false,
		"zkimble":         true,
		"Services":        false,
		"WebServices":     false,
		"Billings":        false,
	} {
		if got := r.Keep(term, words, allow); got != keep {
			t.Errorf("Keep(%q) = %v, want %v", term, got, keep)
		}
	}
}

func TestARegisterCompilesToAnIndexAndAddsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, []string{"home", "work"}); err != nil {
		t.Fatal(err)
	}
	r, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Add("work", "Zorblax", false); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := r.Add("work", "zorblax", false); ok {
		t.Error("the same key was added twice")
	}
	if _, err := r.Add("nope", "x", false); err == nil {
		t.Error("an unknown category was accepted")
	}
	b, _ := os.ReadFile(filepath.Join(dir, FileName))
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(strings.Replace(string(b), "home: {}", "home:\n        nets: [198.51.100.0/24]", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteGenerated(Generated{Source: "hub", Category: "home", Terms: []string{"novaline", "novaline", " "}}); err != nil {
		t.Fatal(err)
	}
	r, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &Config{}
	c.Zones.Rules = append(c.Zones.Rules, struct {
		Match string   `yaml:"match"`
		Allow []string `yaml:"allow"`
	}{"github.com/Acme-Home/*", []string{"home"}})
	spec, err := r.Spec(c, "test")
	if err != nil {
		t.Fatal(err)
	}
	ib, err := match.Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	x, _ := match.Load(ib)
	if x.Terms() != 2 {
		t.Errorf("%d terms", x.Terms())
	}
	allow, _ := x.Allow("github.com/acme-home/house")
	hits := match.NewScanner(x).Scan([]byte("zorblax novaline 198.51.100.9"), allow, nil)
	if len(hits) != 1 || x.Text(hits[0].Term) != "Zorblax" {
		t.Errorf("hits %+v", hits)
	}
	c.Zones.Default = []string{"nope"}
	if _, err := r.Spec(c, "test"); err == nil {
		t.Error("an unknown category in a zone was accepted")
	}
}
