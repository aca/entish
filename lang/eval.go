package lang

import (
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var reservedVars = map[string]bool{"true": true, "false": true, "nil": true, "it": true, "_": true}

func ratInt(n int) *big.Rat { return new(big.Rat).SetInt64(int64(n)) }

// execBlock runs b; its statements emit to io.vout. In cond mode its last
// statement reports a failing command as ok false instead of raising (used
// for fns in conditions).
func (in *Interp) execBlock(b *Block, env *Env, io ioCtx, cond bool) (bool, error) {
	act := env
	if len(b.Fns) > 0 {
		act = &Env{parent: env, fns: map[string]*Closure{}}
	}
	cur := act
	ok := true
	for i, st := range b.Stmts {
		if in.intr.Swap(false) {
			return false, interrupted(b.Pos)
		}
		var next *Env
		var err error
		ok, next, err = in.execStmt(st, cur, act, io, cond && i == len(b.Stmts)-1)
		if err != nil {
			return false, err
		}
		cur = next
	}
	return ok, nil
}

func (in *Interp) execStmt(st Stmt, env, act *Env, io ioCtx, cond bool) (bool, *Env, error) {
	switch st := st.(type) {
	case *LetStmt:
		v, err := in.one(st.Val, env, io)
		if err != nil {
			return false, env, err
		}
		return true, &Env{parent: env, name: st.Name, val: v}, nil
	case *FnStmt:
		// The fn is also the value $name from here on, like a let; its own
		// body sees it too, so $name recurses.
		d := st.Def
		c := &Closure{Name: d.Name, Params: d.Params, Rest: d.Rest, Body: d.Body, Src: d.Src}
		node := &Env{parent: env, name: d.Name, val: c}
		c.Env = node
		act.fns[d.Name] = c
		return true, node, nil
	case *ExprStmt:
		ok, err := in.emit(st.X, env, io, cond)
		return ok, env, err
	}
	panic("unknown statement")
}

// values runs e and collects the values it emits.
func (in *Interp) values(e Expr, env *Env, io ioCtx) ([]Value, error) {
	var vals []Value
	var mu sync.Mutex
	cio := io
	cio.vout = func(v Value) error {
		mu.Lock()
		vals = append(vals, v)
		mu.Unlock()
		return nil
	}
	_, err := in.emit(e, env, cio, false)
	return vals, err
}

// one is the value of e where exactly one is needed: let, ( ), an operand.
func (in *Interp) one(e Expr, env *Env, io ioCtx) (Value, error) {
	if we, ok := e.(*WordExpr); ok && isGlobWord(we.W) {
		return nil, globOne(we.W)
	}
	vals, err := in.values(e, env, io)
	if err != nil {
		return nil, err
	}
	name := callName(e)
	switch x := e.(type) {
	case *WordExpr: // (f)
		if g, ok := x.W.Parts[0].(*GroupAtom); ok && len(x.W.Parts) == 1 {
			name = callName(g.X)
		}
	case *IfExpr:
		name = "this if"
	case *MatchExpr:
		name = "this match"
	case *TryExpr:
		name = "this try"
	}
	switch n := len(vals); {
	case n == 1:
		return vals[0], nil
	case n == 0:
		return nil, raisef(e.pos(), name+" emitted no value")
	default:
		return nil, raisef(e.pos(), fmt.Sprintf("%s emitted %d values; to collect them into a list: ... |> list", name, n))
	}
}

// globOne: a glob gives paths, however many match, so it is never one
// value; otherwise its type would depend on the files.
func globOne(w *Word) error {
	return raisef(w.Pos, "a glob gives several paths, not one value; to collect them into a list: [*.go]")
}

// emit runs e, sending the values it emits to io.vout. Commands raise on
// failure; in cond mode a failing command gives ok false instead.
func (in *Interp) emit(e Expr, env *Env, io ioCtx, cond bool) (bool, error) {
	switch e := e.(type) {
	case *WordExpr:
		vals, err := in.wordValues(e.W, env, io)
		if err != nil {
			return false, err
		}
		for _, v := range vals {
			if err := io.emit(v); err != nil {
				return false, err
			}
		}
		return true, nil
	case *BinExpr:
		v, err := in.binary(e, env, io)
		if err != nil {
			return false, err
		}
		return true, io.emit(v)
	case *BlockExpr:
		return in.execBlock(e.B, env, io, cond)
	case *IfExpr:
		b, err := in.test(e.Cond, env, io, "if", true)
		switch {
		case err != nil:
			return false, err
		case b:
			return in.execBlock(e.Then, env, io, cond)
		case e.Else != nil:
			return in.emit(e.Else, env, io, cond)
		}
		return true, nil
	case *TryExpr:
		var ok bool
		var err error
		switch body := e.Body.(type) {
		case *BlockExpr:
			ok, err = in.execBlock(body.B, env, io, cond)
		case *Call:
			var f Value
			if f, err = in.lookupVar(body.HeadVar, env, io); err == nil {
				ok, err = in.callValue(f, nil, io, cond, body.P)
			}
		}
		var r *Raise
		if errors.As(err, &r) && !r.intr {
			return in.execBlock(e.Handler, &Env{parent: env, name: e.Name, val: r.Val}, io, cond)
		}
		return ok, err
	case *MatchExpr:
		return in.match(e, env, io, cond)
	case *OrExpr:
		return in.andOr(e, e.L, e.R, false, env, io, cond)
	case *AndExpr:
		return in.andOr(e, e.L, e.R, true, env, io, cond)
	case *NotExpr:
		if !isCommand(e.X) { // a bool
			b, err := in.test(e.X, env, io, "!", false)
			if err != nil {
				return false, err
			}
			return true, io.emit(!b)
		}
		b, err := in.test(e.X, env, io, "!", true)
		switch {
		case err != nil:
			return false, err
		case cond:
			return !b, nil
		case b: // !cmd as a statement asserts that cmd fails
			return false, raisef(e.P, "'!"+callName(e.X)+"' failed: the command succeeded")
		}
		return true, nil
	case *Pipeline:
		return in.pipeline(e.Stages, e.Ops, e.topic, env, io, cond)
	case *Call:
		return in.pipeline([]Expr{e}, nil, nil, env, io, cond)
	}
	panic(fmt.Sprintf("unknown expression %T", e))
}

// match runs the first arm whose pattern matches and whose guard holds.
func (in *Interp) match(e *MatchExpr, env *Env, io ioCtx, cond bool) (bool, error) {
	v, err := in.one(e.X, env, io)
	if err != nil {
		return false, err
	}
	for _, arm := range e.Arms {
		aenv, ok, err := in.matchPat(arm.Pat, v, env, env, io)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		if arm.Guard != nil {
			if ok, err = in.test(arm.Guard, aenv, io, "a match guard", true); err != nil {
				return false, err
			}
			if !ok {
				continue
			}
		}
		return in.execBlock(arm.Body, aenv, io, cond)
	}
	s := literal(v)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "..."
	}
	return false, raisef(e.P, "match: no arm matches "+s)
}

