package lang

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Error is a syntax or compile error. Incomplete means the input ended
// inside a construct; the REPL reads another line.
type Error struct {
	Pos        Pos
	Msg        string
	Incomplete bool
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

var keywords = map[string]bool{"let": true, "fn": true, "if": true, "else": true, "try": true, "catch": true, "match": true}

type wordMode int

const (
	modeArg   wordMode = iota // call args: bare words are strings
	modeValue                 // expression operands
	modeData                  // list items, map values: strings must be quoted
)

// ctx carries where a construct is parsed. cond: inside an `if` head, so a
// '{' ends the condition.
type ctx struct {
	cond  bool
	index bool // inside $xs[...]: ':' ends a word
	guard bool // inside a match guard: '=>' ends it
}

type parser struct {
	src        []rune
	pos        int
	file       string
	lineStarts []int
	mlEnd      int           // position right after a multiline string; a statement boundary
	lambdas    []*LambdaAtom // enclosing lambdas, innermost last; nil = fn body
	inStr      bool          // reading a variable interpolated into a string
	spans      []span        // what was read so far, for highlighting
}

// span is a range of runes read as a keyword, a pipe, a fn name or a
// call head (call set; colored once it is resolved).
type span struct {
	start, end int
	kind       SpanKind
	call       *Call
}

func (p *parser) mark(start int, kind SpanKind) {
	p.spans = append(p.spans, span{start: start, end: p.pos, kind: kind})
}

func parse(src, file string) (*Block, error) { return newParser(src, file).parse() }

func newParser(src, file string) *parser {
	p := &parser{src: []rune(src), file: file, mlEnd: -1, lineStarts: []int{0}}
	for i, c := range p.src {
		if c == '\n' {
			p.lineStarts = append(p.lineStarts, i+1)
		}
	}
	return p
}

func (p *parser) parse() (b *Block, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*Error)
			if !ok {
				panic(r)
			}
			b, err = nil, e
		}
	}()
	b = &Block{Pos: p.posAt(0)}
	p.stmts(b, 0)
	return b, nil
}

// ---- helpers

func (p *parser) posAt(off int) Pos {
	line := sort.Search(len(p.lineStarts), func(i int) bool { return p.lineStarts[i] > off }) - 1
	return Pos{File: p.file, Line: line + 1, Col: off - p.lineStarts[line] + 1}
}

func (p *parser) here() Pos { return p.posAt(p.pos) }

func (p *parser) fail(msg string) { panic(&Error{Pos: p.here(), Msg: msg}) }

func (p *parser) failAt(off int, msg string) { panic(&Error{Pos: p.posAt(off), Msg: msg}) }

func (p *parser) incomplete(msg string) {
	panic(&Error{Pos: p.here(), Msg: msg, Incomplete: true})
}

func (p *parser) peek() rune { return p.at(0) }

func (p *parser) at(off int) rune {
	if i := p.pos + off; i >= 0 && i < len(p.src) {
		return p.src[i]
	}
	return 0
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) hasPrefix(s string) bool {
	i := p.pos
	for _, c := range s {
		if i >= len(p.src) || p.src[i] != c {
			return false
		}
		i++
	}
	return true
}

func (p *parser) expect(c rune) {
	if p.eof() {
		p.incomplete(fmt.Sprintf("missing %q", c))
	}
	if p.peek() != c {
		p.fail(fmt.Sprintf("expected %q, found %q", c, p.peek()))
	}
	p.pos++
}

