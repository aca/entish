package lang

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Options configures an Interp.
type Options struct {
	Args   []string // script args, visible as $args
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
	Trace  io.Writer // if set, each external command is logged here before it runs
}

type ioCtx struct {
	in       io.Reader
	out, err io.Writer
	onStart  func(*os.Process) // called for each external started (stream cleanup)
	drop     func(Value)       // REPL: a value the typed input drops, to show; nil inside fns
}

// Env is an immutable chain of variable bindings. A node with fns is a block
// activation that holds the block's fns.
type Env struct {
	parent *Env
	name   string
	val    Value
	fns    map[string]*Closure
}

// Interp runs entish programs. The shell's cwd and env live here; the process
// cwd and env are never changed.
type Interp struct {
	cwd    string
	env    map[string]string
	root   *Env // top-level activation
	top    *Env // head of the top-level variable chain
	topFns map[string]*FnDef
	abbrs  map[string]string
	binds  map[string]*Closure
	args   *List
	std    ioCtx
	intr   atomic.Bool
	trace  io.Writer
	traceM sync.Mutex
	envMu  sync.RWMutex // env is also read by background work (completion, direnv)
	tmpMu  sync.Mutex
	tmps   []string // files made by as_file, removed when the input ends
}

func New(o Options) *Interp {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	env["PWD"] = cwd
	args := &List{}
	for _, a := range o.Args {
		args.Items = append(args.Items, a)
	}
	root := &Env{fns: map[string]*Closure{}}
	return &Interp{
		cwd:    cwd,
		env:    env,
		root:   root,
		top:    root,
		topFns: map[string]*FnDef{},
		abbrs:  map[string]string{},
		binds:  map[string]*Closure{},
		args:   args,
		std:    ioCtx{in: o.Stdin, out: o.Stdout, err: o.Stderr},
		trace:  o.Trace,
	}
}

// Interrupt asks the running program to stop at the next statement.
func (in *Interp) Interrupt() { in.intr.Store(true) }

// Cwd is the shell's current directory.
func (in *Interp) Cwd() string { return in.cwd }

// Getenv reads an env var of the shell (not of the process).
func (in *Interp) Getenv(name string) string {
	v, _ := in.lookupEnv(name)
	return v
}

func (in *Interp) lookupEnv(name string) (string, bool) {
	in.envMu.RLock()
	defer in.envMu.RUnlock()
	v, ok := in.env[name]
	return v, ok
}

func (in *Interp) setEnv(kv ...string) {
	in.envMu.Lock()
	defer in.envMu.Unlock()
	for i := 0; i+1 < len(kv); i += 2 {
		in.env[kv[i]] = kv[i+1]
	}
}

// Environ returns the shell's env as NAME=value pairs, sorted.
func (in *Interp) Environ() []string { return in.environ() }

// ApplyEnv sets env vars of the shell; a nil value unsets the var. It is
// how external tools such as direnv change the shell's env.
func (in *Interp) ApplyEnv(changes map[string]*string) {
	in.envMu.Lock()
	defer in.envMu.Unlock()
	for k, v := range changes {
		if v == nil {
			delete(in.env, k)
		} else {
			in.env[k] = *v
		}
	}
}

// Abbr returns the expansion of an abbr defined with the abbr builtin.
func (in *Interp) Abbr(name string) (string, bool) {
	s, ok := in.abbrs[name]
	return s, ok
}

// Cd changes the shell's directory, like the cd builtin.
func (in *Interp) Cd(dir string) error {
	_, err := biCd(in, in.std, []Value{dir})
	return err
}

// Bound reports whether key was bound with the bind builtin.
func (in *Interp) Bound(key string) bool { return in.binds[key] != nil }

// RunKey runs the fn bound to key, given the input being edited if it
// takes a param. A str the fn returns is text to insert at the cursor, a
// map [input: str] replaces the whole input (replace true), $nil does
// nothing.
func (in *Interp) RunKey(key, input string) (text string, replace bool, err error) {
	f := in.binds[key]
	if f == nil {
		return "", false, nil
	}
	var args []Value
	if len(f.Params) > 0 || f.Rest != "" {
		args = []Value{input}
	}
	v, err := in.callValue(f, args, in.std, false, Pos{})
	if err == nil {
		v, err = force(v)
	}
	if err != nil || v == nil {
		return "", false, err
	}
	if s, ok := v.(string); ok {
		return s, false, nil
	}
	if m, ok := v.(*Map); ok {
		s, ok := m.M["input"].(string)
		if !ok || len(m.Keys) != 1 {
			return "", false, fmt.Errorf("bind %s: the fn returned %s; a map must be [input: str], the new input", key, display(v))
		}
		return s, true, nil
	}
	return "", false, fmt.Errorf("bind %s: the fn returned a %s; it must return a str to insert, [input: str] to replace the input, or $nil", key, typeName(v))
}

