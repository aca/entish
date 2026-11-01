package lang

import (
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strings"
)

var reservedVars = map[string]bool{"true": true, "false": true, "nil": true, "it": true}

func ratInt(n int) *big.Rat { return new(big.Rat).SetInt64(int64(n)) }

// execBlock runs b. In cond mode its last statement reports a failing
// command as false instead of raising (used for fns in conditions).
func (in *Interp) execBlock(b *Block, env *Env, io ioCtx, cond bool) (Value, error) {
	act := env
	if len(b.Fns) > 0 {
		act = &Env{parent: env, fns: map[string]*Closure{}}
	}
	cur := act
	var last Value
	for i, st := range b.Stmts {
		if in.intr.Swap(false) {
			return nil, interrupted(b.Pos)
		}
		v, next, err := in.execStmt(st, cur, act, io, cond && i == len(b.Stmts)-1)
		if err != nil {
			return nil, err
		}
		cur, last = next, v
	}
	return last, nil
}

func (in *Interp) execStmt(st Stmt, env, act *Env, io ioCtx, cond bool) (Value, *Env, error) {
	switch st := st.(type) {
	case *LetStmt:
		v, err := in.eval(st.Val, env, io, false)
		if err != nil {
			return nil, env, err
		}
		return nil, &Env{parent: env, name: st.Name, val: v}, nil
	case *FnStmt:
		// The fn is also the value $name from here on, like a let; its own
		// body sees it too, so $name recurses.
		d := st.Def
		c := &Closure{Name: d.Name, Params: d.Params, Rest: d.Rest, Body: d.Body}
		node := &Env{parent: env, name: d.Name, val: c}
		c.Env = node
		act.fns[d.Name] = c
		return nil, node, nil
	case *ExprStmt:
		v, err := in.eval(st.X, env, io, cond)
		return v, env, err
	}
	panic("unknown statement")
}

// eval evaluates e. Commands raise on failure; in cond mode they report
// success as a bool instead.
func (in *Interp) eval(e Expr, env *Env, io ioCtx, cond bool) (Value, error) {
	switch e := e.(type) {
	case *WordExpr:
		return in.wordValue(e.W, env, io)
	case *BinExpr:
		return in.binary(e, env, io)
	case *BlockExpr:
		return in.execBlock(e.B, env, io, cond)
	case *IfExpr:
		c, err := in.eval(e.Cond, env, io, true)
		if err != nil {
			return nil, err
		}
		b, err := truth(e.Cond, c, "if")
		if err != nil {
			return nil, err
		}
		switch {
		case b:
			return in.execBlock(e.Then, env, io, cond)
		case e.Else != nil:
			return in.eval(e.Else, env, io, cond)
		case cond:
			return true, nil
		}
		return nil, nil
	case *TryExpr:
		var v Value
		var err error
		switch body := e.Body.(type) {
		case *BlockExpr:
			v, err = in.execBlock(body.B, env, io, cond)
		case *Call:
			var f Value
			if f, err = in.lookupVar(body.HeadVar, env, io); err == nil {
				v, err = in.callValue(f, nil, io, cond, body.P)
			}
		}
		var r *Raise
		if errors.As(err, &r) {
			return in.execBlock(e.Handler, &Env{parent: env, name: e.Name, val: r.Val}, io, cond)
		}
		return v, err
	case *OrExpr:
		l, err := in.eval(e.L, env, io, true)
		if err != nil {
			return nil, err
		}
		b, err := truth(e.L, l, "||")
		if err != nil {
			return nil, err
		}
		if !b {
			return in.eval(e.R, env, io, cond)
		}
		if cond {
			return true, nil
		}
		return l, nil
	case *AndExpr:
		l, err := in.eval(e.L, env, io, cond)
		if err != nil {
			return nil, err // outside a condition a failing command raises
		}
		b, err := truth(e.L, l, "&&")
		if err != nil {
			return nil, err
		}
		if !b {
			return false, nil
		}
		return in.eval(e.R, env, io, cond)
	case *NotExpr:
		v, err := in.eval(e.X, env, io, true)
		if err != nil {
			return nil, err
		}
		b, err := truth(e.X, v, "!")
		if err != nil {
			return nil, err
		}
		if !cond && v == nil && isCall(e.X) { // !cmd as a statement asserts that cmd fails
			return nil, raisef(e.P, "'!"+callName(e.X)+"' failed: the command succeeded")
		}
		return !b, nil
	case *Pipeline:
		return in.pipeline(e.Stages, e.Ops, env, io, cond)
	case *Call:
		return in.pipeline([]Expr{e}, nil, env, io, cond)
	}
	panic(fmt.Sprintf("unknown expression %T", e))
}

