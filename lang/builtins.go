package lang

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// builtins is the v0 set: only what the language core needs. Collection
// fns take the data last so they compose with |>.
var builtins map[string]*Builtin

func init() {
	builtins = map[string]*Builtin{}
	add := func(name string, min, max int, fn func(*Interp, ioCtx, []Value) (Value, error)) {
		builtins[name] = &Builtin{Name: name, Min: min, Max: max, Fn: fn}
	}
	add("print", 0, -1, biPrint)
	add("cd", 0, 1, biCd)
	add("export", 1, -1, biExport)
	add("abbr", 2, 2, biAbbr)
	add("bind", 2, 2, biBind)
	add("each", 2, 2, biEach)
	add("exit", 0, 1, biExit)
	add("fail", 1, 1, biFail)
	add("call", 1, 1, nil)   // rewritten to a $f call by the checker
	add("eval", 1, -1, nil)  // compiled when it runs, see evalFn
	add("source", 1, 1, nil) // expanded before checking, see expandSource
	add("src", 1, 1, biSrc)
	add("lines", 0, 1, splitter("lines", '\n'))
	add("lines0", 0, 1, splitter("lines0", 0))
	add("num", 1, 1, biNum)
	add("str", 1, 1, biStr)
	add("round", 2, 2, biRound)
	add("keys", 1, 1, biKeys)
	add("assoc", 3, 3, biAssoc)
	add("len", 1, 1, biLen)
	add("map", 2, 2, biMap)
	add("filter", 2, 2, biFilter)
	add("to_json", 1, 1, biToJSON)
	add("from_json", 0, 1, biFromJSON)
	add("as_file", 0, 1, biAsFile)
	for _, n := range []string{"each", "map", "filter", "lines", "lines0", "len", "num", "str", "round", "keys", "assoc", "to_json", "from_json", "src"} {
		builtins[n].Data = true
	}
	for _, n := range []string{"lines", "lines0", "from_json", "as_file"} {
		builtins[n].Stdin = true
	}
	for _, n := range []string{"lines", "lines0", "num", "str", "round", "keys", "assoc", "len", "map", "filter", "from_json", "as_file", "src"} {
		builtins[n].Value = true
	}
	for _, n := range []string{"print", "to_json", "cd", "export", "abbr", "bind", "each"} {
		builtins[n].Void = true
	}
	for _, n := range []string{"lines", "lines0", "map", "filter", "each", "as_file"} {
		builtins[n].Lazy = true
	}
}

// builtinValue is the builtin for $name, or nil. call has no value: it is
// rewritten by the checker.
func builtinValue(name string) *Builtin {
	if b := builtins[name]; b != nil && b.Fn != nil {
		return b
	}
	return nil
}

func nargs(name string, args []Value, n int) error {
	if len(args) != n {
		return fmt.Errorf("%s takes %d args, got %d", name, n, len(args))
	}
	return nil
}

func biPrint(in *Interp, io ioCtx, args []Value) (Value, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = display(a)
	}
	_, err := fmt.Fprintln(io.out, strings.Join(parts, " "))
	return nil, err
}

func biCd(in *Interp, io ioCtx, args []Value) (Value, error) {
	var dir string
	switch len(args) {
	case 0:
		dir = in.Getenv("HOME")
	case 1:
		s, err := toArg(args[0])
		if err != nil {
			return nil, err
		}
		dir = s
	default:
		return nil, fmt.Errorf("cd takes 0 or 1 args, got %d", len(args))
	}
	p := filepath.Clean(in.abs(dir))
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("cd: %s: no such directory", dir)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("cd: %s: not a directory", dir)
	}
	in.setEnv("OLDPWD", in.cwd, "PWD", p)
	in.cwd = p
	return nil, nil
}

