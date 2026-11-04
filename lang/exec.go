package lang

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// status is how a command ended.
type status struct {
	code int
	sig  string // signal name; "" if the process exited
	cmd  string
	pos  Pos
}

func (s status) ok() bool { return s.code == 0 && s.sig == "" }

func (in *Interp) raiseStatus(s status) error {
	if s.sig != "" {
		return &Raise{Val: errMap(s.pos, s.cmd, fmt.Sprintf("'%s' killed by %s", s.cmd, s.sig), nil, s.sig), intr: s.sig == "SIGINT"}
	}
	code := s.code
	return &Raise{Val: errMap(s.pos, s.cmd, fmt.Sprintf("'%s' failed (exit %d)", s.cmd, code), &code, "")}
}

// pipeline runs stages joined by "|" and "|>". Each operator connects one
// channel: | the stdout of a stage to the stdin of the next, |> the value of
// everything before it to the last arg of the next stage. Stages joined by
// | form a segment; its value is the value of its last stage. A stage's
// stdout that is not piped goes to the pipeline's stdout.
func (in *Interp) pipeline(stages []Expr, ops []string, env *Env, io ioCtx, cond bool) (Value, error) {
	segs := [][]Expr{{stages[0]}}
	for i, op := range ops {
		if op == "|>" {
			segs = append(segs, nil)
		}
		segs[len(segs)-1] = append(segs[len(segs)-1], stages[i+1])
	}
	var carry Value
	has := false
	var streams []*Stream
	defer func() { // stop commands whose output was not read to the end
		for _, s := range streams {
			s.close()
		}
	}()
	track := func(s *Stream) { streams = append(streams, s) }
	for i, seg := range segs {
		v, err := in.segment(seg, env, io, carry, has, cond && i == len(segs)-1, track)
		if err != nil {
			return nil, err
		}
		carry, has = v, true
	}
	return force(carry) // a lazy value never leaves its pipeline
}

// segment runs stages joined by |. carry, if has, is appended to the first
// stage's args. A converter at the end that reads stdin (ls | lines) gets
// the output of the stages before it as a stream, so it handles it as it
// arrives.
func (in *Interp) segment(stages []Expr, env *Env, io ioCtx, carry Value, has, cond bool, track func(*Stream)) (Value, error) {
	n := len(stages)
	if n == 1 {
		return in.stage(stages[0], env, io, carry, has, cond)
	}
	if c, ok := stages[n-1].(*Call); ok && c.stdin {
		s, err := in.startStream(stages[:n-1], env, io, carry, has)
		if err != nil {
			return nil, err
		}
		track(s)
		return in.stage(c, env, io, s, true, false)
	}
	return in.bytePipeline(stages, env, io, carry, has, cond)
}

// stage runs one call or expression and returns its value. A failing
// external raises, or in cond mode gives false.
func (in *Interp) stage(st Expr, env *Env, io ioCtx, carry Value, has, cond bool) (Value, error) {
	c, ok := st.(*Call)
	if !ok {
		return in.eval(st, env, io, cond)
	}
	if c.target == tExternal {
		s, err := in.runExternal(c, env, io)
		switch {
		case err != nil:
			return nil, err
		case s.ok():
			return nil, nil
		case cond && s.sig != "SIGINT":
			return false, nil
		}
		return nil, in.raiseStatus(s)
	}
	fn, err := in.callee(c, env, io)
	if err != nil {
		return nil, err
	}
	args, err := in.fnArgs(c, env, io, carry, has)
	if err != nil {
		return nil, err
	}
	rio, cleanup, err := in.redirect(c.Redirs, env, io)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return in.callValue(fn, args, rio, cond, c.P)
}

// callee is the fn value a call of a fn, builtin or fn variable runs.
func (in *Interp) callee(c *Call, env *Env, io ioCtx) (Value, error) {
	switch c.target {
	case tBuiltin:
		if c.builtin.Name == "eval" {
			return in.compileEval(c, env, io)
		}
		return c.builtin, nil
	case tFn:
		f := lookupFn(env, c.Name)
		if f == nil {
			return nil, raisef(c.P, "fn "+c.Name+" is not defined yet")
		}
		return f, nil
	}
	f, err := in.lookupVar(c.HeadVar, env, io)
	if err != nil {
		return nil, err
	}
	switch f.(type) {
	case *Closure, *Builtin:
		return f, nil
	}
	return nil, raisef(c.P, "$"+c.HeadVar.Name+" is a "+typeName(f)+", not a fn")
}

