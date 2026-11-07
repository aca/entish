package lang

import (
	"sort"
	"strings"
)

// SpanKind is what a highlighted range of the source is.
type SpanKind int

const (
	SpanKeyword  SpanKind = iota
	SpanFn                // a fn or builtin: its call or its definition
	SpanExternal          // a call of an external command
	SpanPipe              // | or |>
)

// Span is a range of runes in the source, for coloring.
type Span struct {
	Start, End int
	Kind       SpanKind
}

// Highlight returns the keywords, pipes, fns and externals in src, sorted.
// src may be unfinished: what was read before the parser stopped is still
// returned. Calls resolve as the checker resolves them; when src does not
// parse, by name.
func (in *Interp) Highlight(src string) []Span {
	p := newParser(src, "")
	prog, err := p.parse()
	if err == nil {
		(&checker{}).block(prog, &fnScope{m: in.topFns}) // binds calls; errors don't matter here
	}
	defined := map[string]bool{}
	for _, s := range p.spans {
		if s.call == nil && s.kind == SpanFn {
			defined[string(p.src[s.start:s.end])] = true
		}
	}
	out := make([]Span, 0, len(p.spans))
	for _, s := range p.spans {
		k := s.kind
		if c := s.call; c != nil {
			k = SpanFn
			switch {
			case err == nil && c.target == tExternal:
				k = SpanExternal
			case err == nil:
			case c.Caret || strings.Contains(c.Name, "/"):
				k = SpanExternal
			case in.topFns[c.Name] == nil && !defined[c.Name] && builtins[c.Name] == nil:
				k = SpanExternal
			}
		}
		out = append(out, Span{s.start, s.end, k})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// MatchBracket returns the offset of the bracket that pairs with the one at
// pos in src, or -1. Brackets in strings and comments don't count, and
// nothing past a syntax error does: it is not known what they are.
func (in *Interp) MatchBracket(src string, pos int) int {
	p := newParser(src, "")
	end := len(p.src)
	if _, err := p.parse(); err != nil {
		end = p.pos
	}
	if pos < 0 || pos >= end {
		return -1
	}
	lit := make([]bool, end)
	for _, s := range p.lits {
		for i := s.start; i < min(s.end, end); i++ {
			lit[i] = true
		}
	}
	const opens, closes = "([{", ")]}"
	var stack []int
	for i := 0; i < end; i++ {
		if lit[i] {
			continue
		}
		c := p.src[i]
		if strings.ContainsRune(opens, c) {
			stack = append(stack, i)
			continue
		}
		k := strings.IndexRune(closes, c)
		if k < 0 {
			continue
		}
		if len(stack) == 0 || p.src[stack[len(stack)-1]] != rune(opens[k]) {
			return -1
		}
		open := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch pos {
		case open:
			return i
		case i:
			return open
		}
	}
	return -1
}
