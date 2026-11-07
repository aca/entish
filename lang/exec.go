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
	"time"
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
		return &Raise{Val: errMap(s.pos, s.cmd, fmt.Sprintf("'%s' killed by %s", s.cmd, s.sig), nil, s.sig), intr: s.sig == "SIGINT", at: s.pos}
	}
	code := s.code
	return &Raise{Val: errMap(s.pos, s.cmd, fmt.Sprintf("'%s' failed (exit %d)", s.cmd, code), &code, ""), at: s.pos}
}

// condFalse is the status of a call that, in a condition, failed without
// raising: a fn whose last command failed.
var condFalse = status{code: 1}

// result is what a single stage's status means: ok, false in a condition,
// or a raise.
func (in *Interp) result(s status, cond bool) (bool, error) {
	switch {
	case s.ok():
		return true, nil
	case s.cmd == "": // condFalse
		return false, nil
	case cond && s.sig != "SIGINT":
		return false, nil
	}
	return false, in.raiseStatus(s)
}

// pipeline runs stages joined by "|" and "|>", all at once. Each operator
// connects one stream: | the output of a stage to the stdin of the next, |>
// its values to the value input of the next. The other stream of a stage
// is not passed on: before |, its values are dropped; before |>, its output
// goes to the pipeline's output. The last stage's values are the
// pipeline's. A stage that uses $_ runs once per value instead (topicStage).
func (in *Interp) pipeline(stages []Expr, ops []string, topic []bool, env *Env, io ioCtx, cond bool) (bool, error) {
	n := len(stages)
	if n == 1 {
		s, err := in.stage(stages[0], env, io, cond)
		if err != nil {
			return false, err
		}
		return in.result(s, cond)
	}
	runs := make([]*stageRun, n)
	sios := make([]ioCtx, n)
	after := make([][]func(), n) // closes a stage's ends of its pipes
	for i := range stages {
		r := &stageRun{}
		runs[i] = r
		sios[i] = io
		if i > 0 {
			sios[i].vin = nil
		}
		sios[i].onStart = func(p *os.Process) {
			r.add(p)
			if io.onStart != nil {
				io.onStart(p)
			}
		}
	}
	for i, op := range ops {
		if op == "|>" {
			vp := newVpipe()
			sios[i].vout, sios[i+1].vin = vp.send, vp
			after[i] = append(after[i], vp.closeW)
			after[i+1] = append(after[i+1], vp.closeR)
			continue
		}
		pr, pw, err := os.Pipe()
		if err != nil {
			for _, fs := range after {
				for _, f := range fs {
					f()
				}
			}
			return false, raisef(stages[i].pos(), err.Error())
		}
		sios[i].out, sios[i+1].in = pw, pr
		sios[i].vout = nil
		after[i] = append(after[i], func() { pw.Close() })
		after[i+1] = append(after[i+1], func() { pr.Close() })
	}
	var wg sync.WaitGroup
	lastDone, allDone := make(chan struct{}), make(chan struct{})
	for i, st := range stages {
		wg.Add(1)
		go func(i int, st Expr) {
			defer wg.Done()
			r := runs[i]
			if i < len(topic) && topic[i] {
				r.s, r.err = in.topicStage(st, env, sios[i], cond && i == n-1)
			} else {
				r.s, r.err = in.stage(st, env, sios[i], cond && i == n-1)
			}
			r.finish()
			for _, f := range after[i] {
				f()
			}
			if i == n-1 {
				close(lastDone)
			}
		}(i, st)
	}
	// When the last stage is done, one before it that is still running lost
	// its reader. It usually ends at once (SIGPIPE, EPIPE or a closed value
	// pipe on its next write); one that doesn't write, e.g. reading a
	// terminal, is killed.
	go func() {
		<-lastDone
		select {
		case <-allDone:
		case <-time.After(200 * time.Millisecond):
			for _, r := range runs {
				r.kill()
			}
		}
	}()
	wg.Wait()
	close(allDone)
	for i := n - 1; i >= 0; i-- {
		r := runs[i]
		if r.err != nil && !(i < n-1 && (r.killed || lostReader(r.err))) {
			return false, markStages(r.err, stages, runs)
		}
	}
	// pipefail: the rightmost failure counts; a stage before the last that
	// died of SIGPIPE or was killed only lost its reader (`yes | head`).
	for i := n - 1; i >= 0; i-- {
		r := runs[i]
		if r.s.ok() || i < n-1 && (r.s.sig == "SIGPIPE" || r.killed || r.err != nil) {
			continue
		}
		ok, err := in.result(r.s, cond)
		return ok, markStages(err, stages, runs)
	}
	return true, nil
}

// markStages notes on a raise from a pipeline which of its stages failed,
// for Report. Only a raise that came from a stage itself, not from a fn
// it called: that one is shown at its own pipeline.
func markStages(err error, stages []Expr, runs []*stageRun) error {
	var r *Raise
	if !errors.As(err, &r) || r.marks != nil || r.trace != nil {
		return err
	}
	n := len(runs)
	for i, run := range runs {
		var label string
		switch {
		case i < n-1 && (run.killed || run.s.sig == "SIGPIPE" || lostReader(run.err)):
			continue // lost its reader
		case run.err != nil:
			label = "error"
		case run.s.sig != "":
			label = "killed by " + run.s.sig
		case run.s.ok() || run.s.cmd == "":
			continue
		default:
			label = fmt.Sprintf("exit %d", run.s.code)
		}
		r.marks = append(r.marks, mark{at: stages[i].pos(), label: label})
	}
	return err
}

