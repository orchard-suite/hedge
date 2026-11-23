package match

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"syscall"
)

// The index is one file, written by Build and read in place by Open. Every
// lookup reads the mapped bytes directly, so opening it costs one mmap and
// nothing is decoded up front.
//
// Layout, little endian: a header, then the terms, the term table, the
// prefix set, the nets, the categories, the zones, and one string blob.
// The term table and the prefix set are open-addressed hash tables with
// linear probing; a hash of 0 marks an empty slot.

const (
	magic      = "HEDGEIX1"
	version    = 1
	headerSize = 112
	termSize   = 16
	slotSize   = 16
	prefixSize = 8
	netSize    = 12
	catSize    = 8
	zoneSize   = 8

	flagExact = 1

	// MaxCategories is how many categories an index can hold: a set of
	// them is one byte.
	MaxCategories = 8
	maxParts      = 32
)

// Term is one entry of a register.
type Term struct {
	// Text is the term as the register writes it, shown in a finding.
	Text string
	// Exact compares the term's case too.
	Exact bool
	// Cats is the set of categories the term belongs to.
	Cats uint8
}

// Net is an IPv4 network whose addresses belong to categories.
type Net struct {
	Addr, Mask uint32
	Cats       uint8
}

// Zone says which categories a repository may name, by its remote: an
// exact key such as "github.com/acme/app", or a prefix ending in "/*".
type Zone struct {
	Pattern string
	Allow   uint8
}

// Spec is everything an index holds.
type Spec struct {
	Categories []string
	Terms      []Term
	Nets       []Net
	Zones      []Zone
	// Default is what a repository that matches no zone may name.
	Default uint8
	// Authors are the categories a commit's author and committer may also
	// name in any zone: the people who sign their own work.
	Authors uint8
	// Salt makes a term's opaque id, so an id in a repository names no
	// term.
	Salt []byte
	// Fixtures is where an author finds names to use instead.
	Fixtures string
	// Source says which register the index was built from.
	Source string
	// Built is when, in Unix seconds.
	Built int64
}

