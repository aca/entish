package lineedit

import (
	"strings"
	"unicode"
)

// Query is an fzf extended-search query:
//
//	sbtrkt    fuzzy match
//	'wild     exact substring
//	^music    prefix
//	.mp3$     suffix
//	^make$    whole entry
//	!fire     does not contain
//	!^music   does not start with
//	!.mp3$    does not end with
//	a | b     either term
//
// Terms separated by spaces must all match. Matching is case-insensitive
// unless the term has an uppercase letter (smart case).
type Query [][]term // AND of OR-groups

type termKind int

const (
	fuzzyTerm termKind = iota
	exactTerm
	prefixTerm
	suffixTerm
	equalTerm
)

type term struct {
	kind  termKind
	text  string
	inv   bool
	lower bool // compare lowercased
}

func ParseQuery(q string) Query {
	var out Query
	var group []term
	orNext := false
	for _, f := range strings.Fields(q) {
		if f == "|" {
			orNext = true
			continue
		}
		t, ok := parseTerm(f)
		if !ok {
			continue
		}
		if orNext && len(out) > 0 {
			out[len(out)-1] = append(out[len(out)-1], t)
		} else {
			group = []term{t}
			out = append(out, group)
		}
		orNext = false
	}
	return out
}

func parseTerm(f string) (term, bool) {
	t := term{}
	if strings.HasPrefix(f, "!") {
		t.inv, f = true, f[1:]
		t.kind = exactTerm // fzf: !foo is an inverse exact match
	}
	switch {
	case strings.HasPrefix(f, "'"):
		t.kind, f = exactTerm, f[1:]
	case strings.HasPrefix(f, "^") && strings.HasSuffix(f, "$") && len(f) > 2:
		t.kind, f = equalTerm, f[1:len(f)-1]
	case strings.HasPrefix(f, "^"):
		t.kind, f = prefixTerm, f[1:]
	case strings.HasSuffix(f, "$") && len(f) > 1:
		t.kind, f = suffixTerm, f[:len(f)-1]
	}
	if f == "" {
		return t, false
	}
	t.lower = strings.ToLower(f) == f
	t.text = f
	return t, true
}

// Match reports whether s satisfies the query. An empty query matches
// everything.
func (q Query) Match(s string) bool {
	ls := ""
	for _, g := range q {
		ok := false
		for _, t := range g {
			subject := s
			if t.lower {
				if ls == "" {
					ls = strings.ToLower(s)
				}
				subject = ls
			}
			if t.match(subject) != t.inv {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (t term) match(s string) bool {
	switch t.kind {
	case exactTerm:
		return strings.Contains(s, t.text)
	case prefixTerm:
		return strings.HasPrefix(strings.TrimLeftFunc(s, unicode.IsSpace), t.text)
	case suffixTerm:
		return strings.HasSuffix(strings.TrimRightFunc(s, unicode.IsSpace), t.text)
	case equalTerm:
		return strings.TrimSpace(s) == t.text
	}
	// fuzzy: the term's chars appear in order
	rs := []rune(t.text)
	i := 0
	for _, r := range s {
		if i < len(rs) && r == rs[i] {
			i++
		}
	}
	return i == len(rs)
}