func (in *Interp) fnArgs(c *Call, env *Env, io ioCtx, carry Value, has bool) ([]Value, error) {
	var args []Value
	for _, w := range c.Args {
		vals, err := in.wordValues(w, env, io)
		if err != nil {
			return nil, err
		}
		args = append(args, vals...)
	}
	if has {
		args = append(args, carry)
	}
	return args, nil
}

func (in *Interp) callValue(f Value, args []Value, io ioCtx, cond bool, pos Pos) (Value, error) {
	if b, ok := f.(*Builtin); !ok || !b.Lazy {
		var err error
		if args, err = forceAll(args); err != nil {
			return nil, err
		}
	}
	switch f := f.(type) {
	case *Closure:
		name := "fn"
		if f.Name != "" {
			name = "fn " + f.Name
		}
		if len(args) < len(f.Params) || f.Rest == "" && len(args) > len(f.Params) {
			if pos == (Pos{}) { // called by a builtin such as each: point at the fn
				pos = f.Body.Pos
			}
			msg := fmt.Sprintf("%s takes %d args, got %d", name, len(f.Params), len(args))
			if f.Name == "" && len(f.Params) == 0 && len(args) == 1 {
				msg += ": use $it or { |x| ... } to take the item"
			}
			return nil, raisef(pos, msg)
		}
		env := f.Env
		for i, p := range f.Params {
			env = &Env{parent: env, name: p, val: args[i]}
		}
		if f.Rest != "" {
			rest := append([]Value(nil), args[len(f.Params):]...)
			env = &Env{parent: env, name: f.Rest, val: &List{Items: rest}}
		}
		io.drop = nil // values a fn drops are its own business
		return in.execBlock(f.Body, env, io, cond)
	case *Builtin:
		v, err := f.Fn(in, io, args)
		return v, wrap(err, pos)
	case *evalFn:
		return in.callValue(f.fn, args[1:], io, cond, pos) // args[0] is the source
	}
	return nil, raisef(pos, typeName(f)+" is not a fn")
}

// evalFn is a compiled `eval src ...args`. It never escapes a stage.
type evalFn struct{ fn *Closure }

// compileEval parses and checks the source of an eval call, like a fn
// `fn _ ...args { src }` at the top level: it sees top-level fns, builtins
// and env vars, never the caller's variables.
func (in *Interp) compileEval(c *Call, env *Env, io ioCtx) (Value, error) {
	vals, err := in.wordValues(c.Args[0], env, io)
	if err != nil {
		return nil, err
	}
	src, ok := vals[0].(string)
	if !ok {
		return nil, raisef(c.P, "eval needs a str, got a "+typeName(vals[0]))
	}
	prog, err := parse(src, "<eval>")
	if err == nil {
		err = in.expandSource(prog, nil)
	}
	if err == nil {
		err = check(prog, in.topFns, nil, false)
	}
	if err == nil {
		ch := &checker{}
		if ch.blockNeedElse(prog); ch.err != nil { // its last statement is the value, as in a fn body
			err = ch.err
		}
	}
	if err != nil {
		return nil, raisef(c.P, "eval: "+err.Error())
	}
	return &evalFn{&Closure{Rest: "args", Body: prog, Env: in.top}}, nil
}

