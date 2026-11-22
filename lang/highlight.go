package lang

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// SpanKind is what a highlighted range of the source is.
type SpanKind int

const (
	SpanKeyword  SpanKind = iota
	SpanFn                // a fn or builtin: its call or its definition
	SpanExternal          // a call of an external command
	SpanPipe              // | |> or |%
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

// Peek returns a line about src at pos for the editor: the variable there,
// else the signature of the builtin whose call pos is in.
func (in *Interp) Peek(src string, pos int) string {
	if s := in.peekVar(src, pos); s != "" {
		return s
	}
	return in.peekSig(src, pos)
}

// peekSig is the Sig of the builtin called by the innermost call whose head
// or args pos is at or just after. In src that does not parse, the call
// being read when the parser stopped counts up to there, and calls resolve
// by name as in Highlight.
func (in *Interp) peekSig(src string, pos int) string {
	p := newParser(src, "")
	prog, err := p.parse()
	if err == nil {
		(&checker{}).block(prog, &fnScope{m: in.topFns})
	}
	var hit *span
	for i, s := range p.spans {
		end := s.argsEnd
		if s.argsEnd == 0 {
			end = p.pos
		}
		if s.call != nil && s.start <= pos && pos <= end && (hit == nil || s.start > hit.start) {
			hit = &p.spans[i]
		}
	}
	if hit == nil {
		return ""
	}
	c := hit.call
	if err == nil {
		if c.target == tBuiltin {
			return c.builtin.Sig
		}
		return ""
	}
	for _, s := range p.spans {
		if s.call == nil && s.kind == SpanFn && string(p.src[s.start:s.end]) == c.Name {
			return "" // a fn defined in src
		}
	}
	if b := builtins[c.Name]; b != nil && !c.Caret && in.topFns[c.Name] == nil {
		return b.Sig
	}
	return ""
}

// peekVar describes the variable whose $name is at pos in src:
// "$xs = [1 2 3]". It shows a variable bound by an earlier input, read from
// the shell's state, or an env var. A name bound in src itself (a let, a
// param) has no value yet and shows nothing, and so does src that does not
// parse: without its scopes the name could be shadowed. Nothing runs.
func (in *Interp) peekVar(src string, pos int) string {
	rs := []rune(src)
	d := pos
	if d >= len(rs) || rs[d] != '$' {
		for d > 0 && (isIdent(rs[d-1]) || rs[d-1] == ':' || rs[d-1] == '{') {
			d--
		}
		d--
	}
	if d < 0 || d >= len(rs) || rs[d] != '$' {
		return ""
	}
	line, col := 1, d+1
	for i, r := range rs[:d] {
		if r == '\n' {
			line, col = line+1, d-i
		}
	}
	prog, err := parse(src, "")
	if err != nil {
		return ""
	}
	var base *vscope
	for e := in.top; e != nil; e = e.parent {
		if e.fns == nil {
			base = base.add(e.name)
		}
	}
	earlier := map[*vscope]bool{}
	for s := base; s != nil; s = s.parent {
		earlier[s] = true
	}
	var hit *VarRef
	fromEarlier := false
	c := &checker{ref: func(v *VarRef, sc *vscope) {
		if v.Pos.Line != line || v.Pos.Col != col {
			return
		}
		hit = v
		for s := sc; s != nil; s = s.parent {
			if s.name == v.Name {
				fromEarlier = earlier[s]
				return
			}
		}
	}}
	c.vblock(prog, base)
	switch {
	case hit == nil:
		return ""
	case hit.Env:
		name := "$" + hit.Name
		if !unicode.IsUpper(rune(hit.Name[0])) {
			name = "$env:" + hit.Name
		}
		if s, ok := in.lookupEnv(hit.Name); ok {
			return name + " = " + peekText(s)
		}
		return name + " is unset"
	case isDigit(rune(hit.Name[0])):
		if l, err := in.results.lookup(hit.Name); err == nil {
			return "$" + hit.Name + " → " + strings.Replace(peekText(l), " items ", " values ", 1)
		}
		return ""
	case !fromEarlier:
		return ""
	}
	for e := in.top; e != nil; e = e.parent {
		if e.fns == nil && e.name == hit.Name {
			return "$" + hit.Name + " = " + peekText(e.val)
		}
	}
	return ""
}

// peekText is v as a literal, cut after about peekMax runes so that a big
// value costs no more than what fits on a line.
func peekText(v Value) string {
	const peekMax = 400
	var b strings.Builder
	var write func(v Value)
	write = func(v Value) {
		if b.Len() > peekMax {
			return
		}
		switch v := v.(type) {
		case string:
			if rs := []rune(v); len(rs) > peekMax {
				v = string(rs[:peekMax])
			}
			b.WriteString(literal(v))
		case *List:
			b.WriteString("[")
			for i, it := range v.Items {
				if i > 0 {
					b.WriteString(" ")
				}
				if b.Len() > peekMax {
					b.WriteString("…")
					break
				}
				write(it)
			}
			b.WriteString("]")
		case *Map:
			if len(v.Keys) == 0 {
				b.WriteString("[:]")
				return
			}
			b.WriteString("[")
			for i, k := range v.Keys {
				if i > 0 {
					b.WriteString("  ")
				}
				if b.Len() > peekMax {
					b.WriteString("…")
					break
				}
				if !isBareKey(k) {
					k = literal(k)
				}
				b.WriteString(k + ": ")
				write(v.M[v.Keys[i]])
			}
			b.WriteString("]")
		default:
			b.WriteString(literal(v))
		}
	}
	write(v)
	if l, ok := v.(*List); ok {
		return strconv.Itoa(len(l.Items)) + " items " + b.String()
	}
	return b.String()
}
