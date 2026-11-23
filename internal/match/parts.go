// Package match finds a register's terms in text. It runs inside every
// hook, so it works without regular expressions and without allocating
// per word: text is cut into parts, and each run of parts is looked up by
// its hash in the compiled index.
//
// A part is a run of letters and digits, cut again where camel case
// starts a new word: "LiveDocsAPI" is live, docs, api, "V2Docs" is v2,
// docs, and "s6" stays one part. A term matches a run of whole parts, so
// it never matches inside a longer word ("acme" is not found in "acmes"),
// but it is found inside a compound ("AcmeWidget", "acme-widget",
// "acme_widget").
//
// Parts join across punctuation freely. They join across whitespace only
// for a term that itself has a space, so "nova line" never reads as
// "novaline".
package match

// part is one word piece of a text: text[start:end].
type part struct {
	start, end int
	// spaced is true when whitespace separates this part from the one
	// before it.
	spaced bool
	// broken is true when the gap before this part is too wide for a term
	// to span it.
	broken bool
}

// maxGap is the widest gap, in bytes, a term may span between two parts.
const maxGap = 3

// maxSplit is the longest word cut at camel case. A longer run of letters
// and digits is a hash or an encoded blob, and its case means nothing.
const maxSplit = 32

func isWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func lower(c byte) byte {
	if isUpper(c) {
		return c + 'a' - 'A'
	}
	return c
}

// A blob is an encoded value: a key, a token, a hash in base64. Its
// letters are random, so it holds no words; cut skips it whole. A blob is
// a run of base64 characters at least blobLength long, with digits, whose
// case flips often. A path, an identifier, or a hex id flips rarely.
const blobLength = 24

func isBlobChar(c byte) bool {
	return isWord(c) && c < 0x80 || c == '+' || c == '/' || c == '=' || c == '-' || c == '_'
}

// blobEnd is the end of the blob that starts at i, or -1.
func blobEnd(text []byte, i int) int {
	j, digits, flips := i, 0, 0
	for j < len(text) && isBlobChar(text[j]) {
		c := text[j]
		if isDigit(c) {
			digits++
		}
		if j > i && (isUpper(c) && isLower(text[j-1]) || isLower(c) && isUpper(text[j-1])) {
			flips++
		}
		j++
	}
	if n := j - i; n >= blobLength && digits >= 2 && flips*5 >= n {
		return j
	}
	return -1
}

// cut appends the parts of text to ps and returns it, so a caller can reuse
// one slice across many texts.
func cut(text []byte, ps []part) []part {
	ps = ps[:0]
	n := len(text)
	prevEnd := -1
	i := 0
	for i < n {
		if (i == 0 || !isBlobChar(text[i-1])) && isBlobChar(text[i]) {
			if end := blobEnd(text, i); end >= 0 {
				i = end
				prevEnd = -1
				continue
			}
		}
		if !isWord(text[i]) {
			i++
			continue
		}
		// A word: text[i:j].
		j := i
		for j < n && isWord(text[j]) {
			j++
		}
		gapSpaced, gapBroken := false, false
		if prevEnd >= 0 {
			if i-prevEnd > maxGap {
				gapBroken = true
			}
			for k := prevEnd; k < i; k++ {
				if isSpace(text[k]) {
					gapSpaced = true
					break
				}
			}
		} else {
			gapBroken = true
		}
		// Cut the word at camel-case boundaries.
		s := i
		for k := i + 1; k < j && j-i <= maxSplit; k++ {
			a, b := text[k-1], text[k]
			split := isLower(a) && isUpper(b) ||
				(isUpper(a) || isDigit(a)) && isUpper(b) && k+1 < j && isLower(text[k+1])
			if split {
				ps = append(ps, part{start: s, end: k, spaced: gapSpaced, broken: gapBroken})
				gapSpaced, gapBroken = false, false
				s = k
			}
		}
		ps = append(ps, part{start: s, end: j, spaced: gapSpaced, broken: gapBroken})
		prevEnd = j
		i = j
	}
	return ps
}

// FNV-1a, 64 bit. A key's hash starts from a seed byte that says whether
// it is compared in lower case or exactly.
const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
	seedLower = 0x00
	seedExact = 0x01
)

func hashStart(seed byte) uint64 {
	return (fnvOffset ^ uint64(seed)) * fnvPrime
}

func hashByte(h uint64, c byte) uint64 {
	return (h ^ uint64(c)) * fnvPrime
}

// Key is how a term is stored: its parts joined with nothing between them,
// lower case unless exact. It also says how many parts the term has and
// how many of its gaps are whitespace.
func Key(term string, exact bool) (key []byte, parts, spaces int) {
	text := []byte(term)
	ps := cut(text, nil)
	for i, p := range ps {
		if i > 0 && p.spaced {
			spaces++
		}
		for k := p.start; k < p.end; k++ {
			c := text[k]
			if !exact {
				c = lower(c)
			}
			key = append(key, c)
		}
	}
	return key, len(ps), spaces
}

// Words are a text's parts in lower case, as a term is matched by them.
func Words(text string) []string {
	b := []byte(text)
	var out []string
	for _, p := range cut(b, nil) {
		w := make([]byte, 0, p.end-p.start)
		for k := p.start; k < p.end; k++ {
			w = append(w, lower(b[k]))
		}
		out = append(out, string(w))
	}
	return out
}

// Hash is the hash of a stored key.
func Hash(key []byte, exact bool) uint64 {
	seed := byte(seedLower)
	if exact {
		seed = seedExact
	}
	h := hashStart(seed)
	for _, c := range key {
		h = hashByte(h, c)
	}
	return nonzero(h)
}

// nonzero keeps 0 free to mean an empty slot.
func nonzero(h uint64) uint64 {
	if h == 0 {
		return 1
	}
	return h
}
