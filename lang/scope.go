package lang

// Variables are lexically scoped and resolved before running: a $name must
// be bound by an enclosing let (earlier in the same or an outer block), a fn
// or lambda param, or a catch. Callers' variables are never visible, and a
// fn body sees only variables bound before the fn definition. A fn is also
// bound as $name from its definition on, and builtins are values $map etc.
// unless a variable of that name is in scope.

type vscope struct {
	name   string
	parent *vscope
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
			c.vblock(d.Body, sc.add(d.Name).add(d.Params...).add(d.Rest))
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
		for _, st := range e.Stages {
			c.vexpr(st, sc)
		}
	case *Call:
		if e.HeadVar != nil {
			c.vref(e.HeadVar, sc)
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
	case *ListAtom:
		for _, w := range a.Items {
			c.vword(w, sc)
		}
	case *MapAtom:
		for _, w := range a.Vals {
			c.vword(w, sc)
		}
	case *LambdaAtom:
		c.vblock(a.Body, sc.add(a.Params...).add(a.Rest))
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

func (c *checker) vref(v *VarRef, sc *vscope) {
	if v.Env {
		return // env vars belong to the scope frame, not to lexical scope
	}
	for _, ix := range v.Index {
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