func biExit(in *Interp, io ioCtx, args []Value) (Value, error) {
	switch len(args) {
	case 0:
		return nil, &ExitError{Code: 0}
	case 1:
		n, ok := args[0].(*Num)
		if !ok || !n.R.IsInt() || !n.R.Num().IsInt64() {
			return nil, fmt.Errorf("exit needs an integer code")
		}
		return nil, &ExitError{Code: int(n.R.Num().Int64())}
	}
	return nil, fmt.Errorf("exit takes 0 or 1 args, got %d", len(args))
}

func biFail(in *Interp, io ioCtx, args []Value) (Value, error) {
	if err := nargs("fail", args, 1); err != nil {
		return nil, err
	}
	if m, ok := args[0].(*Map); ok {
		return nil, &Raise{Val: m}
	}
	return nil, errors.New(display(args[0]))
}

// biSrc returns the source of a fn or lambda, as written.
func biSrc(in *Interp, io ioCtx, args []Value) (Value, error) {
	switch f := args[0].(type) {
	case *Closure:
		return f.Src, nil
	case *Builtin:
		return nil, fmt.Errorf("src: %s is a builtin; it has no entish source", f.Name)
	}
	return nil, fmt.Errorf("src needs a fn, got a %s", typeName(args[0]))
}

func biNum(in *Interp, io ioCtx, args []Value) (Value, error) {
	if err := nargs("num", args, 1); err != nil {
		return nil, err
	}
	switch v := args[0].(type) {
	case *Num:
		return v, nil
	case string:
		r, ok := new(big.Rat).SetString(strings.TrimSpace(v))
		if !ok {
			return nil, fmt.Errorf("num: %s is not a number", literal(v))
		}
		return &Num{R: r}, nil
	}
	return nil, fmt.Errorf("num needs a str, got %s", typeName(args[0]))
}

// round rounds a num to n decimal places, half away from zero:
// 1 / 3 |> round 4 is 0.3333. The result has an exact decimal, so it can
// be printed or passed to an external.
func biRound(in *Interp, io ioCtx, args []Value) (Value, error) {
	n, ok := args[0].(*Num)
	if !ok || !n.R.IsInt() || n.R.Sign() < 0 {
		return nil, fmt.Errorf("round takes a whole number of digits >= 0, got %s", display(args[0]))
	}
	x, ok := args[1].(*Num)
	if !ok {
		return nil, fmt.Errorf("round needs a num, got a %s", typeName(args[1]))
	}
	scale := new(big.Int).Exp(big.NewInt(10), n.R.Num(), nil)
	v := new(big.Rat).Mul(x.R, new(big.Rat).SetInt(scale))
	q, r := new(big.Int).QuoRem(new(big.Int).Abs(v.Num()), v.Denom(), new(big.Int))
	if r.Lsh(r, 1).Cmp(v.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if v.Sign() < 0 {
		q.Neg(q)
	}
	return &Num{R: new(big.Rat).SetFrac(q, scale)}, nil
}

// keys lists a map's keys in insertion order: [a: 1 b: 2] |> keys is ['a' 'b'].
func biKeys(in *Interp, io ioCtx, args []Value) (Value, error) {
	m, ok := args[0].(*Map)
	if !ok {
		return nil, fmt.Errorf("keys needs a map, got a %s", typeName(args[0]))
	}
	l := &List{}
	for _, k := range m.Keys {
		l.Items = append(l.Items, k)
	}
	return l, nil
}

// assoc returns a copy of a map with key k set to v: $m |> assoc c 3.
// An existing key keeps its place.
func biAssoc(in *Interp, io ioCtx, args []Value) (Value, error) {
	k, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("assoc takes a str key, got a %s: use (str $k)", typeName(args[0]))
	}
	m, ok := args[2].(*Map)
	if !ok {
		return nil, fmt.Errorf("assoc needs a map, got a %s", typeName(args[2]))
	}
	out := &Map{Keys: append([]string(nil), m.Keys...), M: maps.Clone(m.M)}
	out.set(k, args[1])
	return out, nil
}

