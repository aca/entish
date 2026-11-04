package lang

import "fmt"

// The checker binds every call to a fn, a builtin or an external and checks
// names, scopes and arity. Fns have no kinds: every call writes stdout and
// returns a value ($nil for externals), so the checks on values and bytes
// reject only what the source alone makes certain.

type fnScope struct {
	m      map[string]*FnDef
	parent *fnScope
}

func (s *fnScope) lookup(name string) *FnDef {
	for ; s != nil; s = s.parent {
		if d, ok := s.m[name]; ok {
			return d
		}
	}
	return nil
}

type checker struct {
	err *Error
}

func (c *checker) errorf(pos Pos, format string, args ...any) {
	if c.err == nil {
		c.err = &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
	}
}

// check resolves and checks prog. top and topVars hold fns and variables
// defined by earlier REPL inputs. script enables checks that only make sense in files.
func check(prog *Block, top map[string]*FnDef, topVars []string, script bool) error {
	c := &checker{}
	c.block(prog, &fnScope{m: top})
	if c.err != nil {
		return c.err
	}
	c.vblock(prog, (*vscope)(nil).add(topVars...))
	if c.err != nil {
		return c.err
	}
	c.checkBlock(prog, script)
	if c.err != nil {
		return c.err
	}
	return nil
}

// ---- resolution

func (c *checker) block(b *Block, parent *fnScope) {
	s := &fnScope{m: map[string]*FnDef{}, parent: parent}
	for _, d := range b.Fns {
		s.m[d.Name] = d
	}
	for _, st := range b.Stmts {
		switch st := st.(type) {
		case *LetStmt:
			c.expr(st.Val, s)
		case *FnStmt:
			c.block(st.Def.Body, s)
		case *ExprStmt:
			c.expr(st.X, s)
		}
	}
}

func (c *checker) expr(e Expr, s *fnScope) {
	switch e := e.(type) {
	case *OrExpr:
		c.expr(e.L, s)
		c.expr(e.R, s)
	case *AndExpr:
		c.expr(e.L, s)
		c.expr(e.R, s)
	case *NotExpr:
		c.expr(e.X, s)
	case *Pipeline:
		for _, st := range e.Stages {
			c.expr(st, s)
		}
	case *Call:
		c.call(e, s)
	case *BinExpr:
		c.expr(e.L, s)
		c.expr(e.R, s)
	case *IfExpr:
		c.expr(e.Cond, s)
		c.block(e.Then, s)
		if e.Else != nil {
			c.expr(e.Else, s)
		}
	case *TryExpr:
		c.expr(e.Body, s)
		c.block(e.Handler, s)
	case *MatchExpr:
		c.expr(e.X, s)
		for _, arm := range e.Arms {
			for _, v := range patVars(arm.Pat) {
				c.atom(v, s)
			}
			if arm.Guard != nil {
				c.expr(arm.Guard, s)
			}
			c.block(arm.Body, s)
		}
	case *BlockExpr:
		c.block(e.B, s)
	case *WordExpr:
		c.word(e.W, s)
	}
}

func (c *checker) call(e *Call, s *fnScope) {
	switch {
	case e.Caret: // ^name or ^$name
		e.target = tExternal
	case e.HeadVar != nil:
		e.target = tVar
	default:
		if d := s.lookup(e.Name); d != nil {
			e.target, e.fn = tFn, d
		} else if b, ok := builtins[e.Name]; ok {
			e.target, e.builtin = tBuiltin, b
			switch e.Name {
			case "call":
				c.callBuiltin(e)
			case "eval":
				if len(e.Args) == 0 {
					c.errorf(e.P, "eval takes its source as its first arg: eval 'rm ...$args' $f")
				}
			case "source":
				c.errorf(e.P, "source must be a statement at the top level of a file or input: source path")
			case "as_file":
				if len(e.Args) > 0 {
					c.errorf(e.P, "as_file reads stdin, not a value: cmd | as_file")
				}
			}
		} else {
			e.target = tExternal
		}
	}
	if e.HeadVar != nil {
		c.atom(e.HeadVar, s)
	}
	for _, w := range e.Args {
		c.word(w, s)
	}
	for _, r := range e.Redirs {
		if r.Target != nil {
			c.word(r.Target, s)
		}
	}
}