// stageRun tracks the externals a running stage started, to kill them if
// the stage outlives its reader.
type stageRun struct {
	s   status
	err error

	mu     sync.Mutex
	procs  []*os.Process
	done   bool
	killed bool
}

func (r *stageRun) add(p *os.Process) {
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.mu.Unlock()
}

func (r *stageRun) finish() {
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
}

func (r *stageRun) kill() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.killed = true
	for _, p := range r.procs {
		p.Kill() // no-op for a process already waited for
	}
}

func isEPIPE(err error) bool { return errors.Is(err, syscall.EPIPE) }

// topicStage runs st once per value of its value input, with $_ bound to
// the value, as each runs a block: |> rm $_ is |> each { rm $it }. It stops
// at the first value it fails on.
func (in *Interp) topicStage(st Expr, env *Env, io ioCtx, cond bool) (status, error) {
	tio := io
	tio.vin = nil // the item is $_; the rest of the stream is not its input
	n := 0
	for v, ok := io.vin.next(); ok; v, ok = io.vin.next() {
		n++
		s, err := in.stage(st, &Env{parent: env, name: "_", val: v}, tio, cond)
		if err != nil {
			addFrame(err, frame{what: fmt.Sprintf("in a $_ stage, value #%d: %s", n, short(display(v)))})
			return s, err
		}
		if !s.ok() {
			return s, nil
		}
	}
	return status{}, nil
}

// stage runs one call or expression. An external reports its exit status;
// anything else reports condFalse if, in cond mode, it failed.
func (in *Interp) stage(st Expr, env *Env, io ioCtx, cond bool) (status, error) {
	c, ok := st.(*Call)
	if !ok {
		ok, err := in.emit(st, env, io, cond)
		return okStatus(ok), err
	}
	if c.target == tExternal {
		return in.runExternal(c, env, io)
	}
	fn, err := in.callee(c, env, io)
	if err != nil {
		return status{}, err
	}
	args, err := in.fnArgs(c, env, io)
	if err != nil {
		return status{}, err
	}
	rio, cleanup, err := in.redirect(c.Redirs, env, io)
	if err != nil {
		return status{}, err
	}
	defer cleanup()
	ok, err = in.callValue(fn, args, rio, cond, c.P)
	return okStatus(ok), err
}

func okStatus(ok bool) status {
	if ok {
		return status{}
	}
	return condFalse
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
	var f Value
	var err error
	if c.HeadAtom != nil {
		f, err = in.atomValue(c.HeadAtom, env, io)
	} else {
		f, err = in.lookupVar(c.HeadVar, env, io)
	}
	if err != nil {
		return nil, err
	}
	switch f.(type) {
	case *Closure, *Builtin:
		return f, nil
	}
	return nil, raisef(c.P, callName(c)+" is a "+typeName(f)+", not a fn")
}

func (in *Interp) fnArgs(c *Call, env *Env, io ioCtx) ([]Value, error) {
	var args []Value
	for _, w := range c.Args {
		vals, err := in.wordValues(w, env, io)
		if err != nil {
			return nil, err
		}
		args = append(args, vals...)
	}
	return args, nil
}

// callValue calls f. Its body reads the stdin and value input of io and
// emits to its value output.
func (in *Interp) callValue(f Value, args []Value, io ioCtx, cond bool, pos Pos) (bool, error) {
	switch f := f.(type) {
	case *Closure:
		name := "fn"
		if f.Name != "" {
			name = "fn " + f.Name
		}
		if len(args) < len(f.Params) || f.Rest == "" && !f.AnyArgs && len(args) > len(f.Params) {
			if pos == (Pos{}) { // called by a builtin such as each: point at the fn
				pos = f.Body.Pos
			}
			return false, raisef(pos, fmt.Sprintf("%s takes %d args, got %d", name, len(f.Params), len(args)))
		}
		env := f.Env
		for i, p := range f.Params {
			env = &Env{parent: env, name: p, val: args[i]}
		}
		if f.Rest != "" {
			rest := append([]Value(nil), args[len(f.Params):]...)
			env = &Env{parent: env, name: f.Rest, val: &List{Items: rest}}
		}
		ok, err := in.execBlock(f.Body, env, io, cond)
		if err != nil && pos != (Pos{}) { // a builtin caller adds its own frame
			addFrame(err, frame{what: "in " + name, at: pos})
		}
		return ok, err
	case *Builtin:
		return true, wrap(f.Fn(in, io, args), pos)
	case *evalFn:
		return in.callValue(f.fn, args[1:], io, cond, pos) // args[0] is the source
	}
	return false, raisef(pos, typeName(f)+" is not a fn")
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
	if len(vals) != 1 {
		return nil, raisef(c.P, fmt.Sprintf("eval needs one str, got %d values", len(vals)))
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
		err = check(prog, in.topFns, nil)
	}
	if err != nil {
		return nil, raisef(c.P, "eval: "+err.Error())
	}
	return &evalFn{&Closure{Rest: "args", Body: prog, Env: in.top}}, nil
}

// capture runs x and returns its output as a str, like $( ). Its values
// are dropped.
func (in *Interp) capture(x Expr, env *Env, io ioCtx) (Value, error) {
	var buf bytes.Buffer
	cio := io
	cio.out = &buf
	cio.vout = nil
	if _, err := in.emit(x, env, cio, false); err != nil {
		return nil, err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
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
		return status{}, &Raise{Val: errMap(c.P, name, err.Error(), &code, ""), at: c.P}
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
