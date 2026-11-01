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
