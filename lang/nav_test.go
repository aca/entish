package lang

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNav(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "space and 'quote")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	processCwd, _ := os.Getwd()
	for _, source := range []string{"nav", "nav .", "nav 'space and ''quote'", "fn browse { nav }; browse", "bind alt-n { nav }"} {
		t.Run(source, func(t *testing.T) {
			in, _ := newTest(t)
			if err := in.Cd(base); err != nil {
				t.Fatal(err)
			}
			var start string
			in.navigate = func(s string) (string, bool, error) {
				start = s
				return dest, false, nil
			}
			if err := in.RunScript(source, "t"); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(source, "bind") {
				text, replace, err := in.RunKey("alt-n", "unfinished input")
				if err != nil || text != "" || replace {
					t.Fatalf("binding: %q %v %v", text, replace, err)
				}
			}
			wantStart := base
			if strings.HasPrefix(source, "nav 'space") {
				wantStart = dest
			}
			if start != wantStart || in.Cwd() != dest || in.Getenv("PWD") != dest || in.Getenv("OLDPWD") != base {
				t.Fatalf("start %q, cwd %q, PWD %q, OLDPWD %q", start, in.Cwd(), in.Getenv("PWD"), in.Getenv("OLDPWD"))
			}
		})
	}
	if got, _ := os.Getwd(); got != processCwd {
		t.Fatalf("process cwd changed to %q", got)
	}
}

func TestNavCancelAndFailure(t *testing.T) {
	for _, mode := range []string{"cancel", "interrupt", "error", "missing directory", "no terminal"} {
		t.Run(mode, func(t *testing.T) {
			in, _ := newTest(t)
			cwd, pwd, oldpwd := in.Cwd(), in.Getenv("PWD"), in.Getenv("OLDPWD")
			if mode != "no terminal" {
				in.navigate = func(string) (string, bool, error) {
					switch mode {
					case "interrupt":
						return "", true, nil
					case "error":
						return "", false, fmt.Errorf("cannot open terminal")
					case "missing directory":
						return filepath.Join(t.TempDir(), "gone"), false, nil
					}
					return "", false, nil
				}
			}
			source := "nav"
			if mode == "interrupt" {
				source = "try { nav } catch e { print caught }"
			}
			err := in.RunScript(source, "t")
			if (err == nil) != (mode == "cancel") {
				t.Fatalf("unexpected result: %v", err)
			}
			if mode == "interrupt" {
				var r *Raise
				if !errors.As(err, &r) || !r.intr {
					t.Fatalf("Ctrl+C must escape try: %v", err)
				}
			}
			if in.Cwd() != cwd || in.Getenv("PWD") != pwd || in.Getenv("OLDPWD") != oldpwd {
				t.Fatal("cancel or failure changed cwd/env")
			}
		})
	}
}

func TestNavCompileErrors(t *testing.T) {
	for _, src := range []string{"nav a b", "let x = nav", "nav |> count", "nav | cat", "echo hi | nav", "'dir' |> nav"} {
		t.Run(src, func(t *testing.T) {
			in, _ := newTest(t)
			in.navigate = func(string) (string, bool, error) {
				t.Fatal("invalid program opened navigator")
				return "", false, nil
			}
			if err := in.RunScript(src, "t"); err == nil {
				t.Fatal("expected compile error")
			}
		})
	}
}