// Build writes an index.
func Build(s Spec) ([]byte, error) {
	if len(s.Categories) > MaxCategories {
		return nil, fmt.Errorf("an index holds at most %d categories, not %d", MaxCategories, len(s.Categories))
	}
	var strs []byte
	addStr := func(v []byte) (uint32, uint16, error) {
		if len(v) > 0xffff {
			return 0, 0, fmt.Errorf("a string of %d bytes is too long for the index", len(v))
		}
		off := uint32(len(strs))
		strs = append(strs, v...)
		return off, uint16(len(v)), nil
	}

	// Terms merge by key: one key in two categories is one entry in both.
	type entry struct {
		key, text   []byte
		exact       bool
		cats        uint8
		parts, gaps int
	}
	var entries []*entry
	byKey := map[string]*entry{}
	for _, t := range s.Terms {
		key, parts, spaces := Key(t.Text, t.Exact)
		if len(key) == 0 || parts > maxParts {
			continue
		}
		id := string(key)
		if t.Exact {
			id = "\x01" + id
		}
		if e, ok := byKey[id]; ok {
			e.cats |= t.Cats
			if spaces > e.gaps {
				e.gaps = spaces
			}
			continue
		}
		e := &entry{key: key, text: []byte(t.Text), exact: t.Exact, cats: t.Cats, parts: parts, gaps: spaces}
		byKey[id] = e
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].exact != entries[j].exact {
			return !entries[i].exact
		}
		return string(entries[i].key) < string(entries[j].key)
	})

	terms := make([]byte, len(entries)*termSize)
	tableSlots := slots(len(entries))
	table := make([]byte, tableSlots*slotSize)
	prefixes := map[uint64]struct{}{}
	most := 1
	for i, e := range entries {
		ko, kl, err := addStr(e.key)
		if err != nil {
			return nil, err
		}
		do, dl, err := addStr(e.text)
		if err != nil {
			return nil, err
		}
		r := terms[i*termSize:]
		binary.LittleEndian.PutUint32(r[0:], ko)
		binary.LittleEndian.PutUint16(r[4:], kl)
		r[6] = e.cats
		if e.exact {
			r[7] = flagExact
		}
		binary.LittleEndian.PutUint32(r[8:], do)
		binary.LittleEndian.PutUint16(r[12:], dl)
		r[14] = byte(e.gaps)
		r[15] = byte(e.parts)
		if e.parts > most {
			most = e.parts
		}
		h := Hash(e.key, e.exact)
		put(table, tableSlots, h, uint32(i+1))
		seed := byte(seedLower)
		if e.exact {
			seed = seedExact
		}
		p := hashStart(seed)
		for _, c := range e.key {
			p = hashByte(p, c)
			prefixes[nonzero(p)] = struct{}{}
		}
	}
	prefixSlots := slots(len(prefixes))
	prefix := make([]byte, prefixSlots*prefixSize)
	for h := range prefixes {
		mask := uint64(prefixSlots - 1)
		for i := h & mask; ; i = (i + 1) & mask {
			at := prefix[i*prefixSize:]
			if binary.LittleEndian.Uint64(at) == 0 {
				binary.LittleEndian.PutUint64(at, h)
				break
			}
		}
	}

	nets := make([]byte, len(s.Nets)*netSize)
	for i, n := range s.Nets {
		r := nets[i*netSize:]
		binary.LittleEndian.PutUint32(r[0:], n.Addr&n.Mask)
		binary.LittleEndian.PutUint32(r[4:], n.Mask)
		r[8] = n.Cats
	}
	cats := make([]byte, len(s.Categories)*catSize)
	for i, c := range s.Categories {
		o, l, err := addStr([]byte(c))
		if err != nil {
			return nil, err
		}
		binary.LittleEndian.PutUint32(cats[i*catSize:], o)
		binary.LittleEndian.PutUint16(cats[i*catSize+4:], l)
	}
	// The most specific zone wins: exact keys first, then longer prefixes.
	zs := append([]Zone(nil), s.Zones...)
	sort.SliceStable(zs, func(i, j int) bool {
		gi, gj := isGlob(zs[i].Pattern), isGlob(zs[j].Pattern)
		if gi != gj {
			return !gi
		}
		return len(zs[i].Pattern) > len(zs[j].Pattern)
	})
	zones := make([]byte, len(zs)*zoneSize)
	for i, z := range zs {
		pat := z.Pattern
		glob := isGlob(pat)
		if glob {
			pat = pat[:len(pat)-1]
		}
		o, l, err := addStr([]byte(pat))
		if err != nil {
			return nil, err
		}
		r := zones[i*zoneSize:]
		binary.LittleEndian.PutUint32(r[0:], o)
		binary.LittleEndian.PutUint16(r[4:], l)
		r[6] = z.Allow
		if glob {
			r[7] = 1
		}
	}
	fo, fl, err := addStr([]byte(s.Fixtures))
	if err != nil {
		return nil, err
	}
	so, sl, err := addStr(s.Salt)
	if err != nil {
		return nil, err
	}
	ro, rl, err := addStr([]byte(s.Source))
	if err != nil {
		return nil, err
	}

	out := make([]byte, headerSize)
	section := func(b []byte) uint32 {
		for len(out)%8 != 0 {
			out = append(out, 0)
		}
		off := uint32(len(out))
		out = append(out, b...)
		return off
	}
	h := out[:headerSize]
	copy(h, magic)
	le := binary.LittleEndian
	le.PutUint32(h[8:], version)
	le.PutUint32(h[12:], uint32(len(entries)))
	le.PutUint32(h[16:], uint32(tableSlots))
	le.PutUint32(h[20:], uint32(prefixSlots))
	le.PutUint32(h[24:], uint32(most))
	le.PutUint32(h[28:], uint32(len(s.Nets)))
	le.PutUint32(h[32:], uint32(len(s.Categories)))
	le.PutUint32(h[36:], uint32(len(zs)))
	offs := []uint32{section(terms), section(table), section(prefix), section(nets), section(cats), section(zones), section(strs)}
	h = out[:headerSize]
	for i, o := range offs {
		le.PutUint32(h[40+4*i:], o)
	}
	le.PutUint32(h[68:], uint32(len(strs)))
	h[72] = s.Default
	h[73] = s.Authors
	le.PutUint32(h[76:], fo)
	le.PutUint32(h[80:], uint32(fl))
	le.PutUint32(h[84:], so)
	le.PutUint32(h[88:], uint32(sl))
	le.PutUint64(h[92:], uint64(s.Built))
	le.PutUint32(h[100:], ro)
	le.PutUint32(h[104:], uint32(rl))
	return out, nil
}