// Names returns what can start a statement besides externals: keywords,
// builtins and fns defined at the top level so far. For completion.
func (in *Interp) Names() []string {
	var out []string
	for k := range keywords {
		out = append(out, k)
	}
	for b := range builtins {
		out = append(out, b)
	}
	for f := range in.topFns {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// NeedsMore reports whether src is an unfinished statement, so a REPL
// should read another line.
func (in *Interp) NeedsMore(src string) bool {
	if _, err := parse(src, ""); err != nil {
		var e *Error
		return errors.As(err, &e) && e.Incomplete
	}
	// A multiline string stays open until a line that is not part of it.
	lines := strings.Split(src, "\n")
	last := strings.TrimLeft(lines[len(lines)-1], " \t")
	return strings.HasPrefix(last, `\\`) || strings.HasPrefix(last, `$\\`)
}

// Eval runs one REPL input and returns the display forms of the values it
// produced and did not pass on, in order: those of its statements and of
// pipeline stages not followed by |>. Values dropped inside fns are not
// shown.
func (in *Interp) Eval(src string) ([]string, error) {
	vals, err := in.run(src, "", false)
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = display(v)
	}
	return out, err
}

// RunScript runs a whole program, e.g. a file or -c.
func (in *Interp) RunScript(src, file string) error {
	_, err := in.run(src, file, true)
	return err
}

// Check parses and checks a whole program without running it.
func (in *Interp) Check(src, file string) error {
	prog, err := parse(src, file)
	if err == nil {
		err = in.expandSource(prog, in.sourceStack(file))
	}
	if err != nil {
		return err
	}
	return check(prog, in.topFns, nil, true)
}

// run runs a program. At the REPL (!script) it returns the values to show.
func (in *Interp) run(src, file string, script bool) ([]Value, error) {
	defer in.removeTmps()
	prog, err := parse(src, file)
	if err == nil {
		err = in.expandSource(prog, in.sourceStack(file))
	}
	if err != nil {
		return nil, err
	}
	var vars []string
	for e := in.top; e != nil; e = e.parent {
		if e.fns == nil {
			vars = append(vars, e.name)
		}
	}
	if err := check(prog, in.topFns, vars, script); err != nil {
		return nil, err
	}
	var shown []Value
	var mu sync.Mutex // stream producers drop values from their own goroutines
	show := func(v Value) {
		mu.Lock()
		shown = append(shown, v)
		mu.Unlock()
	}
	for _, st := range prog.Stmts {
		if in.intr.Swap(false) {
			return shown, interrupted(prog.Pos)
		}
		io := in.std
		typed := false // typed at the REPL, not a statement of a sourced file
		switch st := st.(type) {
		case *ExprStmt:
			typed = st.X.pos().File == file
		case *LetStmt:
			typed = st.Pos.File == file
		}
		if !script && typed {
			io.drop = show
		}
		v, next, err := in.execStmt(st, in.top, in.root, io, false)
		if err != nil {
			return shown, err
		}
		in.top = next
		if fs, ok := st.(*FnStmt); ok {
			in.topFns[fs.Def.Name] = fs.Def
		}
		if _, ok := st.(*ExprStmt); ok && !script && typed && v != nil {
			show(v)
		}
	}
	return shown, nil
}

func (in *Interp) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(in.cwd, p)
}

func (in *Interp) environ() []string {
	in.envMu.RLock()
	defer in.envMu.RUnlock()
	keys := make([]string, 0, len(in.env))
	for k := range in.env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + in.env[k]
	}
	return out
}

func lookupFn(env *Env, name string) *Closure {
	for e := env; e != nil; e = e.parent {
		if c, ok := e.fns[name]; ok {
			return c
		}
	}
	return nil
}

// ---- errors

// Raise is a raised error. Its value is an error map with keys
// cmd, code, signal, msg, pos.
type Raise struct {
	Val   Value
	cause error
	intr  bool // Ctrl+C: try does not catch it
}

func (r *Raise) Unwrap() error { return r.cause }

func (r *Raise) Error() string {
	if m, ok := r.Val.(*Map); ok {
		msg, _ := m.M["msg"].(string)
		if pos, _ := m.M["pos"].(string); pos != "" {
			return pos + ": " + msg
		}
		return msg
	}
	return display(r.Val)
}

// Code is the exit code a script should end with.
func (r *Raise) Code() int {
	if m, ok := r.Val.(*Map); ok {
		if n, ok := m.M["code"].(*Num); ok && n.R.IsInt() && n.R.Num().IsInt64() {
			if c := int(n.R.Num().Int64()); c != 0 {
				return c
			}
		}
	}
	return 1
}

// ExitError is raised by exit.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit " + strconv.Itoa(e.Code) }
func (e *ExitError) ExitCode() int { return e.Code }

func errMap(pos Pos, cmd, msg string, code *int, sig string) *Map {
	m := newMap()
	if cmd == "" {
		m.set("cmd", nil)
	} else {
		m.set("cmd", cmd)
	}
	if code == nil {
		m.set("code", nil)
	} else {
		m.set("code", &Num{R: ratInt(*code)})
	}
	if sig == "" {
		m.set("signal", nil)
	} else {
		m.set("signal", sig)
	}
	m.set("msg", msg)
	m.set("pos", pos.String())
	return m
}

func raisef(pos Pos, msg string) *Raise { return &Raise{Val: errMap(pos, "", msg, nil, "")} }

func interrupted(pos Pos) *Raise {
	return &Raise{Val: errMap(pos, "", "interrupted", nil, "SIGINT"), intr: true}
}

// wrap turns a plain error from a builtin into a Raise at pos.
func wrap(err error, pos Pos) error {
	if err == nil {
		return nil
	}
	var r *Raise
	var x *ExitError
	if errors.As(err, &r) || errors.As(err, &x) {
		return err
	}
	return &Raise{Val: errMap(pos, "", err.Error(), nil, ""), cause: err}
}
