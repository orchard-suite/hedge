package match

import (
	"bytes"
	"encoding/binary"
)

// Hit is one term or address found in a text.
type Hit struct {
	// Start and End are the byte span in the text.
	Start, End int
	// Term is the term's number in the index, or -1 for an address.
	Term int
	// Net is the network's number for an address, or -1.
	Net int
	// Cats is the set of categories the hit belongs to that were not
	// allowed. It is 0 for an allowed hit, which a scanner reports only
	// when KeepAllowed is set.
	Cats uint8
	// All is every category the hit belongs to.
	All uint8
}

// Scanner holds the buffers one scan reuses. A Scanner is not safe for
// use by two goroutines at once.
type Scanner struct {
	// KeepAllowed reports allowed terms too, so a caller can tell a term
	// that sits inside an allowed one, such as a surname inside a name
	// that may appear.
	KeepAllowed bool
	x           *Index
	parts       []part
	low         []byte
	exact       []byte
}

// NewScanner makes a scanner over an index.
func NewScanner(x *Index) *Scanner {
	return &Scanner{x: x, parts: make([]part, 0, 256), low: make([]byte, 0, 64), exact: make([]byte, 0, 64)}
}

// Scan appends to hits every term in text whose categories are not all in
// allow, and returns it.
func (s *Scanner) Scan(text []byte, allow uint8, hits []Hit) []Hit {
	x := s.x
	if x.nterms > 0 {
		s.parts = cut(text, s.parts)
		ps := s.parts
		most := x.most
		for i := range ps {
			hl, he := hashStart(seedLower), hashStart(seedExact)
			lowAlive, exactAlive := true, true
			s.low, s.exact = s.low[:0], s.exact[:0]
			crossings := 0
			for j := i; j < len(ps) && j-i < most; j++ {
				p := ps[j]
				if j > i {
					if p.broken {
						break
					}
					if p.spaced {
						crossings++
					}
				}
				for k := p.start; k < p.end; k++ {
					c := text[k]
					if lowAlive {
						lc := lower(c)
						hl = hashByte(hl, lc)
						s.low = append(s.low, lc)
					}
					if exactAlive {
						he = hashByte(he, c)
						s.exact = append(s.exact, c)
					}
				}
				if lowAlive {
					h := nonzero(hl)
					if x.isPrefix(h) {
						if t := x.lookup(h); t >= 0 {
							hits = s.accept(hits, t, false, s.low, crossings, allow, ps[i].start, p.end)
						}
					} else {
						lowAlive = false
					}
				}
				if exactAlive {
					h := nonzero(he)
					if x.isPrefix(h) {
						if t := x.lookup(h); t >= 0 {
							hits = s.accept(hits, t, true, s.exact, crossings, allow, ps[i].start, p.end)
						}
					} else {
						exactAlive = false
					}
				}
				if !lowAlive && !exactAlive {
					break
				}
			}
		}
	}
	if x.nnets > 0 {
		hits = s.addresses(text, allow, hits)
	}
	return hits
}

func (s *Scanner) accept(hits []Hit, t int, exact bool, key []byte, crossings int, allow uint8, start, end int) []Hit {
	r := s.x.terms[t*termSize:]
	if (r[7]&flagExact != 0) != exact || int(r[14]) < crossings {
		return hits
	}
	cats := r[6]
	if cats&^allow == 0 && !s.KeepAllowed {
		return hits
	}
	le := binary.LittleEndian
	if !bytes.Equal(s.x.str(le.Uint32(r[0:]), int(le.Uint16(r[4:]))), key) {
		return hits
	}
	return append(hits, Hit{Start: start, End: end, Term: t, Net: -1, Cats: cats &^ allow, All: cats})
}

// addresses finds dotted IPv4 addresses that fall in a network of the index.
func (s *Scanner) addresses(text []byte, allow uint8, hits []Hit) []Hit {
	n := len(text)
	for i := 0; i < n; i++ {
		c := text[i]
		if c < '0' || c > '9' || i > 0 && (isDigit(text[i-1]) || text[i-1] == '.') {
			continue
		}
		var addr uint32
		j, octets := i, 0
		ok := true
		for octets < 4 {
			v, k := 0, j
			for k < n && isDigit(text[k]) && k-j < 3 {
				v = v*10 + int(text[k]-'0')
				k++
			}
			if k == j || v > 255 || k < n && isDigit(text[k]) {
				ok = false
				break
			}
			addr = addr<<8 | uint32(v)
			octets++
			j = k
			if octets < 4 {
				if j >= n || text[j] != '.' {
					ok = false
					break
				}
				j++
			}
		}
		if !ok || j < n && text[j] == '.' && j+1 < n && isDigit(text[j+1]) {
			continue
		}
		le := binary.LittleEndian
		for k := 0; k < s.x.nnets; k++ {
			r := s.x.nets[k*netSize:]
			if addr&le.Uint32(r[4:]) == le.Uint32(r[0:]) && r[8]&^allow != 0 {
				hits = append(hits, Hit{Start: i, End: j, Term: -1, Net: k, Cats: r[8] &^ allow, All: r[8]})
				break
			}
		}
		i = j - 1
	}
	return hits
}