// matchPat matches v against p. Bindings are added to benv; a $v pattern
// is looked up in env, outside the pattern.
func (in *Interp) matchPat(p Pattern, v Value, env, benv *Env, io ioCtx) (*Env, bool, error) {
	switch p := p.(type) {
	case *AnyPat:
		return benv, true, nil
	case *BindPat:
		return &Env{parent: benv, name: p.Name, val: v}, true, nil
	case *LitPat:
		return benv, sameValue(p.Val, v), nil
	case *VarPat:
		want, err := in.lookupVar(p.V, env, io)
		if err != nil {
			return nil, false, err
		}
		return benv, sameValue(want, v), nil
	case *ListPat:
		l, ok := v.(*List)
		if !ok || len(l.Items) < len(p.Items) || p.Rest == "" && len(l.Items) != len(p.Items) {
			return benv, false, nil
		}
		for i, x := range p.Items {
			var err error
			if benv, ok, err = in.matchPat(x, l.Items[i], env, benv, io); err != nil || !ok {
				return benv, ok, err
			}
		}
		if p.Rest != "" && p.Rest != "_" {
			rest := &List{Items: append([]Value(nil), l.Items[len(p.Items):]...)}
			benv = &Env{parent: benv, name: p.Rest, val: rest}
		}
		return benv, true, nil
	case *MapPat:
		m, ok := v.(*Map)
		if !ok {
			return benv, false, nil
		}
		for i, k := range p.Keys {
			mv, ok := m.M[k]
			if !ok {
				return benv, false, nil
			}
			var err error
			if benv, ok, err = in.matchPat(p.Vals[i], mv, env, benv, io); err != nil || !ok {
				return benv, ok, err
			}
		}
		return benv, true, nil
	}
	panic(fmt.Sprintf("unknown pattern %T", p))
}

