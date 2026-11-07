// Package repl is the read-eval-print loop. It knows neither the line
// editor nor the language, only these two interfaces, so either side can be
// swapped.
package repl

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type Editor interface {
	// ReadInput reads one complete input; Enter only accepts it when more
	// reports it finished.
	ReadInput(prompt, rprompt, cont string, more func(string) bool) (string, error)
	AddHistory(input string)
}

type Language interface {
	// NeedsMore reports whether src is unfinished and another line is needed.
	NeedsMore(src string) bool
	// Eval runs src and returns the values to show, as text, even when it
	// fails partway.
	Eval(src string) ([]string, error)
}

// ExitCoder is implemented by an error that ends the session (exit).
type ExitCoder interface{ ExitCode() int }

type Config struct {
	Prompt      func() string
	RightPrompt func() string // optional; shown right-aligned on the first line
	Continue    string
	Out, Err    io.Writer
	Interrupted error // returned by the editor on Ctrl+C

	// Optional hooks, e.g. for terminal integration (OSC 133 marks).
	BeforeRead func()           // before each prompt
	BeforeEval func(src string) // an input was accepted and is about to run
	AfterEval  func(err error)  // the input finished
}

// Run loops until EOF or exit and returns the exit code.
func Run(ed Editor, lang Language, cfg Config) int {
	call := func(f func()) {
		if f != nil {
			f()
		}
	}
	for {
		call(cfg.BeforeRead)
		rprompt := ""
		if cfg.RightPrompt != nil {
			rprompt = cfg.RightPrompt()
		}
		src, err := ed.ReadInput(cfg.Prompt(), rprompt, cfg.Continue, lang.NeedsMore)
		switch {
		case err == nil:
		case cfg.Interrupted != nil && errors.Is(err, cfg.Interrupted):
			continue
		case errors.Is(err, io.EOF):
			return 0
		default:
			fmt.Fprintln(cfg.Err, err)
			return 1
		}
		if strings.TrimSpace(src) == "" {
			continue
		}
		ed.AddHistory(src)
		if cfg.BeforeEval != nil {
			cfg.BeforeEval(src)
		}
		vals, err := lang.Eval(src)
		if cfg.AfterEval != nil {
			cfg.AfterEval(err)
		}
		// Values are shown after the command, so they never reach a pipe or
		// a file; ▶ tells them from output.
		for _, v := range vals {
			fmt.Fprintln(cfg.Out, "▶ "+strings.TrimSuffix(v, "\n"))
		}
		if err != nil {
			var ec ExitCoder
			if errors.As(err, &ec) {
				return ec.ExitCode()
			}
			var rep reporter
			if errors.As(err, &rep) {
				fmt.Fprintln(cfg.Err, rep.Report())
			} else {
				fmt.Fprintln(cfg.Err, err)
			}
		}
	}
}

// reporter is an error with a longer form for the user, such as the
// source line it was raised at.
type reporter interface{ Report() string }
