package lang

import "slices"

// Variables are lexically scoped and resolved before running: a $name must
// be bound by an enclosing let (earlier in the same or an outer block), a fn
// or lambda param, or a catch. Callers' variables are never visible, and a
// fn body sees only variables bound before the fn definition. A fn is also
// bound as $name from its definition on, and builtins are values $map etc.
// unless a variable of that name is in scope.
//
// $_ is bound by a stage after |> that uses it: the stage runs once per
// value. It belongs to the innermost such stage, and a lambda or fn body
// does not see the $_ around it.

type vscope struct {
	name   string
	parent *vscope
	used   *bool // for _: set when a $_ resolves to it
	fence  bool  // a lambda or fn body: the $_ around it is not visible
}

func (s *vscope) has(name string) bool {
	for ; s != nil; s = s.parent {
		if s.name == name {
			return true
		}
	}
	return false
}

func (s *vscope) add(names ...string) *vscope {
	for _, n := range names {
		if n != "" {
			s = &vscope{name: n, parent: s}
		}
	}
	return s
}

func (c *checker) vblock(b *Block, sc *vscope) {
	for _, st := range b.Stmts {
		switch st := st.(type) {
		case *LetStmt:
			c.vexpr(st.Val, sc) // the value sees the outer binding of the name
			sc = sc.add(st.Name)
		case *FnStmt:
			d := st.Def
			c.vblock(d.Body, sc.fenced().add(d.Name).add(d.Params...).add(d.Rest))
			sc = sc.add(d.Name) // $name is the fn from here on, like a let
		case *ExprStmt:
			c.vexpr(st.X, sc)
		}
	}
}

func (c *checker) vexpr(e Expr, sc *vscope) {
	switch e := e.(type) {
	case *OrExpr:
		c.vexpr(e.L, sc)
		c.vexpr(e.R, sc)
	case *AndExpr:
		c.vexpr(e.L, sc)
		c.vexpr(e.R, sc)
	case *NotExpr:
		c.vexpr(e.X, sc)
	case *BinExpr:
		c.vexpr(e.L, sc)
		c.vexpr(e.R, sc)
	case *Pipeline:
		e.topic = make([]bool, len(e.Stages))
		for i, st := range e.Stages {
			if i > 0 && e.Ops[i-1] == "|>" {
				c.vexpr(st, &vscope{name: "_", parent: sc, used: &e.topic[i]})
				if e.topic[i] {
					e.Stages[i] = asValue(st)
				}
				continue
			}
			c.vexpr(st, sc)
		}
	case *Call:
		if e.HeadVar != nil {
			c.vref(e.HeadVar, sc)
		}
		if e.HeadAtom != nil {
			c.vatom(e.HeadAtom, sc)
		}
		for _, w := range e.Args {
			c.vword(w, sc)
		}
		for _, r := range e.Redirs {
			if r.Target != nil {
				c.vword(r.Target, sc)
			}
		}
	case *IfExpr:
		c.vexpr(e.Cond, sc)
		c.vblock(e.Then, sc)
		if e.Else != nil {
			c.vexpr(e.Else, sc)
		}
	case *TryExpr:
		c.vexpr(e.Body, sc)
		c.vblock(e.Handler, sc.add(e.Name))
	case *MatchExpr:
		c.vexpr(e.X, sc)
		for _, arm := range e.Arms {
			for _, v := range patVars(arm.Pat) {
				c.vref(v, sc) // a $v in a pattern is an outer variable
			}
			asc := sc.add(c.patBinds(arm.Pat)...)
			if arm.Guard != nil {
				c.vexpr(arm.Guard, asc)
			}
			c.vblock(arm.Body, asc)
		}
	case *BlockExpr:
		c.vblock(e.B, sc)
	case *WordExpr:
		c.vword(e.W, sc)
	}
}

func (c *checker) vword(w *Word, sc *vscope) {
	for _, a := range w.Parts {
		c.vatom(a, sc)
	}
}

func (c *checker) vatom(a Atom, sc *vscope) {
	switch a := a.(type) {
	case *VarRef:
		c.vref(a, sc)
	case *StrAtom:
		c.vparts(a.Parts, sc)
	case *MLAtom:
		for _, line := range a.Lines {
			c.vparts(line, sc)
		}
	case *SubAtom:
		c.vexpr(a.X, sc)
	case *GroupAtom:
		c.vexpr(a.X, sc)
		c.vindex(a.Index, sc)
	case *ListAtom:
		for _, w := range a.Items {
			c.vword(w, sc)
		}
	case *MapAtom:
		for _, w := range a.Vals {
			c.vword(w, sc)
		}
	case *LambdaAtom:
		c.vblock(a.Body, sc.fenced().add(a.Params...).add(a.Rest))
	case *SpreadAtom:
		c.vatom(a.X, sc)
	}
}

