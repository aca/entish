package lang

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Value is one of: string, bool, nil, *Num, *List, *Map, *Closure, *Builtin.
type Value = any

// Num is an exact rational. Src keeps the spelling of a literal ("0755",
// "1.10") until the number takes part in arithmetic.
type Num struct {
	R   *big.Rat
	Src string
}

type List struct{ Items []Value }

// Map keeps insertion order.
type Map struct {
	Keys []string
	M    map[string]Value
}

func newMap() *Map { return &Map{M: map[string]Value{}} }

func (m *Map) set(k string, v Value) {
	if _, ok := m.M[k]; !ok {
		m.Keys = append(m.Keys, k)
	}
	m.M[k] = v
}

// Closure is a named fn or a lambda.
type Closure struct {
	Name   string
	Params []string
	Rest   string
	Body   *Block
	Src    string // as written, for src
	Env    *Env
}

type Builtin struct {
	Name  string
	Min   int  // fewest args
	Max   int  // most args; -1 = any
	Data  bool // takes its data as the last arg, so it belongs after |>, not |
	Stdin bool // with its data arg left out it reads stdin, like cat: ls | lines
	Value bool // returns a value and writes nothing, so it cannot go left of |
	Void  bool // returns no value ($nil), so it cannot be used as one
	Lazy  bool // takes streams and seqs as they are (see stream.go)
	Fn    func(in *Interp, io ioCtx, args []Value) (Value, error)
}

var numRe = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

func isNumber(s string) bool { return numRe.MatchString(s) }

func numLit(s string) *Num {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad number literal " + s)
	}
	return &Num{R: r, Src: s}
}

func typeName(v Value) string {
	switch v.(type) {
	case nil:
		return "nil"
	case string:
		return "str"
	case bool:
		return "bool"
	case *Num:
		return "num"
	case *List:
		return "list"
	case *Map:
		return "map"
	case *Closure, *Builtin:
		return "fn"
	case *Stream:
		return "str"
	case *Seq:
		return "list"
	}
	return fmt.Sprintf("%T", v)
}

// ratString formats r exactly. exact is false when r has no finite decimal
// form (1/3); the result is then "1/3".
func ratString(r *big.Rat) (s string, exact bool) {
	if r.IsInt() {
		return r.Num().String(), true
	}
	d := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	var c2, c5 int
	for new(big.Int).Mod(d, two).Sign() == 0 {
		d.Div(d, two)
		c2++
	}
	for new(big.Int).Mod(d, five).Sign() == 0 {
		d.Div(d, five)
		c5++
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		return r.Num().String() + "/" + r.Denom().String(), false
	}
	return r.FloatString(max(c2, c5)), true
}

func numString(n *Num) (string, bool) {
	if n.Src != "" {
		return n.Src, true
	}
	return ratString(n.R)
}

// display formats v for print and the REPL: strings raw, everything else in
// entish literal syntax.
func display(v Value) string {
	if s, ok := v.(string); ok {
		return s
	}
	return literal(v)
}

func literal(v Value) string {
	switch v := v.(type) {
	case nil:
		return "$nil"
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case bool:
		if v {
			return "$true"
		}
		return "$false"
	case *Num:
		s, _ := numString(v)
		return s
	case *List:
		parts := make([]string, len(v.Items))
		for i, it := range v.Items {
			parts[i] = literal(it)
		}
		return "[" + strings.Join(parts, " ") + "]"
	case *Map:
		if len(v.Keys) == 0 {
			return "[:]"
		}
		parts := make([]string, len(v.Keys))
		for i, k := range v.Keys {
			key := k
			if !isBareKey(k) {
				key = literal(k)
			}
			parts[i] = key + ": " + literal(v.M[k])
		}
		return "[" + strings.Join(parts, "  ") + "]"
	case *Closure:
		if v.Name == "" {
			return "<fn>"
		}
		return "<fn " + v.Name + ">"
	case *Builtin:
		return "<builtin " + v.Name + ">"
	}
	return fmt.Sprint(v)
}

func isBareKey(k string) bool {
	if k == "" {
		return false
	}
	for _, c := range k {
		if !isKeyChar(c) {
			return false
		}
	}
	return true
}

// toArg converts a value for an external's argv. Only str and num cross the
// boundary.
func toArg(v Value) (string, error) {
	switch v := v.(type) {
	case string:
		return v, nil
	case *Num:
		s, exact := numString(v)
		if !exact {
			return "", fmt.Errorf("%s has no exact decimal; round it first", s)
		}
		return s, nil
	case nil:
		return "", fmt.Errorf("$nil passed to an external (unset variable?)")
	case *List:
		return "", fmt.Errorf("list passed to an external, use ...$xs")
	}
	return "", fmt.Errorf("%s passed to an external", typeName(v))
}

// concat converts a value that is glued to other text or interpolated.
func concat(v Value) (string, error) {
	switch v.(type) {
	case string, *Num:
		return toArg(v)
	case nil:
		return "", fmt.Errorf("cannot use $nil in a string (unset variable?)")
	}
	return "", fmt.Errorf("cannot use a %s in a string", typeName(v))
}

func equal(a, b Value) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	if typeName(a) != typeName(b) {
		return false, fmt.Errorf("cannot compare %s with %s", typeName(a), typeName(b))
	}
	switch a := a.(type) {
	case string:
		return a == b.(string), nil
	case bool:
		return a == b.(bool), nil
	case *Num:
		return a.R.Cmp(b.(*Num).R) == 0, nil
	case *List:
		bl := b.(*List)
		if len(a.Items) != len(bl.Items) {
			return false, nil
		}
		for i := range a.Items {
			eq, err := equal(a.Items[i], bl.Items[i])
			if err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	case *Map:
		bm := b.(*Map)
		if len(a.Keys) != len(bm.Keys) {
			return false, nil
		}
		for _, k := range a.Keys {
			bv, ok := bm.M[k]
			if !ok {
				return false, nil
			}
			eq, err := equal(a.M[k], bv)
			if err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	}
	return a == b, nil
}