// isCall: e may give the $nil of a command that succeeded.
func isCall(e Expr) bool {
	switch e := e.(type) {
	case *Call, *Pipeline:
		return true
	case *AndExpr:
		return isCall(e.L) || isCall(e.R)
	case *OrExpr:
		return isCall(e.L) || isCall(e.R)
	}
	return false
}

// truth is what an operand of if, ||, && and ! stands for. A call is true
// when it succeeds and returns $nil, as a command does, or $true; it is
// false when it fails (in a condition) or returns $false. Anything else
// must be a bool: there is no truthiness.
func truth(e Expr, v Value, what string) (bool, error) {
	if v == nil && isCall(e) {
		return true, nil
	}
	return wantBool(v, e.pos(), what)
}

func wantBool(v Value, pos Pos, what string) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, raisef(pos, fmt.Sprintf("%s needs a bool, got %s", what, typeName(v)))
	}
	return b, nil
}

func (in *Interp) binary(e *BinExpr, env *Env, io ioCtx) (Value, error) {
	l, err := in.eval(e.L, env, io, false)
	if err != nil {
		return nil, err
	}
	r, err := in.eval(e.R, env, io, false)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case "==", "!=":
		eq, err := equal(l, r)
		if err != nil {
			return nil, raisef(e.P, err.Error())
		}
		return eq == (e.Op == "=="), nil
	case "<", ">", "<=", ">=":
		var c int
		switch l := l.(type) {
		case *Num:
			rn, ok := r.(*Num)
			if !ok {
				return nil, raisef(e.P, "cannot compare num with "+typeName(r))
			}
			c = l.R.Cmp(rn.R)
		case string:
			rs, ok := r.(string)
			if !ok {
				return nil, raisef(e.P, "cannot compare str with "+typeName(r))
			}
			c = strings.Compare(l, rs)
		default:
			return nil, raisef(e.P, "cannot order "+typeName(l))
		}
		switch e.Op {
		case "<":
			return c < 0, nil
		case ">":
			return c > 0, nil
		case "<=":
			return c <= 0, nil
		}
		return c >= 0, nil
	}
	ln, lok := l.(*Num)
	rn, rok := r.(*Num)
	if !lok || !rok {
		return nil, raisef(e.P, fmt.Sprintf("%s needs numbers, got %s and %s", e.Op, typeName(l), typeName(r)))
	}
	a, b := ln.R, rn.R
	z := new(big.Rat)
	switch e.Op {
	case "+":
		z.Add(a, b)
	case "-":
		z.Sub(a, b)
	case "*":
		z.Mul(a, b)
	case "/", "%":
		if b.Sign() == 0 {
			return nil, raisef(e.P, "division by zero")
		}
		z.Quo(a, b)
		if e.Op == "%" {
			fl := new(big.Int).Div(z.Num(), z.Denom()) // Euclidean: floor for a positive denominator
			z.Sub(a, new(big.Rat).Mul(b, new(big.Rat).SetInt(fl)))
		}
	case "**":
		if !b.IsInt() || !b.Num().IsInt64() {
			return nil, raisef(e.P, "** needs an integer exponent")
		}
		n := b.Num().Int64()
		neg := n < 0
		if neg {
			if a.Sign() == 0 {
				return nil, raisef(e.P, "division by zero")
			}
			n = -n
		}
		num := new(big.Int).Exp(a.Num(), big.NewInt(n), nil)
		den := new(big.Int).Exp(a.Denom(), big.NewInt(n), nil)
		z.SetFrac(num, den)
		if neg {
			z.Inv(z)
		}
	}
	return &Num{R: z}, nil
}

// ---- words

func isGlobWord(w *Word) bool {
	for _, a := range w.Parts {
		if l, ok := a.(*LitAtom); ok && l.Glob {
			return true
		}
	}
	return false
}