func biStr(in *Interp, io ioCtx, args []Value) (Value, error) {
	if err := nargs("str", args, 1); err != nil {
		return nil, err
	}
	return display(args[0]), nil
}

func biLen(in *Interp, io ioCtx, args []Value) (Value, error) {
	if err := nargs("len", args, 1); err != nil {
		return nil, err
	}
	var n int
	switch v := args[0].(type) {
	case string:
		n = utf8.RuneCountInString(v)
	case *List:
		n = len(v.Items)
	case *Map:
		n = len(v.Keys)
	default:
		return nil, fmt.Errorf("len needs a str, list or map, got %s", typeName(v))
	}
	return &Num{R: ratInt(n)}, nil
}

// collection args: (fn, list)

// export sets env vars of the shell: export PATH="$HOME/bin:$PATH" GOOS=linux.
// Every env var is passed to externals; there is no unexported env.
func biExport(in *Interp, io ioCtx, args []Value) (Value, error) {
	kv := make([][2]string, 0, len(args))
	for _, a := range args {
		s, ok := a.(string)
		if !ok {
			return nil, fmt.Errorf("export takes NAME=value, got a %s", typeName(a))
		}
		name, val, ok := strings.Cut(s, "=")
		if !ok || !isEnvName(name) {
			return nil, fmt.Errorf("export takes NAME=value, got %s", literal(s))
		}
		kv = append(kv, [2]string{name, val})
	}
	for _, p := range kv { // all or nothing
		in.setEnv(p[0], p[1])
	}
	return nil, nil
}

// abbr defines an abbreviation for the editor: abbr gco 'git checkout'.
// Typed where a command can start, the name is replaced by the expansion
// on Space or Enter.
func biAbbr(in *Interp, io ioCtx, args []Value) (Value, error) {
	name, ok := args[0].(string)
	if !ok || name == "" || quoteWord(name) != name {
		return nil, fmt.Errorf("abbr takes a plain word as its name, got %s", display(args[0]))
	}
	exp, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("abbr takes a str as its expansion, got a %s", typeName(args[1]))
	}
	in.abbrs[name] = exp
	return nil, nil
}

// bind binds a key in the editor: bind ctrl-x { print $it | wl-copy }.
// A fn that takes a param gets the input being edited; a str it returns is
// inserted at the cursor, a map [input: str] replaces the input.
func biBind(in *Interp, io ioCtx, args []Value) (Value, error) {
	key, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("bind takes a key such as ctrl-x or alt-c, got a %s", typeName(args[0]))
	}
	if err := checkKey(key); err != nil {
		return nil, err
	}
	f, ok := args[1].(*Closure)
	if !ok {
		return nil, fmt.Errorf("bind takes a fn, got a %s", typeName(args[1]))
	}
	if len(f.Params) > 1 {
		return nil, fmt.Errorf("bind takes a fn of at most one param (the input), got %d", len(f.Params))
	}
	in.binds[key] = f
	return nil, nil
}

// checkKey accepts ctrl-a .. ctrl-z and alt-a .. alt-z, alt-0 .. alt-9.
func checkKey(key string) error {
	if c, ok := strings.CutPrefix(key, "ctrl-"); ok && len(c) == 1 && c[0] >= 'a' && c[0] <= 'z' {
		switch c {
		case "i":
			return fmt.Errorf("bind: ctrl-i is Tab to the terminal")
		case "j", "m":
			return fmt.Errorf("bind: ctrl-%s is Enter to the terminal", c)
		}
		return nil
	}
	if c, ok := strings.CutPrefix(key, "alt-"); ok && len(c) == 1 && (c[0] >= 'a' && c[0] <= 'z' || c[0] >= '0' && c[0] <= '9') {
		return nil
	}
	return fmt.Errorf("bind takes a key such as ctrl-x or alt-c, got %s", display(key))
}