// skipSpace skips blanks and a comment ('#' at a word start) on this line.
func (p *parser) skipSpace() {
	for {
		switch p.peek() {
		case ' ', '\t', '\r':
			p.pos++
		case '#':
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *parser) skipSpaceNL() {
	for {
		p.skipSpace()
		if p.peek() != '\n' {
			return
		}
		p.pos++
	}
}

func isIdentStart(c rune) bool { return c == '_' || unicode.IsLetter(c) && c < 128 }
func isIdent(c rune) bool      { return isIdentStart(c) || c >= '0' && c <= '9' }
func isKeyChar(c rune) bool    { return isIdent(c) || c == '-' }

func (p *parser) ident() string {
	start := p.pos
	if !isIdentStart(p.peek()) {
		return ""
	}
	for isIdent(p.peek()) {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

// peekWord returns the identifier at pos if it is followed by a word
// boundary.
func (p *parser) peekWord() string {
	i := p.pos
	for i < len(p.src) && isIdent(p.src[i]) {
		i++
	}
	if i == p.pos {
		return ""
	}
	if i < len(p.src) {
		switch p.src[i] {
		case ' ', '\t', '\r', '\n', ';', '{', '}':
		default:
			return ""
		}
	}
	return string(p.src[p.pos:i])
}

func (p *parser) keyword() string {
	if w := p.peekWord(); keywords[w] {
		return w
	}
	return ""
}

func (p *parser) checkMore(after string) {
	if p.eof() {
		p.incomplete("expected more after " + after)
	}
}

// ---- statements

func (p *parser) stmts(b *Block, end rune) {
	for {
		p.skipSpaceNL()
		for p.peek() == ';' {
			p.pos++
			p.skipSpaceNL()
		}
		if p.eof() {
			if end != 0 {
				p.incomplete(fmt.Sprintf("missing %q", end))
			}
			return
		}
		if end != 0 && p.peek() == end {
			return
		}
		s := p.stmt()
		b.Stmts = append(b.Stmts, s)
		if f, ok := s.(*FnStmt); ok {
			b.Fns = append(b.Fns, f.Def)
		}
		p.endStmt(end)
	}
}

func (p *parser) endStmt(end rune) {
	if p.pos == p.mlEnd {
		return
	}
	p.skipSpace()
	c := p.peek()
	if c == 0 || c == '\n' || c == ';' || end != 0 && c == end {
		return
	}
	p.fail(fmt.Sprintf("unexpected %q", c))
}

func (p *parser) stmt() Stmt {
	switch kw := p.keyword(); kw {
	case "let":
		return p.letStmt()
	case "fn":
		return &FnStmt{Def: p.fnDef()}
	case "else", "catch":
		p.fail(kw + " must follow '}' on the same line")
	}
	return &ExprStmt{X: p.chain(ctx{})}
}

func (p *parser) varName(what string) string {
	start := p.pos
	name := p.ident()
	if name == "" {
		p.fail("expected a name after " + what)
	}
	if keywords[name] || reservedVars[name] {
		p.failAt(start, name+" is reserved")
	}
	if unicode.IsUpper(rune(name[0])) {
		p.failAt(start, name+" is an env var name (uppercase); variables start with a lowercase letter or _")
	}
	return name
}

func (p *parser) letStmt() Stmt {
	s := &LetStmt{Pos: p.here()}
	p.pos += 3
	p.mark(p.pos-3, SpanKeyword)
	p.skipSpace()
	s.Name = p.varName("let")
	p.skipSpace()
	if p.peek() != '=' {
		p.fail("expected '=' after let " + s.Name)
	}
	p.pos++
	p.skipSpaceNL()
	p.checkMore("=")
	s.Val = p.chain(ctx{})
	return s
}

func (p *parser) fnDef() *FnDef {
	d := &FnDef{Pos: p.here()}
	start0 := p.pos
	p.pos += 2
	p.mark(p.pos-2, SpanKeyword)
	p.skipSpace()
	start := p.pos
	d.Name = p.ident()
	if d.Name == "" {
		p.fail("expected a fn name")
	}
	p.mark(start, SpanFn)
	for _, c := range d.Name {
		if unicode.IsUpper(c) {
			p.failAt(start, "fn names are snake_case: [a-z_][a-z0-9_]*")
		}
	}
	if keywords[d.Name] {
		p.failAt(start, d.Name+" is a keyword")
	}
	d.Params, d.Rest = p.params('{')
	p.lambdas = append(p.lambdas, nil) // $it never refers past a fn body
	d.Body = p.block()
	p.lambdas = p.lambdas[:len(p.lambdas)-1]
	d.Src = string(p.src[start0:p.pos])
	return d
}

// useIt marks the innermost lambda as taking one param, $it.
func (p *parser) useIt(pos Pos) {
	if len(p.lambdas) == 0 || p.lambdas[len(p.lambdas)-1] == nil {
		return // not in a lambda: an ordinary (undefined) variable
	}
	la := p.lambdas[len(p.lambdas)-1]
	if la.Params != nil || la.Rest != "" {
		panic(&Error{Pos: pos, Msg: "this fn names its params; use them instead of $it"})
	}
	la.usesIt = true
}

func (p *parser) params(end rune) (params []string, rest string) {
	for {
		p.skipSpace()
		if p.peek() == end {
			return
		}
		if p.eof() || p.peek() == '\n' {
			p.fail(fmt.Sprintf("expected a parameter or %q", end))
		}
		if rest != "" {
			p.fail("...rest must be the last parameter")
		}
		if p.hasPrefix("...") {
			p.pos += 3
			rest = p.varName("...")
			continue
		}
		params = append(params, p.varName("fn"))
	}
}

func (p *parser) block() *Block {
	if p.peek() != '{' {
		p.fail("expected '{'")
	}
	b := &Block{Pos: p.here()}
	p.pos++
	p.stmts(b, '}')
	p.expect('}')
	return b
}

// ---- chains and pipelines

func (p *parser) chain(c ctx) Expr {
	x := p.and(c)
	for {
		p.skipSpace()
		if !p.hasPrefix("||") {
			return x
		}
		pos := p.here()
		p.pos += 2
		p.skipSpaceNL()
		p.checkMore("||")
		x = &OrExpr{P: pos, L: x, R: p.and(c)}
	}
}

func (p *parser) and(c ctx) Expr {
	x := p.not(c)
	for {
		p.skipSpace()
		if !p.hasPrefix("&&") {
			return x
		}
		pos := p.here()
		p.pos += 2
		p.skipSpaceNL()
		p.checkMore("&&")
		x = &AndExpr{P: pos, L: x, R: p.not(c)}
	}
}

func (p *parser) not(c ctx) Expr {
	p.skipSpace()
	if p.peek() == '!' && p.at(1) != '=' {
		pos := p.here()
		p.pos++
		p.skipSpace()
		return &NotExpr{P: pos, X: p.not(c)}
	}
	return p.pipeline(c)
}

func (p *parser) pipeline(c ctx) Expr {
	pl := &Pipeline{P: p.here()}
	pl.Stages = append(pl.Stages, p.stage(c))
	for {
		p.skipSpace()
		var op string
		switch {
		case p.hasPrefix("|>"):
			op = "|>"
		case p.peek() == '|' && p.at(1) != '|':
			op = "|"
		default:
			if len(pl.Stages) == 1 {
				return pl.Stages[0]
			}
			return pl
		}
		p.pos += len(op)
		p.mark(p.pos-len(op), SpanPipe)
		p.skipSpaceNL()
		p.checkMore(op)
		st := p.stage(c)
		// `|> $f` calls the fn value with the piped value, `| $f` with no
		// args (its stdin is the pipe).
		if we, ok := st.(*WordExpr); ok {
			if v, ok := singleVar(we.W); ok {
				st = &Call{P: we.W.Pos, HeadVar: v}
			}
		}
		pl.Ops = append(pl.Ops, op)
		pl.Stages = append(pl.Stages, st)
	}
}

func singleVar(w *Word) (*VarRef, bool) {
	if len(w.Parts) == 1 {
		if v, ok := w.Parts[0].(*VarRef); ok && !v.Env {
			return v, true
		}
	}
	return nil, false
}

func (p *parser) stage(c ctx) Expr {
	p.skipSpace()
	if p.eof() {
		p.incomplete("expected a command")
	}
	switch kw := p.keyword(); kw {
	case "if":
		return p.ifExpr()
	case "try":
		return p.tryExpr()
	case "match":
		return p.matchExpr()
	case "let", "fn":
		p.fail(kw + " must start a statement")
	case "else", "catch":
		p.fail(kw + " must follow '}' on the same line")
	}
	if p.peek() == '^' {
		return p.call(c, true)
	}
	if _, ok := p.peekCommandName(); ok {
		return p.call(c, false)
	}
	w := p.word(modeValue, c)
	if v, ok := singleVar(w); ok && p.argAhead(c) {
		call := &Call{P: w.Pos, HeadVar: v}
		p.args(call, c)
		return call
	}
	return p.exprFrom(w, c)
}

func isNameEnd(c rune) bool {
	switch c {
	case 0, ' ', '\t', '\r', '\n', ';', '|', '&', '<', '>', '(', ')', '[', ']', '{', '}', '\'', '"', '$':
		return true
	}
	return false
}

// peekCommandName reports a plain bare word at pos: a fn or command name.
func (p *parser) peekCommandName() (string, bool) {
	if p.hasPrefix(`\\`) {
		return "", false
	}
	i := p.pos
	for i < len(p.src) && !isNameEnd(p.src[i]) {
		i++
	}
	if i == p.pos {
		return "", false
	}
	if i < len(p.src) {
		switch p.src[i] {
		case '\'', '"', '$', '[', '(', '{':
			return "", false // glued to something: not a plain name
		}
	}
	name := string(p.src[p.pos:i])
	if strings.ContainsAny(name, "*?") || isNumber(name) {
		return "", false
	}
	return name, true
}

// argAhead reports whether a call argument follows, as opposed to the end of
// the stage or a binary operator.
func (p *parser) argAhead(c ctx) bool {
	save := p.pos
	defer func() { p.pos = save }()
	if p.pos == p.mlEnd {
		return false
	}
	p.skipSpace()
	if p.callEnd(c) {
		return false
	}
	return p.peekBinOp() == ""
}

func (p *parser) call(c ctx, caret bool) *Call {
	call := &Call{P: p.here(), Caret: caret}
	start := p.pos
	if caret {
		p.pos++
		if p.peek() == '$' {
			v, ok := singleVar(p.word(modeArg, c))
			if !ok {
				p.fail("^ takes a command name or one $var: ^$cmd args")
			}
			call.HeadVar = v
			p.args(call, c)
			return call
		}
	}
	name, ok := p.peekCommandName()
	if !ok {
		p.fail("expected a command name")
	}
	p.pos += len([]rune(name))
	call.Name = name
	p.spans = append(p.spans, span{start: start, end: p.pos, call: call})
	p.args(call, c)
	return call
}

func (p *parser) args(call *Call, c ctx) {
	for {
		if p.pos == p.mlEnd {
			return
		}
		p.skipSpace()
		if p.callEnd(c) {
			return
		}
		if r := p.redirect(); r != nil {
			call.Redirs = append(call.Redirs, r)
			continue
		}
		call.Args = append(call.Args, p.word(modeArg, c))
	}
}

func (p *parser) callEnd(c ctx) bool {
	switch p.peek() {
	case 0, '\n', ';', '|', ')', '}', ']':
		return true
	case '&':
		if p.at(1) == '&' {
			return true
		}
		p.fail("'&' is not supported (no background jobs)")
	case '{':
		return c.cond
	case '=':
		return c.guard && p.at(1) == '>'
	}
	return false
}

var redirOps = []string{"2>&1", ">&2", "2>>", "2>", ">>", ">", "<"}

func (p *parser) redirect() *Redir {
	for _, op := range redirOps {
		if !p.hasPrefix(op) {
			continue
		}
		p.pos += len(op)
		r := &Redir{Op: op}
		if op == "2>&1" || op == ">&2" {
			return r
		}
		p.skipSpace()
		if p.callEnd(ctx{}) {
			p.fail("expected a file after " + op)
		}
		r.Target = p.word(modeArg, ctx{})
		return r
	}
	return nil
}

// ---- expressions

var precedence = map[string]int{
	"==": 1, "!=": 1, "<": 1, ">": 1, "<=": 1, ">=": 1,
	"+": 2, "-": 2,
	"*": 3, "/": 3, "%": 3,
	"**": 4,
}

// peekBinOp returns a standalone binary operator at pos (surrounded by
// blanks), or "".
func (p *parser) peekBinOp() string {
	i := p.pos
	for i < len(p.src) && strings.ContainsRune("+-*/%=!<>", p.src[i]) {
		i++
	}
	if i == p.pos {
		return ""
	}
	if i < len(p.src) && !strings.ContainsRune(" \t\r\n", p.src[i]) {
		return ""
	}
	op := string(p.src[p.pos:i])
	if _, ok := precedence[op]; !ok {
		return ""
	}
	return op
}

func (p *parser) exprFrom(first *Word, c ctx) Expr {
	return p.binRest(p.operand(first), 1, c)
}

func (p *parser) operand(w *Word) Expr {
	if len(w.Parts) == 1 {
		if l, ok := w.Parts[0].(*LitAtom); ok && !l.Glob {
			panic(&Error{Pos: w.Pos, Msg: fmt.Sprintf("%s is a bare word in a value: quote it ('%s') or call it in parens (%s ...)", l.S, l.S, l.S)})
		}
	}
	return &WordExpr{W: w}
}

func (p *parser) binRest(left Expr, minPrec int, c ctx) Expr {
	for {
		if p.pos == p.mlEnd {
			return left
		}
		save := p.pos
		p.skipSpace()
		op := p.peekBinOp()
		prec := precedence[op]
		if op == "" || prec < minPrec {
			p.pos = save
			return left
		}
		pos := p.here()
		p.pos += len(op)
		p.skipSpace()
		p.checkMore(op)
		if p.peek() == '\n' || c.cond && p.peek() == '{' {
			p.fail("expected a value after " + op)
		}
		if name, ok := p.peekCommandName(); ok {
			if c.index {
				name, _, _ = strings.Cut(name, ":") // $xs[$i + 1:4]
			}
			if name != "" && !isNumber(name) {
				p.fail("a call inside an expression needs parens: (" + name + " ...)")
			}
		}
		right := p.operand(p.word(modeValue, c))
		next := prec + 1
		if op == "**" {
			next = prec
		}
		right = p.binRest(right, next, c)
		left = &BinExpr{P: pos, Op: op, L: left, R: right}
		if prec == 1 {
			save := p.pos
			p.skipSpace()
			if precedence[p.peekBinOp()] == 1 {
				p.fail("comparisons do not chain; use &&")
			}
			p.pos = save
		}
	}
}

func (p *parser) ifExpr() Expr {
	e := &IfExpr{P: p.here()}
	p.pos += 2
	p.mark(p.pos-2, SpanKeyword)
	p.skipSpace()
	p.checkMore("if")
	e.Cond = p.chain(ctx{cond: true})
	p.skipSpace()
	if p.peek() != '{' {
		p.fail("expected '{' after the if condition")
	}
	e.Then = p.block()
	save := p.pos
	p.skipSpace()
	if p.keyword() != "else" {
		p.pos = save
		return e
	}
	p.pos += 4
	p.mark(p.pos-4, SpanKeyword)
	p.skipSpace()
	if p.keyword() == "if" {
		e.Else = p.ifExpr()
	} else {
		e.Else = &BlockExpr{B: p.block()}
	}
	return e
}

func (p *parser) tryExpr() Expr {
	e := &TryExpr{P: p.here()}
	p.pos += 3
	p.mark(p.pos-3, SpanKeyword)
	p.skipSpace()
	switch p.peek() {
	case '{':
		e.Body = &BlockExpr{B: p.block()}
	case '$':
		w := p.word(modeValue, ctx{cond: true})
		v, ok := singleVar(w)
		if !ok {
			p.fail("try takes a block or a fn value")
		}
		e.Body = &Call{P: w.Pos, HeadVar: v}
	default:
		p.fail("try takes a block or a fn value")
	}
	p.skipSpace()
	if p.keyword() != "catch" {
		p.fail("expected catch on the same line after try { ... }")
	}
	p.pos += 5
	p.mark(p.pos-5, SpanKeyword)
	p.skipSpace()
	e.Name = p.varName("catch")
	p.skipSpace()
	e.Handler = p.block()
	return e
}

// matchExpr parses `match value { pattern [if guard] => body ... }`. Arms
// are separated by newlines or ';'. A body is a block or one expression.
func (p *parser) matchExpr() Expr {
	e := &MatchExpr{P: p.here()}
	p.pos += 5
	p.mark(p.pos-5, SpanKeyword)
	p.skipSpace()
	p.checkMore("match")
	if p.peek() == '{' {
		p.fail("match takes its value first: match $x { ... }; it cannot follow |>")
	}
	e.X = p.chain(ctx{cond: true})
	p.skipSpace()
	if p.peek() != '{' {
		p.fail("expected '{' after the match value")
	}
	p.pos++
	for {
		p.skipSpaceNL()
		for p.peek() == ';' {
			p.pos++
			p.skipSpaceNL()
		}
		if p.eof() {
			p.incomplete("missing '}'")
		}
		if p.peek() == '}' {
			p.pos++
			break
		}
		e.Arms = append(e.Arms, p.matchArm())
		p.endStmt('}')
	}
	if len(e.Arms) == 0 {
		panic(&Error{Pos: e.P, Msg: "match needs at least one arm: pattern => body"})
	}
	return e
}

func (p *parser) matchArm() *MatchArm {
	arm := &MatchArm{Pat: p.pattern()}
	p.skipSpace()
	if p.keyword() == "if" {
		p.pos += 2
		p.mark(p.pos-2, SpanKeyword)
		p.skipSpace()
		p.checkMore("if")
		arm.Guard = p.chain(ctx{guard: true})
		p.skipSpace()
	}
	if p.eof() {
		p.incomplete("expected => after the pattern")
	}
	if !p.hasPrefix("=>") {
		p.fail("expected => after the pattern")
	}
	p.pos += 2
	p.mark(p.pos-2, SpanKeyword)
	p.skipSpace()
	// On the next line, a body could not be told apart from the next arm.
	if c := p.peek(); c == 0 || c == '\n' || c == '}' || c == ';' {
		p.fail("expected a body on the same line after =>; for more lines use a block: => { ... }")
	}
	if p.peek() == '{' {
		save := p.pos
		p.pos++
		p.skipSpace()
		if p.peek() == '|' && p.at(1) != '|' && p.at(1) != '>' {
			p.fail("the { } after => is a block; to give a fn, put it inside one: => { {|x| ... } }")
		}
		p.pos = save
		arm.Body = p.block()
		return arm
	}
	pos := p.here()
	arm.Body = &Block{Pos: pos, Stmts: []Stmt{&ExprStmt{X: p.chain(ctx{})}}}
	return arm
}

// pattern parses one match pattern.
func (p *parser) pattern() Pattern {
	pos := p.here()
	switch ch := p.peek(); {
	case ch == '[':
		return p.collectionPattern()
	case ch == '\'':
		return &LitPat{Pos: pos, Val: p.rawString().(*StrAtom).Parts[0].Lit}
	case ch == '"':
		s := p.interpString().(*StrAtom)
		var b strings.Builder
		for _, part := range s.Parts {
			if part.Var != nil {
				panic(&Error{Pos: pos, Msg: "a pattern string cannot interpolate; match a variable's value with $" + part.Var.Name})
			}
			b.WriteString(part.Lit)
		}
		return &LitPat{Pos: pos, Val: b.String()}
	case ch == '$':
		a := p.dollar()
		v, ok := a.(*VarRef)
		if !ok {
			panic(&Error{Pos: pos, Msg: "a pattern cannot run code; bind the value with let first"})
		}
		return &VarPat{V: v}
	}
	start := p.pos
	for !isWordEnd(p.peek()) && p.peek() != '[' && p.peek() != '{' && !p.hasPrefix("=>") {
		p.pos++
	}
	w := string(p.src[start:p.pos])
	switch {
	case w == "":
		if p.eof() {
			p.incomplete("expected a pattern")
		}
		p.fail("expected a pattern")
	case w == "_":
		return &AnyPat{Pos: pos}
	case isNumber(w):
		return &LitPat{Pos: pos, Val: numLit(w)}
	case reservedVars[w] && w != "it":
		p.failAt(start, fmt.Sprintf("%s in a pattern would bind a variable named %s; to match the value write $%s", w, w, w))
	}
	if strings.IndexFunc(w, func(c rune) bool { return !isIdent(c) }) >= 0 {
		p.failAt(start, fmt.Sprintf("bare word %s in a pattern: quote it, '%s'; a plain name binds a variable", w, w))
	}
	p.pos = start
	return &BindPat{Pos: pos, Name: p.varName("a pattern")}
}

// collectionPattern parses [a b ...rest], [k: pat] or [:].
func (p *parser) collectionPattern() Pattern {
	pos := p.here()
	p.pos++
	p.skipSpaceNL()
	if p.hasPrefix(":]") {
		p.pos += 2
		return &MapPat{Pos: pos}
	}
	var list *ListPat
	var m *MapPat
	for {
		p.skipSpaceNL()
		if p.eof() {
			p.incomplete("missing ']'")
		}
		if p.peek() == ']' {
			p.pos++
			break
		}
		if key, ok := p.mapKey(); ok {
			if list != nil {
				p.fail("mixed list items and map entries")
			}
			if m == nil {
				m = &MapPat{Pos: pos}
			}
			p.skipSpaceNL()
			if p.peek() == ']' {
				p.fail("missing pattern for key " + key)
			}
			if slices.Contains(m.Keys, key) {
				p.fail("duplicate key " + key + " in pattern")
			}
			m.Keys = append(m.Keys, key)
			m.Vals = append(m.Vals, p.pattern())
			continue
		}
		if m != nil {
			p.fail("mixed list items and map entries")
		}
		if list == nil {
			list = &ListPat{Pos: pos}
		}
		if list.Rest != "" {
			p.fail("...rest must be the last item of a list pattern")
		}
		if p.hasPrefix("...") {
			p.pos += 3
			if p.peek() == '_' && isWordEnd(p.at(1)) {
				p.pos++
				list.Rest = "_"
			} else {
				list.Rest = p.varName("...")
			}
			continue
		}
		list.Items = append(list.Items, p.pattern())
	}
	if m != nil {
		return m
	}
	if list == nil {
		return &ListPat{Pos: pos}
	}
	return list
}

// ---- words

func isWordEnd(c rune) bool {
	switch c {
	case 0, ' ', '\t', '\r', '\n', ';', '|', '&', '<', '>', ')', ']', '}':
		return true
	}
	return false
}

func (p *parser) word(mode wordMode, c ctx) *Word {
	w := &Word{Pos: p.here()}
	add := func(a Atom) { w.Parts = append(w.Parts, a) }
	if c.cond && p.peek() == '{' {
		p.fail("expected a value before '{'")
	}
	switch {
	case p.hasPrefix(`\\`), p.hasPrefix(`$\\`):
		add(p.mlString())
		return w
	case p.hasPrefix("...$"), p.hasPrefix("...("):
		p.pos += 3
		var x Atom
		if p.peek() == '$' {
			x = p.dollar()
			if _, ok := x.(*VarRef); !ok {
				p.fail("expected a variable after ...")
			}
		} else {
			x = p.group()
		}
		add(&SpreadAtom{X: x})
		return w
	case p.peek() == '[':
		add(p.bracket())
	case p.peek() == '{':
		add(p.lambda())
	case p.peek() == '(':
		add(p.group())
	case p.peek() == '~':
		add(p.tilde())
	}
	for {
		ch := p.peek()
		switch {
		case isWordEnd(ch), c.index && ch == ':':
			return p.finishWord(w, mode)
		case ch == '\'':
			add(p.rawString())
		case ch == '"':
			add(p.interpString())
		case ch == '$':
			add(p.dollar())
		case ch == '[':
			add(p.globClass())
		case ch == '(':
			p.fail("quote words containing '(' — or put a space before it")
		case ch == '{':
			p.fail("quote words containing '{'")
		default:
			add(p.bare(c))
		}
	}
}

// tilde reads a ~ that starts an arg or value: ~ and ~/... are $HOME. At
// the head of a command, ~/x is the path of an external (lookPath).
func (p *parser) tilde() Atom {
	pos := p.here()
	if next := p.at(1); next != '/' && !isWordEnd(next) {
		p.fail("~ expands only as ~ or ~/...: quote it ('~x') or use $HOME")
	}
	p.pos++
	return &HomeAtom{Pos: pos}
}

func (p *parser) finishWord(w *Word, mode wordMode) *Word {
	if len(w.Parts) == 1 {
		if l, ok := w.Parts[0].(*LitAtom); ok && !l.Glob && isNumber(l.S) {
			w.Parts[0] = &NumAtom{N: numLit(l.S)}
		}
	}
	if mode == modeData {
		for _, a := range w.Parts {
			if l, ok := a.(*LitAtom); ok && !l.Glob && !strings.ContainsAny(l.S, "*?") {
				panic(&Error{Pos: w.Pos, Msg: fmt.Sprintf("bare word in a list or map: quote it, '%s'", l.S)})
			}
		}
	}
	return w
}

func (p *parser) bare(c ctx) Atom {
	start := p.pos
	for {
		ch := p.peek()
		if isWordEnd(ch) || c.index && ch == ':' || strings.ContainsRune(`'"$[({`, ch) {
			break
		}
		p.pos++
	}
	s := string(p.src[start:p.pos])
	return &LitAtom{S: s, Glob: strings.ContainsAny(s, "*?")}
}

func (p *parser) globClass() Atom {
	start := p.pos
	for p.peek() != ']' {
		if p.eof() || p.peek() == '\n' {
			p.failAt(start, "unterminated [ in a glob")
		}
		p.pos++
	}
	p.pos++
	return &LitAtom{S: string(p.src[start:p.pos]), Glob: true}
}

func (p *parser) rawString() Atom {
	start := p.pos
	p.pos++
	var b strings.Builder
	for {
		ch := p.peek()
		if ch == 0 || ch == '\n' {
			p.failAt(start, "unterminated string")
		}
		p.pos++
		if ch == '\'' {
			if p.peek() == '\'' {
				b.WriteRune('\'')
				p.pos++
				continue
			}
			break
		}
		b.WriteRune(ch)
	}
	return &StrAtom{Parts: []StrPart{{Lit: b.String()}}}
}

func (p *parser) interpString() Atom {
	start := p.pos
	p.pos++
	s := &StrAtom{}
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			s.Parts = append(s.Parts, StrPart{Lit: b.String()})
			b.Reset()
		}
	}
	for {
		ch := p.peek()
		switch {
		case ch == 0 || ch == '\n':
			p.failAt(start, "unterminated string")
		case ch == '"':
			p.pos++
			flush()
			return s
		case ch == '\\':
			p.escape(&b)
		case ch == '$' && (isIdentStart(p.at(1)) || p.at(1) == '{'):
			flush()
			p.pos++
			s.Parts = append(s.Parts, StrPart{Var: p.strVarRef()})
		default:
			b.WriteRune(ch)
			p.pos++
		}
	}
}

func (p *parser) escape(b *strings.Builder) {
	start := p.pos
	p.pos++
	ch := p.peek()
	p.pos++
	switch ch {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case '0':
		b.WriteByte(0)
	case '"', '\\', '$':
		b.WriteRune(ch)
	case 'x':
		h := string(p.src[p.pos:min(p.pos+2, len(p.src))])
		n, err := strconv.ParseUint(h, 16, 8)
		if err != nil || len(h) != 2 {
			p.failAt(start, `\x needs two hex digits`)
		}
		b.WriteByte(byte(n))
		p.pos += 2
	case 'u':
		if p.peek() != '{' {
			p.failAt(start, `\u needs braces: \u{1F600}`)
		}
		end := p.pos
		for end < len(p.src) && p.src[end] != '}' {
			end++
		}
		n, err := strconv.ParseUint(string(p.src[p.pos+1:min(end, len(p.src))]), 16, 32)
		if err != nil || end >= len(p.src) {
			p.failAt(start, `bad \u{...} escape`)
		}
		b.WriteRune(rune(n))
		p.pos = end + 1
	default:
		p.failAt(start, fmt.Sprintf(`unknown escape \%c`, ch))
	}
}

// dollar parses $( ), ${name}, $name, $NAME, $env:name; a lone $ is text.
func (p *parser) dollar() Atom {
	if p.at(1) == '(' {
		p.pos += 2
		p.skipSpaceNL()
		p.checkMore("$(")
		x := p.chain(ctx{})
		p.skipSpaceNL()
		p.expect(')')
		return &SubAtom{X: x}
	}
	if isIdentStart(p.at(1)) || p.at(1) == '{' {
		p.pos++
		return p.varRefAfterDollar()
	}
	p.pos++
	return &LitAtom{S: "$"}
}

func (p *parser) varRefAfterDollar() *VarRef {
	v := &VarRef{Pos: p.posAt(p.pos - 1)}
	if p.peek() == '{' {
		p.pos++
		v.Name = p.ident()
		if v.Name == "" {
			p.fail("expected a name in ${...}")
		}
		p.expect('}')
	} else {
		v.Name = p.ident()
		if v.Name == "env" && p.peek() == ':' && isKeyChar(p.at(1)) {
			p.pos++
			start := p.pos
			for isIdent(p.peek()) {
				p.pos++
			}
			v.Name, v.Env = string(p.src[start:p.pos]), true
			return v
		}
	}
	if unicode.IsUpper(rune(v.Name[0])) {
		v.Env = true
		return v
	}
	if v.Name == "it" {
		p.useIt(v.Pos)
	}
	for p.peek() == '[' {
		v.Index = append(v.Index, p.index())
	}
	return v
}

func (p *parser) index() Index {
	start := p.pos
	p.pos++
	p.skipSpace()
	var ix Index
	at := p.pos
	key := p.indexArg()
	if p.peek() == ':' {
		p.pos++
		ix.Lo, ix.Hi = key, p.indexArg()
	} else if key == nil {
		p.failAt(start, "empty index")
	} else {
		ix.Key = key
	}
	p.skipSpace()
	if p.peek() != ']' {
		if ix.Key != nil && !p.inStr && p.src[at] != '\'' && !p.eof() && p.peek() != '\n' {
			if name, ok := ix.Key.Lit.(string); ok {
				p.failAt(at, "a call in an index needs parens: $xs[("+name+" ...)]")
			}
		}
		p.failAt(start, "unterminated index")
	}
	p.pos++
	return ix
}

// strVarRef reads a variable interpolated into a string.
func (p *parser) strVarRef() *VarRef {
	save := p.inStr
	p.inStr = true
	defer func() { p.inStr = save }()
	return p.varRefAfterDollar()
}

// indexArg reads a key, or one end of a slice; nil if there is none. It is
// a literal, a variable or an expression: $xs[(len $xs) - 1], $xs[$i + 1].
func (p *parser) indexArg() *IndexArg {
	const noCode = "a string interpolates variables, never code; bind it first: let i = ..."
	var a IndexArg
	w := &Word{Pos: p.here()}
	switch p.peek() {
	case '(':
		if p.inStr {
			p.fail(noCode)
		}
		g := p.group().(*GroupAtom)
		a.X, w.Parts = g.X, []Atom{g}
	case '$':
		p.pos++
		a.Var = p.varRefAfterDollar()
		w.Parts = []Atom{a.Var}
	case '\'':
		s := p.rawString().(*StrAtom)
		a.Lit, w.Parts = s.Parts[0].Lit, []Atom{s}
	default:
		s := p.pos
		for !p.eof() && !strings.ContainsRune("]: \t\n", p.peek()) {
			p.pos++
		}
		lit := string(p.src[s:p.pos])
		if lit == "" {
			return nil
		}
		if isNumber(lit) {
			a.Lit = numLit(lit)
			w.Parts = []Atom{&NumAtom{N: a.Lit.(*Num)}}
		} else {
			a.Lit = lit
			w.Parts = []Atom{&LitAtom{S: lit}}
		}
	}
	save := p.pos
	p.skipSpace()
	if p.peekBinOp() == "" {
		p.pos = save
		return &a
	}
	if p.inStr {
		p.fail(noCode)
	}
	return &IndexArg{X: p.binRest(p.operand(w), 1, ctx{index: true})}
}

func (p *parser) mlString() Atom {
	ml := &MLAtom{}
	for {
		interp := false
		switch {
		case p.hasPrefix(`$\\`):
			interp = true
			p.pos += 3
		case p.hasPrefix(`\\`):
			p.pos += 2
		}
		var line []StrPart
		var b strings.Builder
		for !p.eof() && p.peek() != '\n' {
			ch := p.peek()
			if interp && ch == '$' && (isIdentStart(p.at(1)) || p.at(1) == '{') {
				if b.Len() > 0 {
					line = append(line, StrPart{Lit: b.String()})
					b.Reset()
				}
				p.pos++
				line = append(line, StrPart{Var: p.strVarRef()})
				continue
			}
			b.WriteRune(ch)
			p.pos++
		}
		b.WriteByte('\n')
		line = append(line, StrPart{Lit: b.String()})
		ml.Lines = append(ml.Lines, line)
		if p.eof() {
			break
		}
		p.pos++ // newline
		for p.peek() == ' ' || p.peek() == '\t' {
			p.pos++
		}
		if !p.hasPrefix(`\\`) && !p.hasPrefix(`$\\`) {
			break
		}
	}
	p.mlEnd = p.pos
	return ml
}

func (p *parser) group() Atom {
	p.pos++
	p.skipSpaceNL()
	if p.peek() == ')' {
		p.fail("empty ()")
	}
	p.checkMore("(")
	x := p.chain(ctx{})
	p.skipSpaceNL()
	p.expect(')')
	return &GroupAtom{X: x}
}

func (p *parser) lambda() Atom {
	la := &LambdaAtom{Pos: p.here()}
	start := p.pos
	p.pos++
	// {|x| ...} and { |x| ... } are the same: no statement starts with '|'.
	save := p.pos
	p.skipSpace()
	if p.peek() == '|' && p.at(1) != '>' && p.at(1) != '|' {
		p.pos++
		la.Params, la.Rest = p.params('|')
		p.pos++
	} else {
		p.pos = save
	}
	b := &Block{Pos: la.Pos}
	p.lambdas = append(p.lambdas, la)
	p.stmts(b, '}')
	p.lambdas = p.lambdas[:len(p.lambdas)-1]
	p.expect('}')
	la.Body = b
	la.Src = string(p.src[start:p.pos])
	if la.usesIt {
		la.Params = []string{"it"} // a block that uses $it takes one param
	}
	return la
}

// bracket parses a list [a b] or a map [k: v]; [:] is the empty map.
func (p *parser) bracket() Atom {
	p.pos++
	p.skipSpaceNL()
	if p.hasPrefix(":]") {
		p.pos += 2
		return &MapAtom{}
	}
	var list *ListAtom
	var m *MapAtom
	for {
		p.skipSpaceNL()
		if p.eof() {
			p.incomplete("missing ']'")
		}
		if p.peek() == ']' {
			p.pos++
			break
		}
		if key, ok := p.mapKey(); ok {
			if list != nil {
				p.fail("mixed list items and map entries")
			}
			if m == nil {
				m = &MapAtom{}
			}
			p.skipSpaceNL()
			if p.peek() == ']' {
				p.fail("missing value for key " + key)
			}
			if slices.Contains(m.Keys, key) {
				p.fail("duplicate key " + key + " in map")
			}
			m.Keys = append(m.Keys, key)
			m.Vals = append(m.Vals, p.word(modeData, ctx{}))
			continue
		}
		if m != nil {
			p.fail("mixed list items and map entries")
		}
		if list == nil {
			list = &ListAtom{}
		}
		list.Items = append(list.Items, p.word(modeData, ctx{}))
	}
	if m != nil {
		return m
	}
	if list == nil {
		return &ListAtom{}
	}
	return list
}

// mapKey consumes `key:` or `'key':` when followed by a blank.
func (p *parser) mapKey() (string, bool) {
	save := p.pos
	var key string
	if p.peek() == '\'' {
		key = p.rawString().(*StrAtom).Parts[0].Lit
	} else {
		for isKeyChar(p.peek()) {
			p.pos++
		}
		key = string(p.src[save:p.pos])
	}
	if key != "" && p.peek() == ':' && strings.ContainsRune(" \t\r\n]", p.at(1)) {
		p.pos++
		return key, true
	}
	p.pos = save
	return "", false
}
