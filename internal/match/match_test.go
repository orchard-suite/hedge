package match

import (
	"strings"
	"testing"
)

const (
	home uint8 = 1 << iota
	work
)

func testIndex(t *testing.T) *Index {
	t.Helper()
	b, err := Build(Spec{
		Categories: []string{"home", "work"},
		Terms: []Term{
			{Text: "zorblax", Cats: work},
			{Text: "Acme Widget", Cats: work},
			{Text: "novaline", Cats: home},
			{Text: "QX", Exact: true, Cats: work},
			{Text: "s9", Cats: work},
			{Text: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", Cats: home},
			{Text: "pat.person@example.org", Cats: home},
			{Text: "Shared Name", Cats: home},
			{Text: "shared-name", Cats: work},
		},
		Nets: []Net{{Addr: 198<<24 | 51<<16 | 100<<8, Mask: 0xffffff00, Cats: home}},
		Zones: []Zone{
			{Pattern: "github.com/acme-home/*", Allow: home},
			{Pattern: "github.com/acme-home/public-tool", Allow: 0},
			{Pattern: "git.example.com/*", Allow: work},
		},
		Salt:     []byte("pepper"),
		Fixtures: "docs/fixtures.md",
		Source:   "test",
		Built:    1700000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	x, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func found(x *Index, text string, allow uint8) []string {
	var out []string
	for _, h := range NewScanner(x).Scan([]byte(text), allow, nil) {
		out = append(out, text[h.Start:h.End])
	}
	return out
}

func TestATermIsFoundAsWholeParts(t *testing.T) {
	x := testIndex(t)
	for text, want := range map[string]string{
		"the Zorblax app":     "Zorblax",
		"MyZorblaxApi":        "Zorblax",
		"zorblax-api":         "zorblax",
		"zorblax_api":         "zorblax",
		"path/zorblax/x.go":   "zorblax",
		"apps/s9/bff":         "s9",
		"S9Portal markup.pdf": "S9",
		"QXApi.WebApi":        "QX",
	} {
		got := found(x, text, 0)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q: found %q, want %q", text, got, want)
		}
	}
	for _, text := range []string{"zorblaxes", "azorblax", "vs9", "qx is lower case", "Qx", "zorb.Lax(x)", "zorb_lax"} {
		if got := found(x, text, 0); len(got) != 0 {
			t.Errorf("%q: found %q, want nothing", text, got)
		}
	}
}

func TestAnEncodedBlobHoldsNoWords(t *testing.T) {
	x := testIndex(t)
	for _, text := range []string{
		"AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50u/K1SZFPTOtr/KBHBeksoQXGw==;",
		"sha512-9pQXk2LmZr0bYwTq+fW8vNcR3sQXhJ4kLp7aB1dE6gHi==",
	} {
		if got := found(x, text, 0); len(got) != 0 {
			t.Errorf("%q: found %q in a blob", text, got)
		}
	}
	for _, text := range []string{
		"/srv/apps/v2/releases/2026/QXApi/zorblax/main.go",
		"id 0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0 here",
	} {
		if got := found(x, text, 0); len(got) == 0 {
			t.Errorf("%q: a path or an id read as a blob", text)
		}
	}
}

func TestAPhraseSpansSpacesButAWordDoesNot(t *testing.T) {
	x := testIndex(t)
	for _, text := range []string{"Acme Widget", "acme-widget", "AcmeWidget", "acme   widget"} {
		if got := found(x, text, 0); len(got) != 1 {
			t.Errorf("%q: found %q, want the phrase", text, got)
		}
	}
	if got := found(x, "acme\n\n\n\nwidget", 0); len(got) != 0 {
		t.Errorf("a gap wider than %d bytes still matched: %q", maxGap, got)
	}
	// A term with no gap of its own spans camel case only; a phrase spans
	// punctuation too.
	for text, n := range map[string]int{"nova line": 0, "nova-line": 0, "nova.Line": 0, "NovaLine": 1, "acme-widget": 1} {
		if got := found(x, text, 0); len(got) != n {
			t.Errorf("%q: found %q, want %d", text, got, n)
		}
	}
}

func TestIdentifiersMatchWithOrWithoutTheirDashes(t *testing.T) {
	x := testIndex(t)
	for _, text := range []string{
		"tenant: 0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0",
		"id=0F1E2D3C4B5A69788796A5B4C3D2E1F0",
		"mail pat.person@example.org today",
	} {
		if got := found(x, text, 0); len(got) != 1 {
			t.Errorf("%q: found %q", text, got)
		}
	}
}

func TestAllowedCategoriesAreNotHits(t *testing.T) {
	x := testIndex(t)
	if got := found(x, "zorblax and novaline", work); len(got) != 1 || got[0] != "novaline" {
		t.Errorf("work allowed: found %q", got)
	}
	if got := found(x, "zorblax and novaline", home|work); len(got) != 0 {
		t.Errorf("all allowed: found %q", got)
	}
	// One key in two categories is one term in both.
	hits := NewScanner(x).Scan([]byte("shared name"), home, nil)
	if len(hits) != 1 || hits[0].Cats != work {
		t.Errorf("a shared key: %+v", hits)
	}
}

func TestAddressesInANetworkAreHits(t *testing.T) {
	x := testIndex(t)
	for text, n := range map[string]int{
		"host 198.51.100.7:9290":  1,
		"198.51.100.0/24":         1,
		"198.51.101.7":            0,
		"version 1.198.51.100.7":  0,
		"198.51.100.7.5":          0,
		"1198.51.100.7":           0,
		"addr 198.51.100.300 bad": 0,
	} {
		if got := found(x, text, 0); len(got) != n {
			t.Errorf("%q: found %q, want %d", text, got, n)
		}
	}
	if got := found(x, "198.51.100.7", home); len(got) != 0 {
		t.Errorf("an allowed network was a hit: %q", got)
	}
}

func TestAnIDNamesNoTerm(t *testing.T) {
	x := testIndex(t)
	hits := NewScanner(x).Scan([]byte("zorblax"), 0, nil)
	if len(hits) != 1 {
		t.Fatal(hits)
	}
	id := x.ID(hits[0].Term)
	if !strings.HasPrefix(id, "hedge-") || strings.Contains(id, "zorblax") || id != x.ID(hits[0].Term) {
		t.Errorf("id %q", id)
	}
	if x.Text(hits[0].Term) != "zorblax" {
		t.Errorf("text %q", x.Text(hits[0].Term))
	}
}

func TestZonesPreferTheMostSpecificRule(t *testing.T) {
	x := testIndex(t)
	for key, want := range map[string]uint8{
		"github.com/acme-home/app":         home,
		"github.com/acme-home/public-tool": 0,
		"git.example.com/group/sub/app":    work,
		"github.com/someone/else":          0,
	} {
		if got, _ := x.Allow(key); got != want {
			t.Errorf("%s: allow %b, want %b", key, got, want)
		}
	}
	if got := x.Categories(home | work); strings.Join(got, ",") != "home,work" {
		t.Errorf("categories %q", got)
	}
}

func BenchmarkScan(b *testing.B) {
	var terms []Term
	for i := 0; i < 20000; i++ {
		terms = append(terms, Term{Text: "term" + strings.Repeat("x", i%7) + itoa(i), Cats: work})
	}
	buf, _ := Build(Spec{Categories: []string{"home", "work"}, Terms: terms, Salt: []byte("s")})
	x, _ := Load(buf)
	text := []byte(strings.Repeat("func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) { // serve the page\n", 500))
	s := NewScanner(x)
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Scan(text, 0, nil)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}