func isGlob(p string) bool { return len(p) >= 2 && p[len(p)-2:] == "/*" }

func slots(n int) int {
	s := 16
	for s < n*2 {
		s <<= 1
	}
	return s
}

func put(table []byte, n int, h uint64, v uint32) {
	mask := uint64(n - 1)
	for i := h & mask; ; i = (i + 1) & mask {
		at := table[i*slotSize:]
		if binary.LittleEndian.Uint64(at) == 0 {
			binary.LittleEndian.PutUint64(at, h)
			binary.LittleEndian.PutUint32(at[8:], v)
			return
		}
	}
}

// Index is an opened index.
type Index struct {
	b                                       []byte
	nterms, tableSlots, prefixSlots, most   int
	nnets, ncats, nzones                    int
	terms, table, prefix, nets, cats, zones []byte
	strs                                    []byte
}

// ErrNoIndex means there is no index to open.
var ErrNoIndex = errors.New("no hedge index")

// Open maps an index file.
func Open(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoIndex
		}
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < headerSize {
		return nil, fmt.Errorf("%s is not a hedge index", path)
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return Load(b)
}

// Load reads an index from bytes.
func Load(b []byte) (*Index, error) {
	if len(b) < headerSize || string(b[:8]) != magic {
		return nil, errors.New("not a hedge index")
	}
	le := binary.LittleEndian
	if v := le.Uint32(b[8:]); v != version {
		return nil, fmt.Errorf("hedge index version %d; this hedge reads %d: run hedge refresh", v, version)
	}
	x := &Index{b: b}
	x.nterms = int(le.Uint32(b[12:]))
	x.tableSlots = int(le.Uint32(b[16:]))
	x.prefixSlots = int(le.Uint32(b[20:]))
	x.most = int(le.Uint32(b[24:]))
	x.nnets = int(le.Uint32(b[28:]))
	x.ncats = int(le.Uint32(b[32:]))
	x.nzones = int(le.Uint32(b[36:]))
	sec := func(i, size int) ([]byte, error) {
		off := int(le.Uint32(b[40+4*i:]))
		if off > len(b) || off+size > len(b) {
			return nil, errors.New("hedge index is cut short: run hedge refresh")
		}
		return b[off : off+size], nil
	}
	var err error
	if x.terms, err = sec(0, x.nterms*termSize); err != nil {
		return nil, err
	}
	if x.table, err = sec(1, x.tableSlots*slotSize); err != nil {
		return nil, err
	}
	if x.prefix, err = sec(2, x.prefixSlots*prefixSize); err != nil {
		return nil, err
	}
	if x.nets, err = sec(3, x.nnets*netSize); err != nil {
		return nil, err
	}
	if x.cats, err = sec(4, x.ncats*catSize); err != nil {
		return nil, err
	}
	if x.zones, err = sec(5, x.nzones*zoneSize); err != nil {
		return nil, err
	}
	if x.strs, err = sec(6, int(le.Uint32(b[68:]))); err != nil {
		return nil, err
	}
	return x, nil
}

func (x *Index) str(off uint32, n int) []byte {
	return x.strs[off : int(off)+n]
}

func (x *Index) hstr(offAt, lenAt int) string {
	le := binary.LittleEndian
	return string(x.str(le.Uint32(x.b[offAt:]), int(le.Uint32(x.b[lenAt:]))))
}

