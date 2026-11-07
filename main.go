// Command entish runs the entish shell.
//
//	entish                  interactive REPL (or a script on stdin)
//	entish file [args...]   run a file
//	entish -c src [args...] run src
//
// -x before any of these logs each external command to stderr before it
// runs, like bash -x but only for commands.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aca/entish/complete"
	"github.com/aca/entish/history"
	"github.com/aca/entish/lang"
	"github.com/aca/entish/lineedit"
	"github.com/aca/entish/repl"
)

func main() { os.Exit(run(os.Args[1:])) }

var trace bool

// config is run at interactive startup, before ~/.entrc. Edit config.ent and
// rebuild to change the built-in setup.
//
//go:embed config.ent
var config string

func newInterp(args []string) *lang.Interp {
	o := lang.Options{Args: args, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if trace {
		o.Trace = os.Stderr
	}
	return lang.New(o)
}

func run(args []string) int {
	if len(args) > 0 && args[0] == "-x" {
		trace, args = true, args[1:]
	}
	switch {
	case len(args) > 0 && args[0] == "-c":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: entish -c src [args...]")
			return 2
		}
		return report(newInterp(args[2:]).RunScript(args[1], "-c"))
	case len(args) > 0:
		src, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "entish:", err)
			return 2
		}
		return report(newInterp(args[1:]).RunScript(string(src), args[0]))
	case !lineedit.IsTerminal(os.Stdin):
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "entish:", err)
			return 2
		}
		return report(newInterp(nil).RunScript(string(src), "stdin"))
	}
	return interactive()
}

// exitCode is the status an input or script ends with.
func exitCode(err error) int {
	var exit *lang.ExitError
	var r *lang.Raise
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.Code
	case errors.As(err, &r):
		return r.Code()
	}
	return 2
}

// report prints a script's error and returns the process exit code.
func report(err error) int {
	if err == nil {
		return 0
	}
	var exit *lang.ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	fmt.Fprintln(os.Stderr, "entish:", errText(err))
	var r *lang.Raise
	if errors.As(err, &r) {
		return r.Code()
	}
	return 2
}

// errText is an error as shown to the user: a raise with its source line
// and the fn calls it came out of.
func errText(err error) string {
	var r *lang.Raise
	if errors.As(err, &r) {
		return r.Report()
	}
	return err.Error()
}

func interactive() int {
	in := newInterp(nil)

	// Children share our process group and terminal, so Ctrl+C reaches the
	// running command. The shell itself only notes it; Notify (not Ignore)
	// keeps the default disposition for children.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGQUIT)
	go func() {
		for range sig {
			in.Interrupt()
		}
	}()

	if err := in.RunScript(config, "config.ent"); err != nil {
		fmt.Fprintln(os.Stderr, "entish:", errText(err))
	}
	if rc := rcPath(); rc != "" {
		if src, err := os.ReadFile(rc); err == nil {
			if err := in.RunScript(string(src), rc); err != nil {
				fmt.Fprintln(os.Stderr, "entish:", errText(err))
			}
		}
	}

	var args complete.Args
	if _, err := exec.LookPath("fish"); err == nil {
		args = complete.Fish(in.Cwd, in.Environ)
	}
	opts := lineedit.Options{
		Complete: complete.Shell(in.Cwd, in.Getenv, in.Names, args),
		Abbr:     complete.Abbr(in.Abbr),
		Color:    highlighter(in),
		Pair:     func(buf []rune, pos int) int { return in.MatchBracket(string(buf), pos) },
	}
	k := &keys{in: in}
	opts.Keys = k
	var hs *history.Store
	if path := history.DefaultPath(); path != "" {
		if h, err := history.Open(path, in.Cwd); err != nil {
			fmt.Fprintln(os.Stderr, "entish: history disabled:", err)
		} else {
			defer h.Close()
			hs = h
			opts.History, opts.Suggest, opts.Dirs = h, h.Suggest, &dirs{h: h, k: k}
		}
	}
	ed := lineedit.New(os.Stdin, os.Stdout, opts)
	defer ed.Close()
	t := newTermMarks(os.Stdout, in.Cwd)
	rprompt := func(direnv bool) string { return rightPrompt(int(t.lastCode.Load()), direnv, time.Now()) }
	dh := &direnvHook{in: in, ed: ed, rprompt: rprompt}
	prompt := func() string { return leftPrompt(in.Cwd()) + osc133("B") }
	rightNow := func() string { return rprompt(in.Getenv("DIRENV_DIR") != "") }
	// The interpreter keeps cwd to itself; at the prompt nothing runs, so
	// sync the process cwd for tools that read it (tmux pane_current_path).
	beforeRead := func() {
		os.Chdir(in.Cwd())
		if hs != nil {
			hs.Visit(in.Cwd()) // a failed write only loses this visit
		}
		dh.beforePrompt()
		t.beforePrompt()
	}
	k.after = func() { beforeRead(); ed.SetPrompt(prompt()); ed.SetRightPrompt(rightNow()) }
	return repl.Run(ed, in, repl.Config{
		Prompt:      prompt,
		RightPrompt: rightNow,
		Continue:    "::: ",
		Out:         os.Stdout,
		Err:         os.Stderr,
		Interrupted: lineedit.ErrInterrupted,
		BeforeRead:  beforeRead,
		BeforeEval:  func(src string) { dh.beforeCommand(); t.commandStart(src) },
		AfterEval:   func(err error) { t.commandDone(exitCode(err)) },
	})
}

// keys runs the fns bound with bind for the editor.
type keys struct {
	in    *lang.Interp
	after func() // a binding may cd: redo what happens before a prompt
}

func (k *keys) Bound(key string) bool { return k.in.Bound(key) }

func (k *keys) Run(key, input string) (string, bool) {
	s, replace, err := k.in.RunKey(key, input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "entish:", errText(err))
	}
	k.after()
	return s, replace
}

// dirs feeds the editor's location mode (Ctrl+D) from the history store.
type dirs struct {
	h *history.Store
	k *keys
}

func (d *dirs) Dirs() []lineedit.Dir {
	var out []lineedit.Dir
	for _, v := range d.h.Dirs() {
		out = append(out, lineedit.Dir{Path: v.Path, Time: v.Time})
	}
	return out
}

func (d *dirs) Cd(dir string) {
	if err := d.k.in.Cd(dir); err != nil {
		fmt.Fprintln(os.Stderr, "entish:", err)
	}
	d.k.after()
}

func rcPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".entrc")
}

// spanStyles colors the input: keywords magenta, fns and builtins cyan,
// externals blue, pipes yellow.
var spanStyles = map[lang.SpanKind]string{
	lang.SpanKeyword:  "\x1b[35m",
	lang.SpanFn:       "\x1b[36m",
	lang.SpanExternal: "\x1b[34m",
	lang.SpanPipe:     "\x1b[33m",
}

func highlighter(in *lang.Interp) lineedit.Highlighter {
	return func(buf []rune) []lineedit.Span {
		var out []lineedit.Span
		for _, s := range in.Highlight(string(buf)) {
			out = append(out, lineedit.Span{Start: s.Start, End: s.End, Style: spanStyles[s.Kind]})
		}
		return out
	}
}