func (c *checker) vparts(parts []StrPart, sc *vscope) {
	for _, p := range parts {
		if p.Var != nil {
			c.vref(p.Var, sc)
		}
	}
}

func (c *checker) vindex(index []Index, sc *vscope) {
	for _, ix := range index {
		for _, a := range []*IndexArg{ix.Key, ix.Lo, ix.Hi} {
			switch {
			case a == nil:
			case a.Var != nil:
				c.vref(a.Var, sc)
			case a.X != nil:
				c.vexpr(a.X, sc)
			}
		}
	}
}

func (c *checker) vref(v *VarRef, sc *vscope) {
	if v.Env {
		return // env vars belong to the scope frame, not to lexical scope
	}
	c.vindex(v.Index, sc)
	if v.Name == "_" {
		c.vtopic(v, sc)
		return
	}
	switch {
	case sc.has(v.Name):
		return
	case v.Name == "it":
		c.errorf(v.Pos, "$it is only defined inside a block passed as a fn: { print $it }")
	case reservedVars[v.Name], v.Name == "args", builtinValue(v.Name) != nil:
		return
	default:
		c.errorf(v.Pos, "undefined variable $%s", v.Name)
	}
}

// asValue undoes the parser's `|> $f` and `|> ( )` calls in a stage that
// uses $_: $f is called so that it reads the values, but such a stage has no
// value input, so there the word is a value: |> $xs[$_], |> ($_ * 2).
func asValue(st Expr) Expr {
	call, ok := st.(*Call)
	if !ok || !call.piped {
		return st
	}
	if call.HeadVar != nil {
		return &WordExpr{W: &Word{Pos: call.P, Parts: []Atom{call.HeadVar}}}
	}
	if g, ok := call.HeadAtom.(*GroupAtom); ok {
		return &WordExpr{W: &Word{Pos: call.P, Parts: []Atom{g}}}
	}
	return st
}

func (s *vscope) fenced() *vscope { return &vscope{parent: s, fence: true} }

// vtopic resolves $_ to the innermost |> stage around it.
func (c *checker) vtopic(v *VarRef, sc *vscope) {
	fenced := false
	for s := sc; s != nil; s = s.parent {
		switch {
		case s.fence:
			fenced = true
		case s.name == "_" && fenced:
			c.errorf(v.Pos, "a { } block does not see the $_ of the stage around it; take the item as the block's arg: { |x| ... } or $it")
			return
		case s.name == "_":
			*s.used = true
			return
		}
	}
	c.errorf(v.Pos, "$_ is the item of a stage after |>: ... |> rm $_")
}

// patBinds is the names a pattern binds; binding one twice is an error.
func (c *checker) patBinds(p Pattern) []string {
	var names []string
	var walk func(Pattern)
	add := func(pos Pos, name string) {
		if slices.Contains(names, name) {
			c.errorf(pos, "$%s is bound twice in this pattern", name)
		}
		names = append(names, name)
	}
	walk = func(p Pattern) {
		switch p := p.(type) {
		case *BindPat:
			add(p.Pos, p.Name)
		case *ListPat:
			for _, x := range p.Items {
				walk(x)
			}
			if p.Rest != "" && p.Rest != "_" {
				add(p.Pos, p.Rest)
			}
		case *MapPat:
			for _, x := range p.Vals {
				walk(x)
			}
		}
	}
	walk(p)
	return names
}

// patVars is the $v patterns in p.
func patVars(p Pattern) []*VarRef {
	switch p := p.(type) {
	case *VarPat:
		return []*VarRef{p.V}
	case *ListPat:
		var vs []*VarRef
		for _, x := range p.Items {
			vs = append(vs, patVars(x)...)
		}
		return vs
	case *MapPat:
		var vs []*VarRef
		for _, x := range p.Vals {
			vs = append(vs, patVars(x)...)
		}
		return vs
	}
	return nil
}

// irrefutable: p matches every value.
func irrefutable(p Pattern) bool {
	switch p.(type) {
	case *AnyPat, *BindPat:
		return true
	}
	return false
}

func patPos(p Pattern) Pos {
	switch p := p.(type) {
	case *AnyPat:
		return p.Pos
	case *BindPat:
		return p.Pos
	case *LitPat:
		return p.Pos
	case *VarPat:
		return p.V.Pos
	case *ListPat:
		return p.Pos
	case *MapPat:
		return p.Pos
	}
	panic("unknown pattern")
}