// Terms is how many terms the index holds.
func (x *Index) Terms() int { return x.nterms }

// Default is what a repository in no zone may name.
func (x *Index) Default() uint8 { return x.b[72] }

// Authors are the categories an author or committer may also name.
func (x *Index) Authors() uint8 { return x.b[73] }

// Fixtures is where an author finds names to use instead.
func (x *Index) Fixtures() string { return x.hstr(76, 80) }

// Source says which register the index was built from.
func (x *Index) Source() string { return x.hstr(100, 104) }

// Built is when the index was built, in Unix seconds.
func (x *Index) Built() int64 { return int64(binary.LittleEndian.Uint64(x.b[92:])) }

func (x *Index) salt() []byte { return []byte(x.hstr(84, 88)) }

// Text is a term as its register writes it.
func (x *Index) Text(term int) string {
	r := x.terms[term*termSize:]
	le := binary.LittleEndian
	return string(x.str(le.Uint32(r[8:]), int(le.Uint16(r[12:]))))
}

// ID is a term's opaque id: it says which term without saying the term.
func (x *Index) ID(term int) string {
	r := x.terms[term*termSize:]
	key := x.str(binary.LittleEndian.Uint32(r[0:]), int(binary.LittleEndian.Uint16(r[4:])))
	h := sha256.New()
	h.Write(x.salt())
	h.Write([]byte{r[7]})
	h.Write(key)
	return "hedge-" + hex.EncodeToString(h.Sum(nil)[:6])
}

// NetID is a network's opaque id.
func (x *Index) NetID(net int) string {
	h := sha256.New()
	h.Write(x.salt())
	h.Write([]byte("net"))
	h.Write(x.nets[net*netSize : net*netSize+8])
	return "hedge-" + hex.EncodeToString(h.Sum(nil)[:6])
}

// Categories names a set of categories.
func (x *Index) Categories(set uint8) []string {
	var out []string
	le := binary.LittleEndian
	for i := 0; i < x.ncats; i++ {
		if set&(1<<i) != 0 {
			r := x.cats[i*catSize:]
			out = append(out, string(x.str(le.Uint32(r), int(le.Uint16(r[4:])))))
		}
	}
	return out
}

// Category is the set holding the named category, and whether there is one.
func (x *Index) Category(name string) (uint8, bool) {
	for i := 0; i < x.ncats; i++ {
		r := x.cats[i*catSize:]
		le := binary.LittleEndian
		if string(x.str(le.Uint32(r), int(le.Uint16(r[4:])))) == name {
			return 1 << i, true
		}
	}
	return 0, false
}

// Allow is what a repository with this remote key may name, such as
// "github.com/acme/app", and the zone pattern that said so ("" for the
// default).
func (x *Index) Allow(key string) (uint8, string) {
	le := binary.LittleEndian
	for i := 0; i < x.nzones; i++ {
		r := x.zones[i*zoneSize:]
		pat := string(x.str(le.Uint32(r), int(le.Uint16(r[4:]))))
		if r[7] == 1 {
			if len(key) >= len(pat) && key[:len(pat)] == pat {
				return r[6], pat + "*"
			}
		} else if key == pat {
			return r[6], pat
		}
	}
	return x.Default(), ""
}

func (x *Index) lookup(h uint64) int {
	mask := uint64(x.tableSlots - 1)
	le := binary.LittleEndian
	for i := h & mask; ; i = (i + 1) & mask {
		at := x.table[i*slotSize:]
		v := le.Uint64(at)
		if v == h {
			return int(le.Uint32(at[8:])) - 1
		}
		if v == 0 {
			return -1
		}
	}
}

func (x *Index) isPrefix(h uint64) bool {
	mask := uint64(x.prefixSlots - 1)
	le := binary.LittleEndian
	for i := h & mask; ; i = (i + 1) & mask {
		v := le.Uint64(x.prefix[i*prefixSize:])
		if v == h {
			return true
		}
		if v == 0 {
			return false
		}
	}
}