// capture runs x and returns its stdout as a str, like $( ). Its value is
// dropped.
func (in *Interp) capture(x Expr, env *Env, io ioCtx) (Value, error) {
	var buf bytes.Buffer
	cio := io
	cio.out = &buf
	if _, err := in.eval(x, env, cio, false); err != nil {
		return nil, err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// bytePipeline runs a | b | c concurrently and returns the value of the
// last stage.
func (in *Interp) bytePipeline(stages []Expr, env *Env, io ioCtx, carry Value, has, cond bool) (Value, error) {
	n := len(stages)
	sts := make([]status, n)
	vals := make([]Value, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	src := io.in
	for i, st := range stages {
		sio := io
		sio.in = src
		var w, r *os.File
		if i < n-1 {
			pr, pw, err := os.Pipe()
			if err != nil {
				return nil, raisef(st.pos(), err.Error())
			}
			sio.out, w, src = pw, pw, pr
		}
		if i > 0 {
			r = sio.in.(*os.File)
		}
		wg.Add(1)
		go func(i int, st Expr, sio ioCtx, w, r *os.File) {
			defer wg.Done()
			sts[i], vals[i], errs[i] = in.pipeStage(st, env, sio, carry, has && i == 0, i == n-1, cond)
			if errs[i] == nil { // a lazy value may still read the pipe closed below
				vals[i], errs[i] = force(vals[i])
			}
			if w != nil {
				w.Close()
			}
			if r != nil {
				r.Close()
			}
		}(i, st, sio, w, r)
	}
	wg.Wait()
	for i := n - 1; i >= 0; i-- {
		if errs[i] != nil {
			return nil, errs[i]
		}
	}
	// pipefail: the rightmost failure counts; a stage before the last that
	// died of SIGPIPE only lost its reader (`yes | head`).
	for i := n - 1; i >= 0; i-- {
		if sts[i].ok() || i < n-1 && sts[i].sig == "SIGPIPE" {
			continue
		}
		if cond && sts[i].sig != "SIGINT" {
			return false, nil
		}
		return nil, in.raiseStatus(sts[i])
	}
	if io.drop != nil {
		for _, v := range vals[:n-1] { // only their output went on
			if v != nil {
				io.drop(v)
			}
		}
	}
	return vals[n-1], nil
}

// pipeStage runs one stage of a byte pipeline. An external reports its exit
// status, for pipefail; anything else its value.
func (in *Interp) pipeStage(st Expr, env *Env, sio ioCtx, carry Value, has, last, cond bool) (status, Value, error) {
	var s status
	var v Value
	var err error
	if c, ok := st.(*Call); ok && c.target == tExternal {
		s, err = in.runExternal(c, env, sio)
	} else {
		v, err = in.stage(st, env, sio, carry, has, cond && last)
	}
	if !last && errors.Is(err, syscall.EPIPE) {
		err = nil
	}
	return s, v, err
}

// ---- externals

func (in *Interp) runExternal(c *Call, env *Env, io ioCtx) (status, error) {
	// Resolve the command before evaluating args, so a missing command fails
	// before $( ) args run.
	name := c.Name
	if c.HeadVar != nil {
		v, err := in.lookupVar(c.HeadVar, env, io)
		if err != nil {
			return status{}, err
		}
		s, ok := v.(string)
		switch {
		case !ok:
			return status{}, raisef(c.P, "$"+c.HeadVar.Name+" is a "+typeName(v)+", not a command name")
		case s == "":
			return status{}, raisef(c.P, "$"+c.HeadVar.Name+" is an empty command name")
		}
		name = s
	}
	path, err := in.lookPath(name)
	if err != nil {
		code := 127
		return status{}, &Raise{Val: errMap(c.P, name, err.Error(), &code, "")}
	}
	argv := []string{name}
	for _, w := range c.Args {
		vals, err := in.wordValues(w, env, io)
		if err != nil {
			return status{}, err
		}
		for _, v := range vals {
			s, err := toArg(v)
			if err != nil {
				return status{}, raisef(w.Pos, err.Error())
			}
			argv = append(argv, s)
		}
	}
	rio, cleanup, err := in.redirect(c.Redirs, env, io)
	if err != nil {
		return status{}, err
	}
	defer cleanup()
	in.traceCmd(argv, c.Redirs, rio)
	cmd := &exec.Cmd{Path: path, Args: argv, Dir: in.cwd, Env: in.environ(), Stdin: rio.in, Stdout: rio.out, Stderr: rio.err}
	if err := cmd.Start(); err != nil {
		return status{}, raisef(c.P, err.Error())
	}
	if rio.onStart != nil {
		rio.onStart(cmd.Process)
	}
	cmd.Wait()
	in.intr.Store(false) // a Ctrl+C during the command went to the command
	s := status{cmd: strings.Join(argv, " "), pos: c.P}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		s.sig = sigName(ws.Signal())
	} else {
		s.code = cmd.ProcessState.ExitCode()
	}
	return s, nil
}

func (in *Interp) lookPath(name string) (string, error) {
	if rest, ok := strings.CutPrefix(name, "~/"); ok {
		home, err := in.home()
		if err != nil {
			return "", err
		}
		name = filepath.Join(home, rest)
	}
	if strings.Contains(name, "/") {
		if p := in.abs(name); isExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("%s: no such executable", name)
	}
	for _, dir := range filepath.SplitList(in.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		if p := filepath.Join(in.abs(dir), name); isExecutable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: command not found", name)
}

// home is what ~ stands for. An empty $HOME is an error, so ~/x never
// becomes /x.
func (in *Interp) home() (string, error) {
	if h := in.Getenv("HOME"); h != "" {
		return h, nil
	}
	return "", fmt.Errorf("~: $HOME is not set")
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func (in *Interp) redirect(rs []*Redir, env *Env, io ioCtx) (ioCtx, func(), error) {
	var files []*os.File
	cleanup := func() {
		for _, f := range files {
			f.Close()
		}
	}
	for _, r := range rs {
		switch r.Op {
		case "2>&1":
			io.err = io.out
			continue
		case ">&2":
			io.out = io.err
			continue
		}
		v, err := in.wordValue(r.Target, env, io)
		if err != nil {
			cleanup()
			return io, nil, err
		}
		name, err := toArg(v)
		if err != nil {
			cleanup()
			return io, nil, raisef(r.Target.Pos, err.Error())
		}
		flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		switch r.Op {
		case ">>", "2>>":
			flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		case "<":
			flag = os.O_RDONLY
		}
		f, err := os.OpenFile(in.abs(name), flag, 0o666)
		if err != nil {
			cleanup()
			return io, nil, raisef(r.Target.Pos, err.Error())
		}
		files = append(files, f)
		switch r.Op {
		case "<":
			io.in = f
		case "2>", "2>>":
			io.err = f
		default:
			io.out = f
		}
	}
	return io, cleanup, nil
}

var sigNames = map[syscall.Signal]string{
	syscall.SIGHUP: "SIGHUP", syscall.SIGINT: "SIGINT", syscall.SIGQUIT: "SIGQUIT",
	syscall.SIGILL: "SIGILL", syscall.SIGABRT: "SIGABRT", syscall.SIGBUS: "SIGBUS",
	syscall.SIGFPE: "SIGFPE", syscall.SIGKILL: "SIGKILL", syscall.SIGSEGV: "SIGSEGV",
	syscall.SIGPIPE: "SIGPIPE", syscall.SIGALRM: "SIGALRM", syscall.SIGTERM: "SIGTERM",
	syscall.SIGUSR1: "SIGUSR1", syscall.SIGUSR2: "SIGUSR2",
}

func sigName(s syscall.Signal) string {
	if n, ok := sigNames[s]; ok {
		return n
	}
	return fmt.Sprintf("SIG%d", int(s))
}

// traceCmd logs an external command as copy-pastable entish source:
// "+ git commit -m 'fix it' > log.txt". Stages of a pipeline run
// concurrently, so their lines may appear in any order.
func (in *Interp) traceCmd(argv []string, rs []*Redir, rio ioCtx) {
	if in.trace == nil {
		return
	}
	parts := make([]string, 0, len(argv)+len(rs))
	for _, a := range argv {
		parts = append(parts, quoteWord(a))
	}
	for _, r := range rs {
		if r.Target == nil {
			parts = append(parts, r.Op)
			continue
		}
		var name string
		switch r.Op {
		case "<":
			name = fileName(rio.in)
		case "2>", "2>>":
			name = fileName(rio.err)
		default:
			name = fileName(rio.out)
		}
		parts = append(parts, r.Op, quoteWord(name))
	}
	in.traceM.Lock()
	defer in.traceM.Unlock()
	fmt.Fprintln(in.trace, "+ "+strings.Join(parts, " "))
}

func fileName(v any) string {
	if f, ok := v.(*os.File); ok {
		return f.Name()
	}
	return "?"
}

// quoteWord quotes s with '...' when it is not a plain word.
func quoteWord(s string) string {
	if s == "" {
		return "''"
	}
	for _, c := range s {
		if !(isIdent(c) || strings.ContainsRune("-./:=@%+,^~", c)) {
			return "'" + strings.ReplaceAll(s, "'", "''") + "'"
		}
	}
	return s
}