// wordValues is what a word contributes to args or list items: a spread or
// a glob may give several values.
func (in *Interp) wordValues(w *Word, env *Env, io ioCtx) ([]Value, error) {
	if len(w.Parts) == 1 {
		if sp, ok := w.Parts[0].(*SpreadAtom); ok {
			v, err := in.atomValue(sp.X, env, io)
			if err != nil {
				return nil, err
			}
			l, ok := v.(*List)
			if !ok {
				return nil, raisef(w.Pos, "... needs a list, got "+typeName(v))
			}
			return l.Items, nil
		}
	}
	if isGlobWord(w) {
		paths, err := in.glob(w, env, io)
		if err != nil {
			return nil, err
		}
		vals := make([]Value, len(paths))
		for i, p := range paths {
			vals[i] = p
		}
		return vals, nil
	}
	v, err := in.wordValue(w, env, io)
	return []Value{v}, err
}

// wordValue is a word used as one value; a glob is a list.
func (in *Interp) wordValue(w *Word, env *Env, io ioCtx) (Value, error) {
	if isGlobWord(w) {
		vals, err := in.wordValues(w, env, io)
		return &List{Items: vals}, err
	}
	if len(w.Parts) == 1 {
		if _, ok := w.Parts[0].(*SpreadAtom); ok {
			return nil, raisef(w.Pos, "... is only allowed in args and lists")
		}
		return in.atomValue(w.Parts[0], env, io)
	}
	var b strings.Builder
	for _, a := range w.Parts {
		v, err := in.atomValue(a, env, io)
		if err != nil {
			return nil, err
		}
		s, err := concat(v)
		if err != nil {
			return nil, raisef(w.Pos, err.Error())
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

func (in *Interp) atomValue(a Atom, env *Env, io ioCtx) (Value, error) {
	switch a := a.(type) {
	case *LitAtom:
		return a.S, nil
	case *NumAtom:
		return a.N, nil
	case *HomeAtom:
		home, err := in.home()
		if err != nil {
			return nil, raisef(a.Pos, err.Error())
		}
		return home, nil
	case *StrAtom:
		s, err := in.strParts(a.Parts, env, io)
		if err != nil {
			return nil, err
		}
		return s, nil
	case *MLAtom:
		var b strings.Builder
		for _, line := range a.Lines {
			s, err := in.strParts(line, env, io)
			if err != nil {
				return nil, err
			}
			b.WriteString(s)
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	case *VarRef:
		return in.lookupVar(a, env, io)
	case *SubAtom:
		return in.capture(a.X, env, io)
	case *GroupAtom:
		return in.eval(a.X, env, io, false)
	case *ListAtom:
		l := &List{}
		for _, w := range a.Items {
			vals, err := in.wordValues(w, env, io)
			if err != nil {
				return nil, err
			}
			l.Items = append(l.Items, vals...)
		}
		return l, nil
	case *MapAtom:
		m := newMap()
		for i, k := range a.Keys {
			v, err := in.wordValue(a.Vals[i], env, io)
			if err != nil {
				return nil, err
			}
			m.set(k, v)
		}
		return m, nil
	case *LambdaAtom:
		return &Closure{Params: a.Params, Rest: a.Rest, Body: a.Body, Env: env}, nil
	}
	panic(fmt.Sprintf("unknown atom %T", a))
}

func (in *Interp) strParts(parts []StrPart, env *Env, io ioCtx) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		if p.Var == nil {
			b.WriteString(p.Lit)
			continue
		}
		v, err := in.lookupVar(p.Var, env, io)
		if err != nil {
			return "", err
		}
		s, err := concat(v)
		if err != nil {
			return "", raisef(p.Var.Pos, "$"+p.Var.Name+": "+err.Error())
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

func (in *Interp) lookupVar(v *VarRef, env *Env, io ioCtx) (Value, error) {
	var val Value
	switch {
	case v.Env:
		if s, ok := in.lookupEnv(v.Name); ok {
			val = s
		}
	case v.Name == "true":
		val = true
	case v.Name == "false":
		val = false
	case v.Name == "nil":
		val = nil
	default:
		found := false
		for e := env; e != nil; e = e.parent {
			if e.fns == nil && e.name == v.Name {
				val, found = e.val, true
				break
			}
		}
		switch {
		case !found && v.Name == "args":
			val = in.args // the script's argv unless a variable named args is in scope
		case !found && builtinValue(v.Name) != nil:
			val = builtinValue(v.Name)
		case !found:
			return nil, raisef(v.Pos, "undefined variable $"+v.Name)
		}
	}
	for _, ix := range v.Index {
		var err error
		if ix.Key != nil {
			var key Value
			if key, err = in.indexArg(ix.Key, val, env, io); err == nil {
				val, err = index(val, key)
			}
		} else {
			val, err = in.slice(ix, val, env, io)
		}
		if err != nil {
			if _, ok := err.(*Raise); ok {
				return nil, err
			}
			return nil, raisef(v.Pos, "$"+v.Name+": "+err.Error())
		}
	}
	return val, nil
}

// indexArg is the value of a key or slice end used on v. A number written
// as a map key is the key spelled that way, as in [1: x].
func (in *Interp) indexArg(a *IndexArg, v Value, env *Env, io ioCtx) (Value, error) {
	if a.Var != nil {
		return in.lookupVar(a.Var, env, io)
	}
	if a.X != nil {
		return in.eval(a.X, env, io, false)
	}
	if n, ok := a.Lit.(*Num); ok {
		if _, ok := v.(*Map); ok {
			return n.Src, nil
		}
	}
	return a.Lit, nil
}

func index(v, key Value) (Value, error) {
	switch v := v.(type) {
	case *List:
		i, err := listIndex(key)
		if err != nil {
			return nil, err
		}
		if i < 0 || i >= len(v.Items) {
			return nil, fmt.Errorf("index %d out of range (len %d)", i, len(v.Items))
		}
		return v.Items[i], nil
	case *Map:
		k, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("a map key must be a str, got a %s: convert it first, let k = str $n", typeName(key))
		}
		val, ok := v.M[k]
		if !ok {
			return nil, fmt.Errorf("no key %s", literal(k))
		}
		return val, nil
	}
	return nil, fmt.Errorf("cannot index a %s", typeName(v))
}

func listIndex(key Value) (int, error) {
	n, ok := key.(*Num)
	if !ok || !n.R.IsInt() || !n.R.Num().IsInt64() {
		return 0, fmt.Errorf("a list index must be an integer, got %s", literal(key))
	}
	return int(n.R.Num().Int64()), nil
}

// slice is $xs[lo:hi]: the items from lo up to, not including, hi. An
// omitted lo is 0 and an omitted hi is the length.
func (in *Interp) slice(ix Index, v Value, env *Env, io ioCtx) (Value, error) {
	l, ok := v.(*List)
	if !ok {
		return nil, fmt.Errorf("cannot slice a %s", typeName(v))
	}
	end := func(a *IndexArg, n *int) error {
		if a == nil {
			return nil
		}
		k, err := in.indexArg(a, v, env, io)
		if err == nil {
			*n, err = listIndex(k)
		}
		return err
	}
	lo, hi := 0, len(l.Items)
	if err := end(ix.Lo, &lo); err != nil {
		return nil, err
	}
	if err := end(ix.Hi, &hi); err != nil {
		return nil, err
	}
	if lo < 0 || lo > hi || hi > len(l.Items) {
		return nil, fmt.Errorf("slice [%d:%d] out of range (len %d)", lo, hi, len(l.Items))
	}
	return &List{Items: append([]Value(nil), l.Items[lo:hi]...)}, nil
}

func escapeGlob(s string) string {
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(`*?[]\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// glob expands a word with active * ? [..] relative to the shell's cwd.
// Hidden files match only when the pattern's last part starts with '.'.
func (in *Interp) glob(w *Word, env *Env, io ioCtx) ([]string, error) {
	var b strings.Builder
	for _, a := range w.Parts {
		if l, ok := a.(*LitAtom); ok {
			if l.Glob {
				b.WriteString(strings.ReplaceAll(l.S, `\`, `\\`))
			} else {
				b.WriteString(escapeGlob(l.S))
			}
			continue
		}
		v, err := in.atomValue(a, env, io)
		if err != nil {
			return nil, err
		}
		s, err := concat(v)
		if err != nil {
			return nil, raisef(w.Pos, err.Error())
		}
		b.WriteString(escapeGlob(s))
	}
	pat := b.String()
	full, prefix := pat, ""
	if !filepath.IsAbs(pat) {
		prefix = strings.TrimSuffix(in.cwd, "/") + "/"
		full = prefix + pat
	}
	matches, err := filepath.Glob(full)
	if err != nil {
		return nil, raisef(w.Pos, "bad glob "+pat)
	}
	dot := strings.HasPrefix(filepath.Base(pat), ".")
	var out []string
	for _, m := range matches {
		if !dot && strings.HasPrefix(filepath.Base(m), ".") {
			continue
		}
		out = append(out, strings.TrimPrefix(m, prefix))
	}
	if len(out) == 0 {
		return nil, raisef(w.Pos, "no matches for "+pat)
	}
	sort.Strings(out)
	return out, nil
}