// callBuiltin rewrites `call $f` into a call of $f with no args, so it runs
// like `$f x` does.
func (c *checker) callBuiltin(e *Call) {
	var v *VarRef
	if len(e.Args) == 1 && len(e.Redirs) == 0 {
		v, _ = singleVar(e.Args[0])
	}
	if v == nil {
		c.errorf(e.P, "call takes one fn variable: call $f")
		return
	}
	e.Name, e.HeadVar, e.Args, e.target, e.builtin = "", v, nil, tVar, nil
}

func (c *checker) word(w *Word, s *fnScope) {
	for _, a := range w.Parts {
		c.atom(a, s)
	}
}

func (c *checker) atom(a Atom, s *fnScope) {
	switch a := a.(type) {
	case *SubAtom:
		c.expr(a.X, s)
	case *GroupAtom:
		c.expr(a.X, s)
	case *VarRef:
		for _, x := range a.indexExprs() {
			c.expr(x, s)
		}
	case *ListAtom:
		for _, w := range a.Items {
			c.word(w, s)
		}
	case *MapAtom:
		for _, w := range a.Vals {
			c.word(w, s)
		}
	case *LambdaAtom:
		c.block(a.Body, s)
	case *SpreadAtom:
		c.atom(a.X, s)
	}
}

// ---- values

// isExpr: e is a value expression, not a call.
func isExpr(e Expr) bool {
	switch e.(type) {
	case *WordExpr, *BinExpr:
		return true
	}
	return false
}

// endsInValue: e certainly ends in a value expression on some branch, so
// dropping its value is a mistake. A call may return $nil, so it never
// counts.
func endsInValue(e Expr) bool {
	switch e := e.(type) {
	case *WordExpr, *BinExpr:
		return true
	case *NotExpr:
		return isExpr(e.X)
	case *OrExpr:
		return endsInValue(e.R) // $verbose && print x is a statement
	case *AndExpr:
		return endsInValue(e.R)
	case *IfExpr:
		return blockEndsInValue(e.Then) || e.Else != nil && endsInValue(e.Else)
	case *TryExpr:
		return endsInValue(e.Body) || blockEndsInValue(e.Handler)
	case *MatchExpr:
		for _, arm := range e.Arms {
			if blockEndsInValue(arm.Body) {
				return true
			}
		}
	case *BlockExpr:
		return blockEndsInValue(e.B)
	}
	return false
}

func blockEndsInValue(b *Block) bool {
	st, ok := lastExpr(b)
	return ok && endsInValue(st)
}

// noValue names e when it certainly has no value: an external, or a
// builtin such as print.
func noValue(e Expr) (string, bool) {
	switch e := e.(type) {
	case *Call:
		switch e.target {
		case tExternal:
			return callName(e), true
		case tBuiltin:
			return e.Name, e.builtin.Void
		}
	case *Pipeline:
		return noValue(e.Stages[len(e.Stages)-1])
	}
	return "", false
}

func (c *checker) needValue(e Expr) {
	n, ok := noValue(e)
	if !ok {
		return
	}
	if call, isCall := e.(*Call); isCall && call.target == tBuiltin {
		c.errorf(e.pos(), "%s has no value", n)
		return
	}
	c.errorf(e.pos(), "%s has no value: capture its output with $( ) or read it with | lines", n)
}

// ---- checks

func callName(e Expr) string {
	switch e := e.(type) {
	case *Call:
		if e.HeadVar != nil {
			return "$" + e.HeadVar.Name
		}
		return e.Name
	case *Pipeline:
		return callName(e.Stages[len(e.Stages)-1])
	case *OrExpr:
		return callName(e.R)
	case *AndExpr:
		return callName(e.R)
	case *NotExpr:
		return callName(e.X)
	}
	return "this"
}

func (c *checker) checkBlock(b *Block, top bool) {
	for i, st := range b.Stmts {
		switch st := st.(type) {
		case *LetStmt:
			c.value(st.Val)
		case *FnStmt:
			c.blockNeedElse(st.Def.Body) // the last statement is the return value
			c.checkBlock(st.Def.Body, false)
		case *ExprStmt:
			c.checkExpr(st.X)
			if (i < len(b.Stmts)-1 || top) && endsInValue(st.X) {
				c.unused(st.X)
			}
		}
	}
}

