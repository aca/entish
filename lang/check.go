package lang

import (
	"fmt"
	"strings"
)

// The checker binds every call to a fn, a builtin or an external and checks
// names, scopes and arity. Fns have no kinds: every call emits an output
// stream and a value stream (empty for externals), so the checks on values
// and bytes reject only what the source alone makes certain.

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
	err    *Error
	vstage *Call // the call being checked is a stage after |> without $_
}

func (c *checker) errorf(pos Pos, format string, args ...any) {
	if c.err == nil {
		c.err = &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
	}
}

// check resolves and checks prog. top and topVars hold fns and variables
// defined by earlier REPL inputs.
func check(prog *Block, top map[string]*FnDef, topVars []string) error {
	c := &checker{}
	c.block(prog, &fnScope{m: top})
	if c.err != nil {
		return c.err
	}
	c.vblock(prog, (*vscope)(nil).add(topVars...))
	if c.err != nil {
		return c.err
	}
	c.checkBlock(prog)
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
	case e.HeadVar != nil, e.HeadAtom != nil:
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
	if e.HeadAtom != nil {
		c.atom(e.HeadAtom, s)
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

// callBuiltin rewrites `call $f`, `call { ... }` and `call ( ... )` into a
// call of the fn value with no args, so it runs like `$f x` does.
func (c *checker) callBuiltin(e *Call) {
	var v *VarRef
	var a Atom
	if len(e.Args) == 1 && len(e.Redirs) == 0 {
		v, _ = singleVar(e.Args[0])
		a, _ = singleFnAtom(e.Args[0])
	}
	if v == nil && a == nil {
		c.errorf(e.P, "call takes one fn value: call $f, call { ... }, call (f x)")
		return
	}
	e.Name, e.HeadVar, e.HeadAtom, e.Args, e.target, e.builtin = "", v, a, nil, tVar, nil
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
		for _, x := range indexExprs(a.Index) {
			c.expr(x, s)
		}
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

// noValue names e when it certainly emits no values: an external, or a
// builtin such as print.
func noValue(e Expr) (string, bool) {
	switch e := e.(type) {
	case *Call:
		switch e.target {
		case tExternal:
			return callName(e), true
		case tBuiltin:
			return e.Name, e.builtin.Fn != nil && e.builtin.NoValues
		}
	case *Pipeline:
		return noValue(e.Stages[len(e.Stages)-1])
	}
	return "", false
}

// endsInValue: e certainly ends in a value expression on some branch.
func endsInValue(e Expr) bool {
	switch e := e.(type) {
	case *WordExpr, *BinExpr:
		return true
	case *AndExpr, *OrExpr, *NotExpr:
		return !isCommand(e)
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
	if len(b.Stmts) == 0 {
		return false
	}
	st, ok := b.Stmts[len(b.Stmts)-1].(*ExprStmt)
	return ok && endsInValue(st.X)
}

// condOperand checks e where its status is tested. A fn whose body ends in
// a value always succeeds as a command, so testing its status is a mistake:
// `if big 1` for `fn big n { $n > 9 }`.
func (c *checker) condOperand(e Expr) {
	switch e := e.(type) {
	case *Call:
		if e.target == tFn && blockEndsInValue(e.fn.Body) {
			c.errorf(e.P, "%s ends in a value, so as a command it always succeeds; to test its value: (%s ...)", e.Name, e.Name)
		}
	case *AndExpr:
		c.condOperand(e.L)
		c.condOperand(e.R)
	case *OrExpr:
		c.condOperand(e.L)
		c.condOperand(e.R)
	case *NotExpr:
		c.condOperand(e.X)
	}
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
		if _, ok := e.HeadAtom.(*LambdaAtom); ok {
			return "{ }"
		}
		if e.HeadAtom != nil {
			return "( )"
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

func (c *checker) checkBlock(b *Block) {
	for _, st := range b.Stmts {
		switch st := st.(type) {
		case *LetStmt:
			c.value(st.Val)
		case *FnStmt:
			c.checkBlock(st.Def.Body)
		case *ExprStmt:
			c.checkExpr(st.X)
		}
	}
}

// value checks e where one value is required.
func (c *checker) value(e Expr) {
	if we, ok := e.(*WordExpr); ok && isGlobWord(we.W) {
		c.errorf(e.pos(), "a glob gives several paths, not one value; to collect them into a list: [*.go]")
	}
	c.needValue(e)
	c.checkExpr(e)
}

func (c *checker) checkExpr(e Expr) {
	switch e := e.(type) {
	case *OrExpr:
		c.condOperand(e.L)
		c.checkExpr(e.L)
		c.checkExpr(e.R)
	case *AndExpr:
		c.condOperand(e.L)
		c.checkExpr(e.L)
		c.checkExpr(e.R)
	case *NotExpr:
		c.condOperand(e.X)
		c.checkExpr(e.X)
	case *Pipeline:
		for i, st := range e.Stages {
			if i > 0 && !e.topicAt(i) {
				c.stageAfter(e.Ops[i-1], st)
			}
			if i < len(e.Ops) {
				c.link(st, e.Ops[i], e.Stages[i+1], e.topicAt(i+1))
			}
			if call, ok := st.(*Call); ok && i > 0 && e.Ops[i-1] == "|>" && !e.topicAt(i) {
				c.vstage = call
			}
			c.checkExpr(st)
		}
	case *Call:
		c.arity(e)
		if e.HeadVar != nil {
			c.checkAtom(e.HeadVar)
		}
		if e.HeadAtom != nil {
			c.checkAtom(e.HeadAtom)
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
		c.condOperand(e.Cond)
		c.checkExpr(e.Cond)
		c.checkBlock(e.Then)
		if e.Else != nil {
			c.checkExpr(e.Else)
		}
	case *TryExpr:
		c.checkExpr(e.Body)
		c.checkBlock(e.Handler)
	case *MatchExpr:
		c.value(e.X)
		for i, arm := range e.Arms {
			for _, v := range patVars(arm.Pat) {
				c.checkAtom(v)
			}
			if arm.Guard != nil {
				c.condOperand(arm.Guard)
				c.checkExpr(arm.Guard)
			}
			c.checkBlock(arm.Body)
			if i < len(e.Arms)-1 && arm.Guard == nil && irrefutable(arm.Pat) {
				c.errorf(patPos(e.Arms[i+1].Pat), "unreachable arm: the arm before it matches everything")
			}
		}
	case *BlockExpr:
		c.checkBlock(e.B)
	case *WordExpr:
		c.checkWord(e.W)
	}
}

func (c *checker) stageAfter(op string, st Expr) {
	if _, ok := st.(*Call); !ok {
		if op == "|>" {
			c.errorf(st.pos(), "the right side of |> must be a call, or use $_, the item: |> $_ * 2")
			return
		}
		c.errorf(st.pos(), "the right side of %s must be a call", op)
	}
}

// The four checks of the pipe operators: | connects output to stdin, |>
// values to the value input. Each rejects a side only when the source makes
// certain it lacks the stream; a fn may have both, so it always passes.

// noOutput: the left of | writes nothing: a value expression, or a builtin
// that writes no output.
func noOutput(e Expr) bool {
	if isExpr(e) {
		return true
	}
	call, ok := e.(*Call)
	return ok && call.target == tBuiltin && call.builtin.Fn != nil && !call.builtin.Out
}

// noStdin: the right of | reads no stdin: a builtin that is not a converter.
// call, eval and source run code, which reads stdin like a fn body.
func noStdin(call *Call) bool {
	return call.target == tBuiltin && !call.builtin.Stdin && call.builtin.Fn != nil
}

// noVIn: the right of |> reads no values: an external, or a builtin that
// takes its data as args.
func noVIn(call *Call) bool {
	return call.target == tExternal || call.target == tBuiltin && !call.builtin.VIn && call.builtin.Fn != nil
}

// link checks the operator op between stages l and r. A failing side has two
// fixes, the other operator and an explicit conversion; the hint lists those
// that pass the same checks, so it never suggests code that is rejected.
// topic: r uses $_, so it takes the values one at a time instead of reading
// its value input.
func (c *checker) link(l Expr, op string, r Expr, topic bool) {
	L := "$x"
	if !isExpr(l) {
		L = callName(l)
	}
	if topic {
		if _, ok := noValue(l); ok {
			c.errorf(l.pos(), "%s has no value; make its output values: %s | lines |> ...", L, L)
		}
		if rc, ok := r.(*Call); ok && rc.target == tBuiltin && rc.builtin.VIn && len(rc.Args) == rc.builtin.Min {
			c.errorf(rc.P, "%s reads the value input, but a stage with $_ runs once per value and gets none", rc.Name)
		}
		return
	}
	rc, ok := r.(*Call)
	if !ok {
		return // stageAfter reports it
	}
	R := callName(rc)
	each := R + " $_" // R with the item as its last arg
	if len(rc.Args) > 0 {
		each = R + " ... $_"
	}
	var fixes []string
	add := func(ok bool, fix string, args ...any) {
		if ok {
			fixes = append(fixes, fmt.Sprintf(fix, args...))
		}
	}
	hint := func() string {
		if len(fixes) == 0 {
			return ""
		}
		return "; " + strings.Join(fixes, ", or ")
	}
	_, lNoValue := noValue(l)
	switch {
	case op == "|" && noOutput(l):
		add(!noVIn(rc), "pass it with |>: %s |> %s", L, R)
		add(!noStdin(rc), "print it: %s |> print | %s", L, R)
		add(noVIn(rc) && rc.target == tBuiltin, "pass it as an arg: %s %s", R, L)
		c.errorf(l.pos(), "a value cannot be piped into |%s", hint())
	case op == "|" && noStdin(rc):
		add(!lNoValue && !noVIn(rc), "pass %s's values with |>: %s |> %s", L, L, R)
		add(!noVIn(rc), "read its output as lines: %s | lines |> %s", L, R)
		add(noVIn(rc), "it takes its data as an arg: %s $x", R)
		c.errorf(rc.P, "%s does not read stdin%s", R, hint())
	case op == "|>" && lNoValue:
		add(!noStdin(rc), "pipe its output with |: %s | %s", L, R)
		conv := rc.target == tBuiltin && rc.builtin.Stdin // already turns output into values
		add(!noVIn(rc) && !conv, "make its output values: %s | lines |> %s", L, R)
		c.errorf(l.pos(), "%s has no value%s", L, hint())
	case op == "|>" && noVIn(rc):
		if rc.target == tExternal || rc.builtin.Stdin {
			add(!noOutput(l), "pipe %s's output with |: %s | %s", L, L, R)
			add(rc.target == tExternal || rc.builtin.Max > 0, "pass each value as an arg with $_: |> %s", each)
			add(true, "print the values: %s |> print | %s", L, R)
			c.errorf(rc.P, "%s reads stdin, not values%s", R, hint())
			return
		}
		c.errorf(rc.P, "%s takes its data as an arg, not values: %s $x, or for each value: |> %s", R, R, each)
	}
}

// exprName names e in a message.
func exprName(e Expr) string {
	if isExpr(e) {
		return "a value"
	}
	return callName(e)
}

func (c *checker) checkWord(w *Word) {
	for _, a := range w.Parts {
		c.checkAtom(a)
	}
}

func (c *checker) checkAtom(a Atom) {
	switch a := a.(type) {
	case *SubAtom:
		last := a.X
		if pl, ok := a.X.(*Pipeline); ok {
			last = pl.Stages[len(pl.Stages)-1]
		}
		if noOutput(last) { // $( ) would silently be ''
			c.errorf(last.pos(), "%s writes no output, and $( ) captures output; for a value use ( )", exprName(last))
		}
		c.checkExpr(a.X)
	case *GroupAtom:
		c.value(a.X)
		for _, x := range indexExprs(a.Index) {
			c.value(x)
		}
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
		c.checkBlock(a.Body)
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
	case tVar:
		la, ok := e.HeadAtom.(*LambdaAtom)
		if !ok {
			return
		}
		name, min, max = "this fn", len(la.Params), len(la.Params)
		if la.Rest != "" || la.anyArgs() {
			max = -1
		}
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
	if la, ok := e.HeadAtom.(*LambdaAtom); ok && la.usesIt && n == 0 {
		got += ": $it is the item of each: |> each { ... }; after |> a fn reads the values as its value input"
	} else if e == c.vstage && n < min {
		got += fmt.Sprintf("; |> feeds the value input, not args; pass each value as an arg with $_: |> %s $_", callName(e))
	}
	c.errorf(e.P, "%s takes %s args, %s", name, want, got)
}