func isEnvName(s string) bool {
	if s == "" || !isIdentStart(rune(s[0])) {
		return false
	}
	for _, c := range s {
		if !isIdent(c) {
			return false
		}
	}
	return true
}

// splitter makes lines and lines0 (NUL-separated, as from find -print0):
// they split a str into records. With no arg they read stdin, like cat.
// Reading a running command's output (ls | lines) they are lazy: records
// arrive as the command prints them.
func splitter(name string, delim byte) func(*Interp, ioCtx, []Value) (Value, error) {
	return func(in *Interp, io ioCtx, args []Value) (Value, error) {
		if len(args) == 0 {
			return &Seq{next: records(io.in, delim, func() error { return nil }), close: func() {}}, nil
		}
		if err := nargs(name, args, 1); err != nil {
			return nil, err
		}
		if st, ok := args[0].(*Stream); ok {
			return st.split(delim), nil
		}
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%s needs a str, got %s", name, typeName(args[0]))
		}
		l := &List{}
		if s == "" {
			return l, nil
		}
		d := string(delim)
		for _, rec := range strings.Split(strings.TrimSuffix(s, d), d) {
			if delim == '\n' {
				rec = strings.TrimSuffix(rec, "\r")
			}
			l.Items = append(l.Items, rec)
		}
		return l, nil
	}
}

// fnData checks the (fn, list) args of map, filter and each; the list may
// be a lazy seq.
func fnData(name string, args []Value) (Value, *Seq, error) {
	if err := nargs(name, args, 2); err != nil {
		return nil, nil, err
	}
	switch args[0].(type) {
	case *Closure, *Builtin:
	default:
		return nil, nil, fmt.Errorf("%s needs a fn first, got %s", name, typeName(args[0]))
	}
	switch d := args[1].(type) {
	case *Seq:
		return args[0], d, nil
	case *List:
		i := 0
		return args[0], &Seq{
			next: func() (Value, bool, error) {
				if i >= len(d.Items) {
					return nil, false, nil
				}
				i++
				return d.Items[i-1], true, nil
			},
			close: func() {},
		}, nil
	}
	return nil, nil, fmt.Errorf("%s needs a list last, got %s", name, typeName(args[1]))
}

func biMap(in *Interp, io ioCtx, args []Value) (Value, error) {
	f, q, err := fnData("map", args)
	if err != nil {
		return nil, err
	}
	out := in.mapSeq(f, q, io)
	if _, lazy := args[1].(*Seq); lazy {
		return out, nil
	}
	return out.collect()
}

func biFilter(in *Interp, io ioCtx, args []Value) (Value, error) {
	f, q, err := fnData("filter", args)
	if err != nil {
		return nil, err
	}
	out := in.filterSeq(f, q, io)
	if _, lazy := args[1].(*Seq); lazy {
		return out, nil
	}
	return out.collect()
}

// each runs f for each item as it arrives.
func biEach(in *Interp, io ioCtx, args []Value) (Value, error) {
	f, q, err := fnData("each", args)
	if err != nil {
		return nil, err
	}
	for {
		v, ok, err := q.next()
		if err != nil {
			q.close()
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		if _, err := in.callValue(f, []Value{v}, io, false, Pos{}); err != nil {
			q.close()
			return nil, err
		}
	}
}

// to_json prints a value as compact JSON, like print: [a: [1 $true]] |>
// to_json prints {"a":[1,true]}. A num with no exact decimal and a fn raise.
func biToJSON(in *Interp, io ioCtx, args []Value) (Value, error) {
	if err := nargs("to_json", args, 1); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := writeJSON(&b, args[0]); err != nil {
		return nil, fmt.Errorf("to_json: %w", err)
	}
	b.WriteByte('\n')
	_, err := io.out.Write(b.Bytes())
	return nil, err
}

func writeJSON(b *bytes.Buffer, v Value) error {
	switch v := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		fmt.Fprint(b, v)
	case string:
		e := json.NewEncoder(b)
		e.SetEscapeHTML(false)
		e.Encode(v)
		b.Truncate(b.Len() - 1) // Encode adds a newline
	case *Num:
		s, exact := ratString(v.R) // not the source text: 0755 is no JSON number
		if !exact {
			return fmt.Errorf("%s has no exact decimal; round it first", s)
		}
		b.WriteString(s)
	case *List:
		b.WriteByte('[')
		for i, it := range v.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, it); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case *Map:
		b.WriteByte('{')
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, k)
			b.WriteByte(':')
			if err := writeJSON(b, v.M[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("cannot encode a %s", typeName(v))
	}
	return nil
}

