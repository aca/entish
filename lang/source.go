package lang

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// expandSource replaces each top-level `source path` statement of prog by
// the statements of that file, read and parsed now. The file is checked
// with the program, and its fns and lets are visible after the statement.
// A relative path is relative to the file the statement is in; for the
// REPL, -c, stdin and eval, to the shell's cwd. stack is the files being
// sourced, to report cycles.
func (in *Interp) expandSource(prog *Block, stack []string) error {
	if slices.ContainsFunc(prog.Fns, func(d *FnDef) bool { return d.Name == "source" }) || in.topFns["source"] != nil {
		return nil // a fn named source shadows the builtin
	}
	var stmts []Stmt
	for _, st := range prog.Stmts {
		var call *Call
		if es, ok := st.(*ExprStmt); ok {
			call, _ = es.X.(*Call)
		}
		if call == nil || call.Name != "source" || call.Caret || call.HeadVar != nil {
			stmts = append(stmts, st)
			continue
		}
		path, err := in.sourcePath(call)
		if err != nil {
			return err
		}
		if i := slices.Index(stack, path); i >= 0 {
			return &Error{Pos: call.P, Msg: "source cycle: " + strings.Join(append(stack[i:], path), " -> ")}
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return &Error{Pos: call.P, Msg: "source: " + err.Error()}
		}
		sub, err := parse(string(src), path)
		if err != nil {
			return err
		}
		if err := in.expandSource(sub, append(stack, path)); err != nil {
			return err
		}
		for _, d := range sub.Fns {
			if d.Name == "source" {
				return &Error{Pos: d.Pos, Msg: "a sourced file cannot define fn source: it would change what source means in the file that sources it"}
			}
		}
		stmts = append(stmts, sub.Stmts...)
		prog.Fns = append(prog.Fns, sub.Fns...)
	}
	prog.Stmts = stmts
	return nil
}

// sourcePath is the absolute path of `source path`. The path must be
// literal, so the file is known before the program runs.
func (in *Interp) sourcePath(call *Call) (string, error) {
	bad := &Error{Pos: call.P, Msg: "source takes one literal path: source ~/lib.ent"}
	if len(call.Args) != 1 || len(call.Redirs) > 0 {
		return "", bad
	}
	var b strings.Builder
	for i, a := range call.Args[0].Parts {
		switch a := a.(type) {
		case *LitAtom:
			if a.Glob {
				return "", bad
			}
			b.WriteString(a.S)
		case *StrAtom:
			for _, p := range a.Parts {
				if p.Var != nil {
					return "", bad
				}
				b.WriteString(p.Lit)
			}
		case *HomeAtom:
			if i > 0 {
				return "", bad
			}
			home, err := in.home()
			if err != nil {
				return "", &Error{Pos: call.P, Msg: err.Error()}
			}
			b.WriteString(home)
		default:
			return "", bad
		}
	}
	p := b.String()
	if f := call.P.File; !pseudoFile(f) && !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(f), p)
	}
	return filepath.Clean(in.abs(p)), nil
}

// pseudoFile: f names no file, so relative paths are relative to the cwd.
func pseudoFile(f string) bool {
	switch f {
	case "", "-c", "stdin", "<eval>":
		return true
	}
	return false
}

// sourceStack is the stack expandSource starts with for a program in file.
func (in *Interp) sourceStack(file string) []string {
	if pseudoFile(file) {
		return nil
	}
	return []string{filepath.Clean(in.abs(file))}
}