// unused reports a value statement that is not the last of its block, or the
// last of a script: its value would be dropped silently.
func (c *checker) unused(e Expr) {
	if we, ok := e.(*WordExpr); ok {
		if v, ok := singleVar(we.W); ok {
			c.errorf(e.pos(), "unused value: $%s is not called; to call a fn value: call $%s", v.Name, v.Name)
			return
		}
	}
	c.errorf(e.pos(), "unused value")
}

// value checks e where a value is required.
func (c *checker) value(e Expr) {
	c.needValue(e)
	c.needElse(e)
	c.checkExpr(e)
}

// needElse: an if whose value is used must have an else when its
// then-branch ends in a value expression, or a false condition would
// silently give $nil. A then-branch ending in a call (print, fail, a fn)
// may go without one.
func (c *checker) needElse(e Expr) {
	switch e := e.(type) {
	case *IfExpr:
		if e.Else == nil {
			if blockEndsInValue(e.Then) {
				c.errorf(e.P, "this if is used as a value, so it needs an else")
			}
			return
		}
		c.blockNeedElse(e.Then)
		c.needElse(e.Else)
	case *TryExpr:
		if b, ok := e.Body.(*BlockExpr); ok {
			c.blockNeedElse(b.B)
		}
		c.blockNeedElse(e.Handler)
	case *MatchExpr:
		for _, arm := range e.Arms {
			c.blockNeedElse(arm.Body)
		}
	case *BlockExpr:
		c.blockNeedElse(e.B)
	}
}

func (c *checker) blockNeedElse(b *Block) {
	if st, ok := lastExpr(b); ok {
		c.needElse(st)
	}
}

func lastExpr(b *Block) (Expr, bool) {
	if len(b.Stmts) == 0 {
		return nil, false
	}
	st, ok := b.Stmts[len(b.Stmts)-1].(*ExprStmt)
	if !ok {
		return nil, false
	}
	return st.X, true
}

func (c *checker) checkExpr(e Expr) {
	switch e := e.(type) {
	case *OrExpr:
		c.checkExpr(e.L)
		c.checkExpr(e.R)
	case *AndExpr:
		c.checkExpr(e.L)
		c.checkExpr(e.R)
	case *NotExpr:
		c.checkExpr(e.X)
	case *Pipeline:
		for i, st := range e.Stages {
			if i > 0 {
				c.stageAfter(e.Ops[i-1], st)
				if call, ok := st.(*Call); ok && e.Ops[i-1] == "|>" {
					call.piped = true
				}
			}
			if i < len(e.Ops) && e.Ops[i] == "|" {
				c.stageBefore(st)
			}
			if i < len(e.Ops) && e.Ops[i] == "|>" {
				c.needValue(st)
				c.needElse(st)
			}
			c.checkExpr(st)
		}
	case *Call:
		c.arity(e)
		if e.HeadVar != nil {
			c.checkAtom(e.HeadVar)
		}
		for _, w := range e.Args {
			c.checkWord(w)
		}
		for _, r := range e.Redirs {
			if r.Target == nil {
				continue
			}
			if g, ok := r.Target.Parts[0].(*GroupAtom); ok && r.Op == "<" && len(r.Target.Parts) == 1 {
				if n, ok := noValue(g.X); ok { // bash's <(cmd)
					c.errorf(g.X.pos(), "%s has no value; to pass its output as a file arg: (%s ... | as_file)", n, n)
				}
			}
			c.checkWord(r.Target)
		}
	case *BinExpr:
		c.checkExpr(e.L)
		c.checkExpr(e.R)
	case *IfExpr:
		c.checkExpr(e.Cond)
		c.checkBlock(e.Then, false)
		if e.Else != nil {
			c.checkExpr(e.Else)
		}
	case *TryExpr:
		c.checkExpr(e.Body)
		c.checkBlock(e.Handler, false)
	case *MatchExpr:
		c.value(e.X)
		for i, arm := range e.Arms {
			for _, v := range patVars(arm.Pat) {
				c.checkAtom(v)
			}
			if arm.Guard != nil {
				c.checkExpr(arm.Guard)
			}
			c.checkBlock(arm.Body, false)
			if i < len(e.Arms)-1 && arm.Guard == nil && irrefutable(arm.Pat) {
				c.errorf(patPos(e.Arms[i+1].Pat), "unreachable arm: the arm before it matches everything")
			}
		}
	case *BlockExpr:
		c.checkBlock(e.B, false)
	case *WordExpr:
		c.checkWord(e.W)
	}
}