// sameValue is == for patterns: values of different types differ instead
// of raising.
func sameValue(a, b Value) bool {
	eq, err := equal(a, b)
	return err == nil && eq
}

// isCommand: e runs as a command, so a condition tests its status. Any
// other operand of if, &&, || and ! is a value and must be a bool; ( )
// makes a call one: if (big 10).
func isCommand(e Expr) bool {
	switch e := e.(type) {
	case *Call, *Pipeline:
		return true
	case *AndExpr:
		return isCommand(e.L) || isCommand(e.R)
	case *OrExpr:
		return isCommand(e.L) || isCommand(e.R)
	case *NotExpr:
		return isCommand(e.X)
	}
	return false
}

// test runs e as an operand of if, &&, || and !. A command is tested by its
// status (in cond mode a failure is false), its values dropped; anything
// else must be one bool. There is no truthiness.
func (in *Interp) test(e Expr, env *Env, io ioCtx, what string, cond bool) (bool, error) {
	if isCommand(e) {
		cio := io
		cio.vout = nil
		return in.emit(e, env, cio, cond)
	}
	v, err := in.one(e, env, io)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, raisef(e.pos(), fmt.Sprintf("%s needs a bool, got %s", what, typeName(v)))
	}
	return b, nil
}

// andOr runs l && r (and) or l || r. Between values it is a bool: $a && $b.
// With a command on either side it is control flow: l is tested, r runs if
// needed and emits what it emits; outside a condition l's result is not
// emitted, so `$verbose && print x` adds no value to a fn's. The left of ||
// is always tested as a condition: `make || print failed`.
func (in *Interp) andOr(e, l, r Expr, and bool, env *Env, io ioCtx, cond bool) (bool, error) {
	what := "||"
	if and {
		what = "&&"
	}
	if !isCommand(e) { // between values: a bool, in a condition too
		lv, err := in.test(l, env, io, what, false)
		if err == nil && lv == and {
			lv, err = in.test(r, env, io, what, false)
		}
		if err != nil {
			return false, err
		}
		return true, io.emit(lv)
	}
	lv, err := in.test(l, env, io, what, cond || !and)
	switch {
	case err != nil:
		return false, err
	case lv != and && cond: // r does not run
		return lv, nil
	case lv != and:
		return true, nil
	case cond:
		return in.test(r, env, io, what, true)
	}
	return in.emit(r, env, io, false)
}

func (in *Interp) binary(e *BinExpr, env *Env, io ioCtx) (Value, error) {
	l, err := in.one(e.L, env, io)
	if err != nil {
		return nil, err
	}
	r, err := in.one(e.R, env, io)
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

// wordValues is what a word contributes to args, list items or a value
// stream: a spread or a glob may be several.
func (in *Interp) wordValues(w *Word, env *Env, io ioCtx) ([]Value, error) {
	if len(w.Parts) == 1 {
		if sp, ok := w.Parts[0].(*SpreadAtom); ok { // ...$xs, ...[a b], ...(f): the items of a list
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

// wordValue is a word used as one value.
func (in *Interp) wordValue(w *Word, env *Env, io ioCtx) (Value, error) {
	if isGlobWord(w) {
		return nil, globOne(w)
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
		v, err := in.one(a.X, env, io)
		if err != nil || len(a.Index) == 0 {
			return v, err
		}
		if v, err = in.applyIndex(v, a.Index, env, io); err != nil {
			if _, ok := err.(*Raise); ok {
				return nil, err
			}
			return nil, raisef(a.X.pos(), "( ): "+err.Error())
		}
		return v, nil
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
		return &Closure{Params: a.Params, Rest: a.Rest, AnyArgs: a.anyArgs(), Body: a.Body, Src: a.Src, Env: env}, nil
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
	val, err := in.applyIndex(val, v.Index, env, io)
	if err != nil {
		if _, ok := err.(*Raise); ok {
			return nil, err
		}
		return nil, raisef(v.Pos, "$"+v.Name+": "+err.Error())
	}
	return val, nil
}

// applyIndex applies indexes and slices to val in order.
func (in *Interp) applyIndex(val Value, ixs []Index, env *Env, io ioCtx) (Value, error) {
	for _, ix := range ixs {
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
			return nil, err
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
		return in.one(a.X, env, io)
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

// glob expands a word with active * ? relative to the shell's cwd.
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
