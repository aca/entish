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

// builtins is the v0 set: only what the language core needs. A builtin
// reads its data from its args, its stdin (Stdin) or its value input (VIn),
// and emits values to its value output.
var builtins map[string]*Builtin

func init() {
	builtins = map[string]*Builtin{}
	add := func(name string, min, max int, fn func(*Interp, ioCtx, []Value) error) {
		builtins[name] = &Builtin{Name: name, Min: min, Max: max, Fn: fn}
	}
	add("print", 0, -1, biPrint)
	add("cd", 0, 1, biCd)
	add("export", 1, -1, biExport)
	add("abbr", 2, 2, biAbbr)
	add("bind", 2, 2, biBind)
	add("each", 1, 1, biEach)
	add("count", 0, 0, biCount)
	add("list", 0, 0, biList)
	add("exit", 0, 1, biExit)
	add("fail", 1, 1, biFail)
	add("status", 1, 1, biStatus)
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
	add("to_json", 0, 1, biToJSON)
	add("from_json", 0, 1, biFromJSON)
	add("as_file", 0, 0, biAsFile)
	for _, n := range []string{"lines", "lines0", "from_json", "as_file"} {
		builtins[n].Stdin = true
	}
	for _, n := range []string{"each", "count", "list", "print", "to_json"} {
		builtins[n].VIn = true
	}
	for _, n := range []string{"print", "to_json", "each", "status"} {
		builtins[n].Out = true
	}
	for _, n := range []string{"print", "to_json", "cd", "export", "abbr", "bind", "exit", "fail"} {
		builtins[n].NoValues = true
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

// print writes its args, separated by spaces, and a newline. With no args
// it writes each value of its value input on a line: xs |> print.
func biPrint(in *Interp, io ioCtx, args []Value) error {
	if len(args) == 0 {
		for v, ok := io.vin.next(); ok; v, ok = io.vin.next() {
			if _, err := fmt.Fprintln(io.out, display(v)); err != nil {
				return err
			}
		}
		return nil
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = display(a)
	}
	_, err := fmt.Fprintln(io.out, strings.Join(parts, " "))
	return err
}

func biCd(in *Interp, io ioCtx, args []Value) error {
	var dir string
	switch len(args) {
	case 0:
		dir = in.Getenv("HOME")
	case 1:
		s, err := toArg(args[0])
		if err != nil {
			return err
		}
		dir = s
	default:
		return fmt.Errorf("cd takes 0 or 1 args, got %d", len(args))
	}
	p := filepath.Clean(in.abs(dir))
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("cd: %s: no such directory", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("cd: %s: not a directory", dir)
	}
	in.setEnv("OLDPWD", in.cwd, "PWD", p)
	in.cwd = p
	return nil
}

func biExit(in *Interp, io ioCtx, args []Value) error {
	switch len(args) {
	case 0:
		return &ExitError{Code: 0}
	case 1:
		n, ok := args[0].(*Num)
		if !ok || !n.R.IsInt() || !n.R.Num().IsInt64() {
			return fmt.Errorf("exit needs an integer code")
		}
		return &ExitError{Code: int(n.R.Num().Int64())}
	}
	return fmt.Errorf("exit takes 0 or 1 args, got %d", len(args))
}

func biFail(in *Interp, io ioCtx, args []Value) error {
	if err := nargs("fail", args, 1); err != nil {
		return err
	}
	if m, ok := args[0].(*Map); ok {
		return &Raise{Val: m}
	}
	return errors.New(display(args[0]))
}

// status calls f and emits its exit code: 0 if it succeeds, else the code
// of the command that failed, for commands whose 1 is no error:
// match (status { diff -q a b }) { 0 => ..; 1 => ..; c => fail .. }.
// f's output goes on and its values are dropped. A failure with no code (a
// signal, fail, a language error) and Ctrl+C still raise.
func biStatus(in *Interp, io ioCtx, args []Value) error {
	f := args[0]
	switch f.(type) {
	case *Closure, *Builtin:
	default:
		return fmt.Errorf("status needs a fn, got %s", typeName(f))
	}
	fio := io
	fio.vout = nil
	_, err := in.callValue(f, nil, fio, false, Pos{})
	if err == nil {
		return io.emit(&Num{R: ratInt(0)})
	}
	var r *Raise
	if errors.As(err, &r) && !r.intr {
		if m, ok := r.Val.(*Map); ok {
			if n, ok := m.M["code"].(*Num); ok {
				return io.emit(n)
			}
		}
	}
	return err
}

// biSrc returns the source of a fn or lambda, as written.
func biSrc(in *Interp, io ioCtx, args []Value) error {
	switch f := args[0].(type) {
	case *Closure:
		return io.emit(f.Src)
	case *Builtin:
		return fmt.Errorf("src: %s is a builtin; it has no entish source", f.Name)
	}
	return fmt.Errorf("src needs a fn, got a %s", typeName(args[0]))
}

func biNum(in *Interp, io ioCtx, args []Value) error {
	if err := nargs("num", args, 1); err != nil {
		return err
	}
	switch v := args[0].(type) {
	case *Num:
		return io.emit(v)
	case string:
		r, ok := new(big.Rat).SetString(strings.TrimSpace(v))
		if !ok {
			return fmt.Errorf("num: %s is not a number", literal(v))
		}
		return io.emit(&Num{R: r})
	}
	return fmt.Errorf("num needs a str, got %s", typeName(args[0]))
}

// round rounds a num to n decimal places, half away from zero:
// 1 / 3 |> round 4 is 0.3333. The result has an exact decimal, so it can
// be printed or passed to an external.
func biRound(in *Interp, io ioCtx, args []Value) error {
	n, ok := args[0].(*Num)
	if !ok || !n.R.IsInt() || n.R.Sign() < 0 {
		return fmt.Errorf("round takes a whole number of digits >= 0, got %s", display(args[0]))
	}
	x, ok := args[1].(*Num)
	if !ok {
		return fmt.Errorf("round needs a num, got a %s", typeName(args[1]))
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
	return io.emit(&Num{R: new(big.Rat).SetFrac(q, scale)})
}

// keys lists a map's keys in insertion order: [a: 1 b: 2] |> keys is ['a' 'b'].
func biKeys(in *Interp, io ioCtx, args []Value) error {
	m, ok := args[0].(*Map)
	if !ok {
		return fmt.Errorf("keys needs a map, got a %s", typeName(args[0]))
	}
	l := &List{}
	for _, k := range m.Keys {
		l.Items = append(l.Items, k)
	}
	return io.emit(l)
}

// assoc returns a copy of a map with key k set to v: $m |> assoc c 3.
// An existing key keeps its place.
func biAssoc(in *Interp, io ioCtx, args []Value) error {
	k, ok := args[0].(string)
	if !ok {
		return fmt.Errorf("assoc takes a str key, got a %s: use (str $k)", typeName(args[0]))
	}
	m, ok := args[2].(*Map)
	if !ok {
		return fmt.Errorf("assoc needs a map, got a %s", typeName(args[2]))
	}
	out := &Map{Keys: append([]string(nil), m.Keys...), M: maps.Clone(m.M)}
	out.set(k, args[1])
	return io.emit(out)
}

func biStr(in *Interp, io ioCtx, args []Value) error {
	if err := nargs("str", args, 1); err != nil {
		return err
	}
	return io.emit(display(args[0]))
}

func biLen(in *Interp, io ioCtx, args []Value) error {
	if err := nargs("len", args, 1); err != nil {
		return err
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
		return fmt.Errorf("len needs a str, list or map, got %s", typeName(v))
	}
	return io.emit(&Num{R: ratInt(n)})
}

// collection args: (fn, list)

// export sets env vars of the shell: export PATH="$HOME/bin:$PATH" GOOS=linux.
// Every env var is passed to externals; there is no unexported env.
func biExport(in *Interp, io ioCtx, args []Value) error {
	kv := make([][2]string, 0, len(args))
	for _, a := range args {
		s, ok := a.(string)
		if !ok {
			return fmt.Errorf("export takes NAME=value, got a %s", typeName(a))
		}
		name, val, ok := strings.Cut(s, "=")
		if !ok || !isEnvName(name) {
			return fmt.Errorf("export takes NAME=value, got %s", literal(s))
		}
		kv = append(kv, [2]string{name, val})
	}
	for _, p := range kv { // all or nothing
		in.setEnv(p[0], p[1])
	}
	return nil
}

// abbr defines an abbreviation for the editor: abbr gco 'git checkout'.
// Typed where a command can start, the name is replaced by the expansion
// on Space or Enter.
func biAbbr(in *Interp, io ioCtx, args []Value) error {
	name, ok := args[0].(string)
	if !ok || name == "" || quoteWord(name) != name {
		return fmt.Errorf("abbr takes a plain word as its name, got %s", display(args[0]))
	}
	exp, ok := args[1].(string)
	if !ok {
		return fmt.Errorf("abbr takes a str as its expansion, got a %s", typeName(args[1]))
	}
	in.abbrs[name] = exp
	return nil
}

// bind binds a key in the editor: bind ctrl-x { print $it | wl-copy }.
// A fn that takes a param gets the input being edited; a str it returns is
// inserted at the cursor, a map [input: str] replaces the input.
func biBind(in *Interp, io ioCtx, args []Value) error {
	key, ok := args[0].(string)
	if !ok {
		return fmt.Errorf("bind takes a key such as ctrl-x or alt-c, got a %s", typeName(args[0]))
	}
	if err := checkKey(key); err != nil {
		return err
	}
	f, ok := args[1].(*Closure)
	if !ok {
		return fmt.Errorf("bind takes a fn, got a %s", typeName(args[1]))
	}
	if len(f.Params) > 1 {
		return fmt.Errorf("bind takes a fn of at most one param (the input), got %d", len(f.Params))
	}
	in.binds[key] = f
	return nil
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
// they emit the records of a str. With no arg they read stdin, like cat,
// and emit each record as it arrives.
func splitter(name string, delim byte) func(*Interp, ioCtx, []Value) error {
	return func(in *Interp, io ioCtx, args []Value) error {
		if len(args) == 0 {
			return records(io.in, delim, io.emit)
		}
		s, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("%s needs a str, got %s", name, typeName(args[0]))
		}
		return records(strings.NewReader(s), delim, io.emit)
	}
}

// each calls f with each value of its value input as it arrives and emits
// what f emits, so it also maps and filters: |> each { $it * 2 },
// |> each { if $it > 0 { $it } }.
func biEach(in *Interp, io ioCtx, args []Value) error {
	f := args[0]
	switch f.(type) {
	case *Closure, *Builtin:
	default:
		return fmt.Errorf("each needs a fn, got %s", typeName(f))
	}
	fio := io
	fio.vin = nil // the item is f's arg; the rest of the stream is not f's input
	n := 0
	for v, ok := io.vin.next(); ok; v, ok = io.vin.next() {
		n++
		if _, err := in.callValue(f, []Value{v}, fio, false, Pos{}); err != nil {
			addFrame(err, frame{what: fmt.Sprintf("in each, value #%d: %s", n, short(display(v)))})
			return err
		}
	}
	return nil
}

// list emits the values of its value input as one list: ls | lines |> list.
// ...$xs is the reverse.
func biList(in *Interp, io ioCtx, args []Value) error {
	l := &List{}
	for v, ok := io.vin.next(); ok; v, ok = io.vin.next() {
		l.Items = append(l.Items, v)
	}
	return io.emit(l)
}

// count emits the number of values of its value input: ls | lines |> count.
func biCount(in *Interp, io ioCtx, args []Value) error {
	n := 0
	for _, ok := io.vin.next(); ok; _, ok = io.vin.next() {
		n++
	}
	return io.emit(&Num{R: ratInt(n)})
}

// to_json writes a value as compact JSON and a newline, like print:
// [a: [1 $true]] |> to_json writes {"a":[1,true]}. With no arg it writes each
// value of its value input on a line. A num with no exact decimal and a fn
// raise.
func biToJSON(in *Interp, io ioCtx, args []Value) error {
	write := func(v Value) error {
		var b bytes.Buffer
		if err := writeJSON(&b, v); err != nil {
			return fmt.Errorf("to_json: %w", err)
		}
		b.WriteByte('\n')
		_, err := io.out.Write(b.Bytes())
		return err
	}
	if len(args) == 1 {
		return write(args[0])
	}
	for v, ok := io.vin.next(); ok; v, ok = io.vin.next() {
		if err := write(v); err != nil {
			return err
		}
	}
	return nil
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

// from_json emits each JSON value of a str, or of its stdin as it arrives
// (NDJSON streams): objects become maps (keys in order), arrays lists,
// numbers exact nums, null $nil. A key twice raises.
func biFromJSON(in *Interp, ioc ioCtx, args []Value) error {
	src := ioc.in
	if len(args) == 1 {
		s, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("from_json needs a str, got a %s", typeName(args[0]))
		}
		src = strings.NewReader(s)
	}
	d := json.NewDecoder(src)
	d.UseNumber()
	for {
		v, err := readJSON(d)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("from_json: %w", err)
		}
		if err := ioc.emit(v); err != nil {
			return err
		}
	}
}

// as_file copies stdin, byte for byte, to a new temp file and emits its
// path, for commands that only take files: diff (sort a | as_file) b. It
// takes no value: only print and to_json turn values into bytes. The file
// is in $XDG_RUNTIME_DIR if set, mode 0600, and is removed when the input
// ends.
func biAsFile(in *Interp, ioc ioCtx, args []Value) error {
	dir := in.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "entish-*")
	if err != nil {
		return fmt.Errorf("as_file: %w", err)
	}
	in.tmpMu.Lock()
	in.tmps = append(in.tmps, f.Name())
	in.tmpMu.Unlock()
	_, err = io.Copy(f, ioc.in)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return fmt.Errorf("as_file: %w", err)
	}
	return ioc.emit(f.Name())
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