func (c *checker) stageAfter(op string, st Expr) {
	call, ok := st.(*Call)
	if !ok {
		c.errorf(st.pos(), "the right side of %s must be a call", op)
		return
	}
	// |> passes a value as an arg, which an external cannot take; | passes
	// stdin, which only externals, fns and converters read.
	var b *Builtin
	if call.target == tBuiltin {
		b = call.builtin
	}
	switch {
	case op == "|>" && call.target == tExternal:
		n := callName(call)
		c.errorf(call.P, "%s reads stdin, not a value: print $x | %s", n, n)
	case op == "|>" && b != nil && b.Name == "as_file": // values become bytes only through print
		c.errorf(call.P, "as_file reads stdin, not a value: print $x | as_file")
	case op == "|" && b != nil && b.Stdin:
		call.stdin = true
	case op == "|" && b != nil && b.Data:
		c.errorf(call.P, "%s takes a value, not stdin: read stdin with lines first, e.g. ls | lines |> %s", call.Name, call.Name)
	}
}

// stageBefore: the left side of | must write stdout. A value expression or
// a builtin that only returns a value writes nothing; values are never
// converted to bytes implicitly, so print them.
func (c *checker) stageBefore(st Expr) {
	if call, ok := st.(*Call); ok && !(call.target == tBuiltin && call.builtin.Value) {
		return
	}
	c.errorf(st.pos(), "a value cannot be piped into |; print it: print $x | cmd")
}

func (c *checker) checkWord(w *Word) {
	for _, a := range w.Parts {
		c.checkAtom(a)
	}
}

func (c *checker) checkAtom(a Atom) {
	switch a := a.(type) {
	case *SubAtom:
		c.checkExpr(a.X)
	case *GroupAtom:
		c.value(a.X)
	case *VarRef:
		for _, x := range a.indexExprs() {
			c.value(x)
		}
	case *ListAtom:
		for _, w := range a.Items {
			c.checkWord(w)
		}
	case *MapAtom:
		for _, w := range a.Vals {
			c.checkWord(w)
		}
	case *LambdaAtom:
		c.blockNeedElse(a.Body)
		c.checkBlock(a.Body, false)
	case *SpreadAtom:
		c.checkAtom(a.X)
	}
}

// arity checks the arg count of calls to named fns and builtins. Args that
// expand to an unknown count (a glob, ...$xs) defer the check to run time.
func (c *checker) arity(e *Call) {
	var name string
	var min, max int
	switch e.target {
	case tFn:
		name, min, max = "fn "+e.Name, len(e.fn.Params), len(e.fn.Params)
		if e.fn.Rest != "" {
			max = -1
		}
	case tBuiltin:
		name, min, max = e.Name, e.builtin.Min, e.builtin.Max
	default:
		return
	}
	n := len(e.Args)
	for _, w := range e.Args {
		if isGlobWord(w) {
			return
		}
		if len(w.Parts) == 1 {
			if _, ok := w.Parts[0].(*SpreadAtom); ok {
				return
			}
		}
	}
	if e.piped || e.stdin {
		n++
	}
	if n >= min && (max < 0 || n <= max) {
		return
	}
	want := fmt.Sprintf("%d", min)
	switch {
	case max < 0:
		want = fmt.Sprintf("at least %d", min)
	case max != min:
		want = fmt.Sprintf("%d to %d", min, max)
	}
	got := fmt.Sprintf("got %d", n)
	switch {
	case e.piped:
		got += " (including the value from |>)"
	case e.stdin:
		got += " (including stdin)"
	}
	c.errorf(e.P, "%s takes %s args, %s", name, want, got)
}