// from_json decodes one JSON value: objects become maps (keys in order),
// arrays lists, numbers exact nums, null $nil. A key twice raises. With no
// arg it reads stdin.
func biFromJSON(in *Interp, ioc ioCtx, args []Value) (Value, error) {
	if len(args) == 0 { // stdin, like cat
		b, err := io.ReadAll(ioc.in)
		if err != nil {
			return nil, fmt.Errorf("from_json: %w", err)
		}
		args = []Value{string(b)}
	}
	if err := nargs("from_json", args, 1); err != nil {
		return nil, err
	}
	s, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("from_json needs a str, got a %s", typeName(args[0]))
	}
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	v, err := readJSON(d)
	if err == io.EOF {
		err = errors.New("no JSON value")
	} else if err == nil {
		if _, e := d.Token(); e != io.EOF {
			err = errors.New("unexpected data after the value")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("from_json: %w", err)
	}
	return v, nil
}

// as_file copies stdin, byte for byte, to a new temp file and returns its
// path, for commands that only take files: diff (sort a | as_file) b. It
// takes no value: only print and to_json turn values into bytes. The file
// is in $XDG_RUNTIME_DIR if set, mode 0600, and is removed when the input
// ends.
func biAsFile(in *Interp, ioc ioCtx, args []Value) (Value, error) {
	src := ioc.in
	var st *Stream
	if len(args) > 0 {
		var ok bool
		if st, ok = args[0].(*Stream); !ok {
			return nil, errors.New("as_file reads stdin, not a value: cmd | as_file")
		}
		src = st.r
	}
	dir := in.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "entish-*")
	if err != nil {
		return nil, fmt.Errorf("as_file: %w", err)
	}
	in.tmpMu.Lock()
	in.tmps = append(in.tmps, f.Name())
	in.tmpMu.Unlock()
	_, err = io.Copy(f, src)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return nil, fmt.Errorf("as_file: %w", err)
	}
	if st != nil {
		st.r.Close()
		if err := st.wait(); err != nil { // the producer failed
			return nil, err
		}
	}
	return f.Name(), nil
}

func (in *Interp) removeTmps() {
	in.tmpMu.Lock()
	defer in.tmpMu.Unlock()
	for _, p := range in.tmps {
		os.Remove(p)
	}
	in.tmps = nil
}

func readJSON(d *json.Decoder) (Value, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := t.(type) {
	case json.Delim:
		if t == '[' {
			l := &List{}
			for d.More() {
				v, err := readJSON(d)
				if err != nil {
					return nil, err
				}
				l.Items = append(l.Items, v)
			}
			_, err := d.Token()
			return l, err
		}
		m := newMap()
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key := k.(string)
			if _, dup := m.M[key]; dup {
				return nil, fmt.Errorf("duplicate key %s", literal(key))
			}
			v, err := readJSON(d)
			if err != nil {
				return nil, err
			}
			m.set(key, v)
		}
		_, err := d.Token()
		return m, err
	case json.Number:
		r, _ := new(big.Rat).SetString(string(t))
		return &Num{R: r}, nil
	}
	return t, nil // string, bool or nil
}
