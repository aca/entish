package lang

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTest returns an Interp whose stdout goes to a temp file.
func newTest(t *testing.T) (*Interp, func() string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	in := New(Options{Stdin: os.Stdin, Stdout: f, Stderr: f})
	in.cwd = t.TempDir()
	read := func() string {
		b, _ := os.ReadFile(f.Name())
		f.Truncate(0)
		f.Seek(0, 0)
		return string(b)
	}
	return in, read
}

func TestEvalPrints(t *testing.T) {
	cases := []struct{ src, want string }{
		{"1 + 2", "3"},
		{"0.1 + 0.2 == 0.3", "$true"},
		{"7 / 2", "3.5"},
		{"1 / 3", "1/3"},
		{"'it''s'", "it's"},
		{"\"a \\\n  b\"", "a   b"}, // \ at a line's end continues a "..." string
		{"\"a \\\r\nb\"", "a b"},
		{"[1 'a' [x: 2]]", "[1 'a' [x: 2]]"},
		{"fn xx a b { [$a $b] }; (xx 3 4)[1]", "4"},
		{"([1 [2 3]])[1][0]", "2"},
		{"([1 2 3])[1:]", "[2 3]"},
		{"([k: 'v'])[k]", "v"},
		{"let i = 1; ([7 8])[$i]", "8"},
		{"echo hi | lines", "hi"},
		{"fn f { print x; 42 }; f", "42"},
		{"fn f { ^true }; f", ""},
		{"(...[1 2 3] |> each {|x| $x * 10 } |> list)", "[10 20 30]"},
		{"(...[1 2 3 4] |> each {|x| if $x % 2 == 0 { $x } } |> list)", "[2 4]"},
		{"(...[1 2 3] |> each { $it * $it } |> list)", "[1 4 9]"},
		{"fn f { 'a'; 'b' }; (f |> list)", "['a' 'b']"},
		{"...[1 2]", "2"},
		{"if 1 < 2 { 'yes' } else { 'no' }", "yes"},
		{"try { false } catch e { $e[code] }", "1"},
		{"try { sh -c 'kill -TERM $$' } catch e { $e[signal] }", "SIGTERM"},
		{"let x = 1", ""},
		{"round 4 (1 / 3)", "0.3333"},
		{"round 0 (-5 / 2)", "-3"},
		{"round 2 0.125", "0.13"},
		{"$(to_json [a: [1 $true $nil]  'b c': 'x\"<y>'  f: (7 / 2)])", `{"a":[1,true,null],"b c":"x\"<y>","f":3.5}`},
		{"$(to_json 0755)", "755"},
		{"$(to_json [[:] []])", "[{},[]]"},
		{"$(...[1 'a'] |> to_json)", "1\n\"a\""},
		{`from_json '{"b": [1, 1e-3], "a": {"x": null}}'`, "[b: [1 0.001]  a: [x: $nil]]"},
		{`(from_json '0.1') + 0.2 == 0.3`, "$true"},
		{`(from_json '1 [2]' |> list)`, "[1 [2]]"},
		{"let x = [a: [1 2]  b: 'q']; (to_json $x | from_json) == $x", "$true"},
		{"eval '1 + 2'", "3"},
		{"eval '$args' 'a b' 2", "['a b' 2]"},
		{"...[1 2] |> eval 'count'", "2"},
		{"fn sq x { $x * $x }\neval 'sq 7'", "49"},
		{"let src = 'echo hi'; eval $src | lines", "hi"},
		{"let n = 2; eval \"$n + 1\"", "3"},
		{"eval $(echo 1)", "1"},
		{"let c = 'echo'; ^$c hi | lines", "hi"},
		{"let cs = ['echo']; ^$cs[0] hi | lines", "hi"},
		{"let d = eval '{ |x| $x * 2 }'; $d 21", "42"},
		{"if eval 'false' { 1 } else { 2 }", "2"},
		{"let xs = [10 20 30 40]; $xs[1:3]", "[20 30]"},
		{"let xs = [10 20 30 40]; $xs[:1]", "[10]"},
		{"let xs = [10 20 30 40]; $xs[4:]", "[]"},
		{"let xs = [10 20 30 40]; let i = 2; $xs[$i:]", "[30 40]"},
		{"let xs = [10 20 30 40]; $xs[(len $xs) - 1]", "40"},
		{"let xs = [10 20 30 40]; let i = 1; $xs[$i + 1]", "30"},
		{"let xs = [10 20 30 40]; let i = 1; $xs[$i + 1:4]", "[30 40]"},
		{"let xs = [10 20 30 40]; $xs[1:(len $xs) - 1]", "[20 30]"},
		{"let m = [a: 1]; $m[('a')]", "1"},
		{"let m = [1: 'x']; $m[1]", "x"},
		{"keys [a: 1 b: 2]", "['a' 'b']"},
		{"let m = [a: 1 b: 2]; assoc a 9 $m", "[a: 9  b: 2]"},
		{"let m = [a: 1]; let n = assoc b 2 $m; $m", "[a: 1]"},
		{"fn double x { $x * 2 }; (...[1 2 3] |> each $double |> list)", "[2 4 6]"},
		{"(...['a b' 'c'] |> each $lines |> list)", "['a b' 'c']"},
		{"fn adder n { {|x| $x + $n } }; let add3 = adder 3; (...[1 2] |> each $add3 |> list)", "[4 5]"},
		{"fn fact n { if $n <= 1 { 1 } else { $n * ($fact ($n - 1)) } }; $fact 5", "120"},
		{"fn f { 1 }; let f = 2; $f", "2"},
		{"let f = 2; fn f { 1 }; $f", "<fn f>"},
		{"let lines = 3; $lines", "3"},
		{"let g = { 41 + 1 }; call $g", "42"},
		{"let f = { false }; if call $f { 'y' } else { 'n' }", "n"},
		{"echo hi", ""}, // commands print themselves; nothing extra
		{"match 1 { 1 => 'one'; _ => 'other' }", "one"},
		{"match 'x' { 'y' => 1; s => $s }", "x"},
		{"match [1 2 3] { [a ...rest] => [$a $rest] }", "[1 [2 3]]"},
		{"match [1 2 3] { [a b] => 1; [a ..._] => $a }", "1"},
		{"match [] { [x ..._] => 1; [] => 'empty' }", "empty"},
		{"match [a: 1 b: 2] { [b: x] => $x }", "2"},
		{"match [a: 1] { [b: x] => 1; [:] => 'map' }", "map"},
		{"match [a: [1 'q']] { [a: [1 s]] => $s }", "q"},
		{"match 5 { n if $n > 9 => 'big'; n if $n > 3 => 'mid'; _ => 'small' }", "mid"},
		{"let want = 'b'; match 'b' { $want => 'same'; _ => 'diff' }", "same"},
		{"match $nil { $nil => 'nil' }", "nil"},
		{"match '1' { 1 => 'num'; _ => 'str' }", "str"}, // no conversion, and no raise
		{"match [1] { ['1'] => 'str'; [1] => 'num' }", "num"},
		{"let x = 1; match 2 { x => $x }", "2"},
		{"match 1 { _ => { let y = 2; $y * 3 } }", "6"},
		{"match 1 {\n  1 => print one\n  _ => print other\n}", ""},
		{"match 1 {\n  1 => {\n    'next line'\n  }\n  _ => 'other'\n}", "next line"},
		{"(...[1 2] |> each { match $it { 1 => 'a'; _ => 'b' } } |> list)", "['a' 'b']"},
		{"if match 1 { 1 => $true; _ => $false } { 'y' } else { 'n' }", "y"},
		{"src { $it + 1 }", "{ $it + 1 }"},
		{"fn add a b { $a + $b }; src $add", "fn add a b { $a + $b }"},
		{"let s = src {|x| $x * 2 }; let f = eval $s; $f 21", "42"},
		{"echo a \\\n  b | lines |> list", "['a b']"},
		{"echo a \\  \r\n  b \\\n  c | lines |> list", "['a b c']"},
		{"if $true { 1 } \\\nelse { 2 }", "1"},
		{"let s = (\n  \\\\a \\\n)\n$s", "a \\"},
		{"1 # x \\\n2", "2"},
	}
	for _, c := range cases {
		in, _ := newTest(t)
		got, err := evalLast(in, c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %q, want %q", c.src, got, c.want)
		}
	}
}

// evalLast runs a REPL input and returns the last value it shows.
func evalLast(in *Interp, src string) (string, error) {
	vals, err := in.Eval(src)
	if len(vals) == 0 {
		return "", err
	}
	return vals[len(vals)-1], err
}

// TestReplShows checks which values the REPL shows: those of statements
// and of stages whose value is not passed on with |>, but none from inside
// fns.
func TestReplShows(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{"len [1 2]", []string{"2"}},
		{"'a'; ...[1 2]", []string{"a", "1", "2"}},
		{"let x = 1", nil},
		{"print hi", nil},
		{"fn f { 1 }; f | cat", nil},
		{"fn f { 1 }; f; f", []string{"1", "1"}},
		{"fn f { 1 }; f | cat | lines", nil},
		{"fn f { count }; [2] |> f | cat", nil},
		{"fn f { 1 }; fn g { f | cat; f; 2 }; g", []string{"1", "2"}},
		{"fn f { 1 }; ...[1] |> each {|x| f | cat; $x }", []string{"1"}},
		{"fn f { 1 }; if $true { f | cat }", nil},
		{"fn f { 1 }; match 1 { 1 => { f | cat }; _ => print }", nil},
		{"fn f { 1 }; let s = $(f | cat)", nil},
	}
	for _, c := range cases {
		in, _ := newTest(t)
		got, err := in.Eval(c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q shows %q, want %q", c.src, got, c.want)
		}
	}
}

func TestReplPersists(t *testing.T) {
	in, _ := newTest(t)
	for _, src := range []string{"let x = 20", "fn add a b { $a + $b }", "let y = add $x 1"} {
		if _, err := in.Eval(src); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
	}
	if got, _ := evalLast(in, "$y"); got != "21" {
		t.Errorf("$y = %q", got)
	}
	if got, err := evalLast(in, "...[1] |> each {|v| $add $v 1 }"); got != "2" {
		t.Errorf("$add from an earlier input = %q, %v", got, err)
	}
}

func TestNeedsMore(t *testing.T) {
	in, _ := newTest(t)
	more := []string{"print \"a \\", "print \"a \\\n", "if $true {", "let x =", "ls |", "[1 2", "print (1 +", `\\a`, "fn f {", "match $x {", "match 1 { 1 => {", "match 1 { [1", "echo a \\", "echo a \\  "}
	done := []string{"print hi", "if $true { 1 }", "\\\\a\n", "'unterminated", "match 1 { 1 =>"}
	for _, s := range more {
		if !in.NeedsMore(s) {
			t.Errorf("NeedsMore(%q) = false", s)
		}
	}
	for _, s := range done {
		if in.NeedsMore(s) {
			t.Errorf("NeedsMore(%q) = true", s)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	cases := map[string]string{
		"let x = ls":              "ls has no value",
		"[1] |> jq .":             "jq reads stdin, not values",
		"ls |> lines":             "ls has no value; pipe its output with |: ls | lines",
		"print x |> each { $it }": "print has no value",
		"let s = to_json [1]":     "to_json has no value",
		"let x = (print 1)":       "print has no value",
		"ls | lines a b":          "lines takes 0 to 1 args, got 2",
		"ls | lines | cat":        "cannot be piped",
		"ls | len":                "len does not read stdin; it takes its data as an arg: len $x",
		"ls | print":              "print does not read stdin; read its output as lines: ls | lines |> print",
		"1 | to_json":             "a value cannot be piped into |; pass it with |>: $x |> to_json",
		"1 | cat":                 "a value cannot be piped into |; print it: $x |> print | cat",
		"fn f { 1 }\n1 | f":       "a value cannot be piped into |; pass it with |>: $x |> f, or print it: $x |> print | f",
		"fn f { 1 }\nf | count":   "count does not read stdin; pass f's values with |>: f |> count, or read its output as lines: f | lines |> count",
		"ls |> jq":                "ls has no value; pipe its output with |: ls | jq",
		"ls |> count":             "ls has no value; make its output values: ls | lines |> count",
		"fn f { 1 }\nf |> cat":    "cat reads stdin, not values; pipe f's output with |: f | cat, or pass each value as an arg with $_: |> cat $_, or print the values: f |> print | cat",
		"[1] |> len":              "len takes its data as an arg, not values: len $x, or for each value: |> len $_",
		"'s' |> lines":            "lines reads stdin, not values; pass each value as an arg with $_: |> lines $_, or print the values: $x |> print | lines",
		"let xs = *.txt":          "a glob gives several paths, not one value",
		"fn two { 1; 2 }\nfn add a b { 1 }\nadd (two)": "fn add takes 2 args, got 1",
		"print ...[a: 1]": "... spreads a list, not a map",
		"fn big n { $n > 9 }\nif big 1 { print y }":                 "big ends in a value, so as a command it always succeeds; to test its value: (big ...)",
		"fn big n { if $n > 9 { $true } }\nif big 1 { print y }":    "big ends in a value",
		"fn big n { $n > 9 }\nbig 1 && print y":                     "big ends in a value",
		"fn big n { $n > 9 }\nmatch 1 { x if big $x => 1; _ => 2 }": "big ends in a value",
		"let x = [https://a.com]":                                   "bare word in a list",
		"let MAX = 3":                                               "env var name",
		"if $true { 1 }\nelse { 2 }":                                "else must follow",
		"ls | each { echo $it }":                                    "each does not read stdin",
		"'hello' | tr a-z A-Z":                                      "cannot be piped",
		"fn add a b { $a + $b }\nfn f { add 1 }":                    "fn add takes 2 args, got 1",
		"[1] |> { print $it }":                                      "this fn takes 1 args, got 0: $it is the item of each: |> each { ... }",
		"print a | { print $it }":                                   "$it is the item of each",
		"[1] |> {|a b| print $a }":                                  "this fn takes 2 args, got 0",
		"call { print $it }":                                        "$it is the item of each",
		"call ls":                                                   "call takes one fn value",
		"[1] |> each":                                               "each takes 1 args, got 0",
		"lines a b":                                                 "lines takes 0 to 1 args, got 2",
		"fn v x ...r { 1 }\nv":                                      "fn v takes at least 1 args, got 0",
		"if $true { let x = 1 }\nprint $x":                          "undefined variable $x",
		"fn f { $y }\nlet y = 1":                                    "undefined variable $y",
		"fn show { print $v }\nfn run { let v = 1; show }":          "undefined variable $v",
		"print $it":                                                 "$it is only defined",
		"try { false } catch e { 1 }\nprint $e":                     "undefined variable $e",
		"[1] |> count | cat":                                        "cannot be piped",
		"let m = [a: 1 a: 2]":                                       "duplicate key a",
		"let xs = [1]; $xs[]":                                       "empty index",
		"'print 1' |> eval":                                         "eval takes its source as its first arg",
		"^'ls'":                                                     "expected a command name",
		"print *.[ch]":                                              "glob classes ([ch]) are not supported",
		"print \"x\"[0]":                                            "a glued [ indexes only $name or ( )",
		"print ([1])[(ls)]":                                         "ls has no value",
		"'x' |> as_file":                                            "as_file reads stdin, not values; print the values: $x |> print | as_file",
		"as_file x":                                                 "as_file reads stdin, not a value",
		"cat <(ls)":                                                 "ls has no value; to pass its output as a file arg: (ls ... | as_file)",
		"print $eval":                                               "undefined variable $eval",
		"let xs = [1]; $xs[len $xs]":                                "a call in an index needs parens: $xs[(len ...)]",
		"let xs = [1]; $xs[(ls)]":                                   "ls has no value",
		"let xs = [1]; $xs[($i)]":                                   "undefined variable $i",
		"print \"a $(echo x) b\"":                                   "a string interpolates variables, never code; bind it first: let s = $( ... ), or write a literal $ as \\$",
		"print 'a \\\nb'":                                           "unterminated string",
		"print \"a \\(1) b\"":                                       "a string interpolates variables, never code",
		"let t =\n  $\\\\a $(x)":                                    "or write a literal $( in a raw \\\\ line",
		"print x$(len [1 2])y":                                      "len writes no output, and $( ) captures output; for a value use ( )",
		"let s = $(ls | lines)":                                     "lines writes no output",
		"let xs = [1]; print \"$xs[(len $xs) - 1]\"":                "a string interpolates variables, never code",
		"let xs = [1]; let i = 0; print \"$xs[$i + 1]\"":            "a string interpolates variables, never code",
		"print $call":                                               "undefined variable $call",
		"fn a { $b }\nfn b { 1 }":                                   "undefined variable $b",
		"[1] |> each {|x| $it }":                                    "use them instead of $it",
		"match 1 { _ => 1; 1 => 2 }":                                "unreachable arm",
		"match 1 { x => 1; _ => 2 }":                                "unreachable arm",
		"match [1 1] { [x x] => 1 }":                                "$x is bound twice",
		"match 1 { foo.txt => 1 }":                                  "bare word foo.txt in a pattern",
		"match 1 { true => 1 }":                                     "to match the value write $true",
		"match 1 { }":                                               "match needs at least one arm",
		"match 1 { 1 2 }":                                           "expected => after the pattern",
		"let v = 1; match 1 { \"$v\" => 1 }":                        "a pattern string cannot interpolate",
		"match 1 { $(echo 1) => 1 }":                                "a pattern cannot run code",
		"match 1 { 1 => {|x| $x } }":                                "the { } after => is a block",
		"match 1 { [a ...r b] => 1 }":                               "...rest must be the last item",
		"match 1 { [a: 1 b] => 1 }":                                 "mixed list items and map entries",
		"match 1 { $nope => 1 }":                                    "undefined variable $nope",
		"match 1 { x => 1 }\nprint $x":                              "undefined variable $x",
		"match 1 { x if $y => 1 }":                                  "undefined variable $y",
		"match ls { _ => 1 }":                                       "ls has no value",
		"1 |> match { _ => 1 }":                                     "match takes its value first",
		"let match = 1":                                             "match is reserved",
		"fn f { source x.ent }":                                     "source must be a statement at the top level",
		"source $HOME/x.ent":                                        "source takes one literal path",
		"source a.ent b.ent":                                        "source takes one literal path",
		"source nope.ent":                                           "source: open",
		"src":                                                       "src takes 1 args, got 0",
		"match 1 { 1 => }":                                          "expected a body on the same line after =>",
		"match 1 {\n  1 =>\n  _ => print b\n}":                      "expected a body on the same line after =>",
		"match 1 { 1 => ; _ => 2 }":                                 "expected a body on the same line after =>",
		"src { 1 } | cat":                                           "cannot be piped",
		"echo a\\\nb":                                               "must be a word of its own",
		"echo a\\  \nb":                                             "must be a word of its own",
		"echo a \\ b":                                               "a lone '\\' continues a line only at its end",
	}
	for src, want := range cases {
		in, _ := newTest(t)
		err := in.RunScript(src, "t")
		var e *Error
		if !errors.As(err, &e) || !strings.Contains(e.Msg, want) {
			t.Errorf("%q: got %v, want an error containing %q", src, err, want)
		}
	}
}

func TestScript(t *testing.T) {
	in, out := newTest(t)
	os.WriteFile(filepath.Join(in.cwd, "a.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(in.cwd, "b.txt"), []byte("y\n"), 0o644)
	src := `
fn ll ...args { ^ls ...$args }
let n = ll *.txt | lines |> count
print "files: $n"
*.txt |> each {|f| print "- $f" }
yes | head -1
printf "p\nq\n" | lines |> each { print "line $it" }
false || print fallback
if test -f a.txt && !test -f c.txt { print guard }
let t =
    \\raw $n
    $\\interp $n
print $t
`
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	want := "files: 2\n- a.txt\n- b.txt\ny\nline p\nline q\nfallback\nguard\nraw $n\ninterp 2\n"
	if got := out(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAsFile(t *testing.T) {
	in, out := newTest(t)
	dir := t.TempDir()
	in.env["XDG_RUNTIME_DIR"] = dir
	src := `
let p = (printf 'x\n\n' | as_file)
cat $p
comm -12 (printf 'a\nb\n' | as_file) (printf 'b\nc\n' | as_file)
fn gen { print g }
cat (gen | as_file)
`
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	if got, want := out(), "x\n\nb\ng\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("temp files left after the script: %v", left)
	}
	vals, err := in.Eval("printf a | as_file")
	if err != nil || len(vals) != 1 || filepath.Dir(vals[0]) != dir {
		t.Fatalf("as_file at the REPL = %q, %v", vals, err)
	}
	if _, err := os.Stat(vals[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists after the input: %v", vals[0], err)
	}
	if err := in.RunScript("let p = (false | as_file)", "t"); err == nil || !strings.Contains(err.Error(), "'false' failed") {
		t.Errorf("failing producer: %v", err)
	}
}

func TestRuntimeErrors(t *testing.T) {
	cases := map[string]string{
		"false":                                "'false' failed (exit 1)",
		"echo $NOPE_UNSET_VAR_X":               "$nil passed to an external",
		"print (1 == 'a')":                     "cannot compare num with str",
		"print (1 / 0)":                        "division by zero",
		"echo ...[1 2]":                        "",
		"print *.nomatch":                      "no matches",
		"!true":                                "the command succeeded",
		"let xs = [1]; echo $xs":               "list passed to an external",
		"sleep (1 / 3)":                        "no exact decimal",
		"if 'x' { print 1 }":                   "if needs a bool",
		"let c = 1; ^$c":                       "$c is a num, not a command name",
		"let c = ''; ^$c":                      "$c is an empty command name",
		"let xs = [1 2]; print (str $xs[1:3])": "slice [1:3] out of range",
		"let m = [1: 'x']; let n = 1; print $m[$n]":                     "a map key must be a str",
		"let m = [a: 1]; print (str $m[0:1])":                           "cannot slice a map",
		"keys [1]":                                                      "keys needs a map",
		"to_json (1 / 3)":                                               "to_json: 1/3 has no exact decimal",
		"to_json [{ $it }]":                                             "to_json: cannot encode a fn",
		`from_json '{"a":1,"a":2}'`:                                     "from_json: duplicate key 'a'",
		`from_json '[1] x'`:                                             "from_json: invalid character",
		`let v = from_json ''`:                                          "from_json emitted no value",
		"from_json 1":                                                   "from_json needs a str",
		"fn f { 1; 2 }\nlet x = f":                                      "f emitted 2 values; to collect them into a list: ... |> list",
		"fn pick { if $false { 'a' } }\nls (pick)":                      "pick emitted no value",
		"fn two { 1; 2 }\nprint (two)":                                  "two emitted 2 values",
		"let x = if $false { 1 }":                                       "this if emitted no value",
		"let x = (1 + (...[1 2]))":                                      "emitted 2 values",
		"if ...[$true $true] { print 1 }":                               "emitted 2 values",
		"fn f { print x }\nif (f) { print 1 }":                          "f emitted no value",
		"eval 'print (1 +'":                                             "eval: <eval>:1:",
		"let local = 1; eval '$local'":                                  "eval: <eval>:1:1: undefined variable $local",
		"let n = 1; eval $n":                                            "eval needs a str, got a num",
		"eval 'fn inner { 5 }'; inner":                                  "inner: command not found",
		"print (match 3 { 1 => 'a'; 2 => 'b' })":                        "match: no arm matches 3",
		"print (match 3 { n if 'x' => 1 })":                             "a match guard needs a bool",
		"export V=ab\nprint \"$V[0]\"":                                  "$V: cannot index a str",
		"print \"$env:HOME[0]\"":                                        "$HOME: cannot index a str",
		"status { fail boom }":                                          "boom",
		"status { sh -c 'kill -TERM $$' }":                              "killed by SIGTERM",
		"status 1":                                                      "status needs a fn, got num",
		"print (src $len)":                                              "src: len is a builtin",
		"print (src 1)":                                                 "src needs a fn, got a num",
		"let r = try { sh -c 'kill -INT $$' } catch e { 1 }":            "killed by SIGINT",
		"sh -c 'kill -INT $$' || print no":                              "killed by SIGINT",
		"let r = if sh -c 'kill -INT $$' { 1 } else { 2 }":              "killed by SIGINT",
		"print (match '" + strings.Repeat("é", 300) + "' { 'a' => 1 })": strings.Repeat("é", 199) + "...",
	}
	for src, want := range cases {
		in, _ := newTest(t)
		err := in.RunScript(src, "t")
		if want == "" {
			if err != nil {
				t.Errorf("%q: unexpected %v", src, err)
			}
			continue
		}
		var r *Raise
		if !errors.As(err, &r) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want a raise containing %q", src, err, want)
		}
	}
}

func TestTrace(t *testing.T) {
	in, _ := newTest(t)
	var buf strings.Builder
	in.trace = &buf
	src := "fn ll ...a { ^ls ...$a }\nll -a\nls > out.txt\nlet m = 'it''s'\necho $m\nprint not-traced"
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	want := "+ ls -a\n+ ls > " + filepath.Join(in.cwd, "out.txt") + "\n+ echo 'it''s'\n"
	if buf.String() != want {
		t.Errorf("trace =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestExport(t *testing.T) {
	in, out := newTest(t)
	src := "export GREETING=hi 'NAME=a b'\nprint $GREETING $NAME\nsh -c 'echo $GREETING-$NAME'\nexport http_proxy=x\nprint $env:http_proxy"
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	if got, want := out(), "hi a b\nhi-a b\nx\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, bad := range []string{"export X", "export 1X=2", "export =v"} {
		in, _ := newTest(t)
		if err := in.RunScript(bad, "t"); err == nil || !strings.Contains(err.Error(), "NAME=value") {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestStreaming(t *testing.T) {
	in, out := newTest(t)
	// the first line is handled while the command is still running
	src := `sh -c 'echo a; sleep 0.3; echo b' | lines |> each { |x| print $x; date +%s%3N }`
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(out())
	if len(f) != 4 || f[0] != "a" || f[2] != "b" {
		t.Fatalf("output %q", f)
	}
	ta, _ := strconv.Atoi(f[1])
	tb, _ := strconv.Atoi(f[3])
	if tb-ta < 200 {
		t.Errorf("lines were not streamed: %d ms apart", tb-ta)
	}

	// so it is through a fn, from a fn's statements, and from a list
	for _, src := range []string{
		`fn tag { each { "x$it" } }` + "\n" + `sh -c 'echo a; sleep 0.3; echo b' | lines |> tag |> each { |x| print $x; date +%s%3N }`,
		"fn slow { 'xa'; sleep 0.3; 'xb' }\nslow |> each { |x| print $x; date +%s%3N }",
		"...['xa' 'xb'] |> each { sleep 0.3; $it } |> each { |x| print $x; date +%s%3N }",
	} {
		in, out := newTest(t)
		if err := in.RunScript(src, "t"); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		f := strings.Fields(out())
		if len(f) != 4 || f[0] != "xa" || f[2] != "xb" {
			t.Fatalf("%q: output %q", src, f)
		}
		ta, _ := strconv.Atoi(f[1])
		tb, _ := strconv.Atoi(f[3])
		if tb-ta < 200 {
			t.Errorf("%q: values were not streamed: %d ms apart", src, tb-ta)
		}
	}

	cases := map[string]string{
		"seq 1 6 | lines |> each { num $it } |> each { if $it % 2 == 0 { $it } } |> print": "2\n4\n6\n",
		"let xs = seq 1 3 | lines |> list\nprint $xs (len $xs)":                            "['1' '2' '3'] 3\n",
		"seq 3 | lines |> count |> print":                                                  "3\n",
		"printf 'a b\\0c\\0' | lines0 |> each { print \"<$it>\" }":                         "<a b>\n<c>\n",
		"fn f { count }\nseq 4 | lines |> f |> print":                                      "4\n",
		"yes | lines |> print | head -2":                                                   "y\ny\n",
		"fn f { 'a'; 'b' }\nf |> { print done }":                                           "done\n",
		"printf '{\"a\":1}\\n{\"a\":2}\\n' | from_json |> each { $it[a] } |> print":        "1\n2\n",
	}
	for src, want := range cases {
		in, out := newTest(t)
		if err := in.RunScript(src, "t"); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := out(); got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}

	// failures: the producer's at the end, the consumer's stops the producer
	for src, want := range map[string]string{
		"sh -c 'echo a; exit 3' | lines |> each { |x| print $x }": "exit 3",
		"yes | lines |> each { |x| fail stop }":                   "stop",
		"sleep 30 | lines |> each 1":                              "needs a fn",
	} {
		in, _ := newTest(t)
		start := time.Now()
		err := in.RunScript(src, "t")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%q took %v", src, time.Since(start))
		}
	}
}

// A stage after |> that uses $_ runs once per value, with $_ bound to it.
func TestTopic(t *testing.T) {
	cases := map[string]string{
		"...[1 2 3] |> $_ * 2 |> print":                                          "2\n4\n6\n",
		"...[1 -2 3] |> if $_ > 0 { $_ } |> print":                               "1\n3\n",
		"fn add1 x { $x + 1 }\n...[1 2] |> add1 $_ |> print":                     "2\n3\n",
		"[1 2] |> len $_ |> print":                                               "2\n",
		"printf 'a\\nb\\n' | lines |> echo got $_":                               "got a\ngot b\n",
		"printf 'a\\nb\\n' | lines |> echo $_.bak '/tmp/'$_ \"<$_>\"":            "a.bak /tmp/a <a>\nb.bak /tmp/b <b>\n",
		"...[[n: 1] [n: 2]] |> $_[n] |> print":                                   "1\n2\n",
		"...[1 2] |> $_ |> list |> print":                                        "[1 2]\n",
		"...[1 2] |> ...[$_ $_] |> list |> print":                                "[1 1 2 2]\n",
		"...[1 2] |> echo $_ | tr 12 xy":                                         "x\ny\n",
		"...[1 2] |> each { ...[10 20] |> print $it $_ }":                        "1 10\n1 20\n2 10\n2 20\n",
		"...[1 2] |> print (...[$_ 10] |> list)":                                 "[1 10]\n[2 10]\n",
		"...[1 2] |> print (...[10 20] |> $_ + 1 |> list)":                       "[11 21]\n", // the inner stage owns $_
		"fn add1 x { $x + 1 }\nlet f = $add1\n...[1 2] |> $f $_ |> print":        "2\n3\n",
		"if ...[1 2] |> test $_ -lt 5 { print yes } else { print no }":           "yes\n",
		"if ...[1 9] |> test $_ -lt 5 { print yes } else { print no }":           "no\n",
		"yes | lines |> echo $_ | head -2":                                       "y\ny\n",
		"print '$_' \"\\$_\" $$":                                                 "$_ $_ $$\n",
		"let xs = ['a' 'b']\n...[1 0] |> $xs[$_] |> print":                       "b\na\n", // a lone word is the value, not a call
		"...[1 2] |> ($_ * 2) |> print":                                          "2\n4\n",
		"let fs = [{ 7 }]\n...[0] |> call $fs[$_] |> print":                      "7\n",
		"...['a' 'nope'] |> try { test $_ = a } catch e { print \"failed $_\" }": "failed nope\n",
	}
	for src, want := range cases {
		in, out := newTest(t)
		if err := in.RunScript(src, "t"); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := out(); got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}

	// streamed like each
	in, out := newTest(t)
	if err := in.RunScript(`sh -c 'echo a; sleep 0.3; echo b' | lines |> echo $_ $(date +%s%3N)`, "t"); err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(out())
	if len(f) != 4 || f[0] != "a" || f[2] != "b" {
		t.Fatalf("output %q", f)
	}
	ta, _ := strconv.Atoi(f[1])
	tb, _ := strconv.Atoi(f[3])
	if tb-ta < 200 {
		t.Errorf("values were not streamed: %d ms apart", tb-ta)
	}

	// it stops at the first value it fails on
	in, out = newTest(t)
	err := in.RunScript("...[1 2 3] |> sh -c 'echo $0; test $0 -lt 2' $_", "t")
	if err == nil || !strings.Contains(err.Error(), "exit 1") || out() != "1\n2\n" {
		t.Errorf("got %v", err)
	}
	in, _ = newTest(t)
	err = in.RunScript("fn chk x { if $x > 1 { fail big } }\n...[1 2 3] |> chk $_", "t")
	var r *Raise
	if !errors.As(err, &r) || !strings.Contains(r.Report(), "in a $_ stage, value #2: 2") {
		t.Errorf("got %v", err)
	}

	for src, want := range map[string]string{
		"print $_":                        "$_ is the item of a stage after |>",
		"echo /tmp/out.$_":                "$_ is the item of a stage after |>",
		"let _ = 1":                       "_ is reserved",
		"...[1] |> { |_| 1 }":             "_ is reserved",
		"ls | rm $_":                      "$_ is the item of a stage after |>",
		"...[1] |> echo $_ | grep $_":     "$_ is the item of a stage after |>",
		"...[1] |> each { print $_ }":     "a { } block does not see the $_",
		"fn f { print $_ }":               "$_ is the item of a stage after |>",
		"ls |> rm $_":                     "ls has no value; make its output values: ls | lines |> ...",
		"...[1] |> each $_":               "each reads the value input, but a stage with $_ runs once per value",
		"...[1] |> $_ * 2 | cat":          "a value cannot be piped into |",
		"...[1] |> 2 * 3":                 "the right side of |> must be a call, or use $_",
		"ls | lines |> rm":                "rm reads stdin, not values; pass each value as an arg with $_: |> rm $_",
		"ls | lines |> cp dst":            "pass each value as an arg with $_: |> cp ... $_",
		"fn add1 x { $x + 1 }\n1 |> add1": "fn add1 takes 1 args, got 0; |> feeds the value input, not args; pass each value as an arg with $_: |> add1 $_",
	} {
		in, _ := newTest(t)
		err := in.RunScript(src, "t")
		var e *Error
		if !errors.As(err, &e) || !strings.Contains(e.Msg, want) {
			t.Errorf("%q: got %v, want an error containing %q", src, err, want)
		}
	}
}

func TestAbbr(t *testing.T) {
	in, _ := newTest(t)
	if err := in.RunScript("abbr gco 'git checkout'", "t"); err != nil {
		t.Fatal(err)
	}
	if got, ok := in.Abbr("gco"); !ok || got != "git checkout" {
		t.Errorf("Abbr(gco) = %q, %v", got, ok)
	}
	for _, bad := range []string{"abbr 'a b' x", "abbr x [1]", "abbr x"} {
		in, _ := newTest(t)
		if err := in.RunScript(bad, "t"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestBind(t *testing.T) {
	in, read := newTest(t)
	src := "bind ctrl-x { print $it }\nbind ctrl-v { 'pasted' }\nbind alt-c { cd / }\nbind ctrl-l { [1] }\n" +
		"bind alt-q { [input: \"shq add $it\"] }\nbind alt-w { [input: 'a' cursor: 0] }"
	if err := in.RunScript(src, "t"); err != nil {
		t.Fatal(err)
	}
	if s, r, err := in.RunKey("ctrl-x", "echo hi"); s != "" || r || err != nil || read() != "echo hi\n" {
		t.Errorf("ctrl-x: %q, %v, %v", s, r, err)
	}
	if s, r, err := in.RunKey("ctrl-v", "x"); s != "pasted" || r || err != nil {
		t.Errorf("ctrl-v: %q, %v, %v", s, r, err)
	}
	if s, r, err := in.RunKey("alt-c", ""); s != "" || r || err != nil || in.Cwd() != "/" {
		t.Errorf("alt-c: %q, %v, %v, cwd %s", s, r, err, in.Cwd())
	}
	if s, r, err := in.RunKey("alt-q", "ls -l"); s != "shq add ls -l" || !r || err != nil {
		t.Errorf("alt-q: %q, %v, %v", s, r, err)
	}
	if _, _, err := in.RunKey("ctrl-l", ""); err == nil || !strings.Contains(err.Error(), "must return a str") {
		t.Errorf("ctrl-l: %v", err)
	}
	if _, _, err := in.RunKey("alt-w", ""); err == nil || !strings.Contains(err.Error(), "[input: str]") {
		t.Errorf("alt-w: %v", err)
	}
	if !in.Bound("ctrl-x") || in.Bound("ctrl-y") {
		t.Error("Bound")
	}
	for _, bad := range []string{"bind ctrl-i { 1 }", "bind ctrl-xx { 1 }", "bind f1 { 1 }", "bind ctrl-x 1", "bind ctrl-x {|a b| 1 }"} {
		in, _ := newTest(t)
		if err := in.RunScript(bad, "t"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestTilde(t *testing.T) {
	in, out := newTest(t)
	in.setEnv("HOME", "/h")
	if err := in.RunScript("print ~ ~/x '~/q' \"~/q\" a~b --o=~/x", "t"); err != nil {
		t.Fatal(err)
	}
	if got := out(); got != "/h /h/x ~/q ~/q a~b --o=~/x\n" {
		t.Errorf("got %q", got)
	}
	if err := in.RunScript("print ~user", "t"); err == nil || !strings.Contains(err.Error(), "~ expands only as") {
		t.Errorf("~user: %v", err)
	}
	in.setEnv("HOME", "")
	if err := in.RunScript("print ~/x", "t"); err == nil || !strings.Contains(err.Error(), "$HOME is not set") {
		t.Errorf("empty HOME: %v", err)
	}
	if err := in.RunScript("~/bin/x", "t"); err == nil || !strings.Contains(err.Error(), "$HOME is not set") {
		t.Errorf("empty HOME at the head: %v", err)
	}
}

// Fns have no kinds: stdout and the value are separate, and each operator
// takes one of them.
func TestOutputAndValue(t *testing.T) {
	cases := map[string]string{
		// a recursive fn that prints, with no else
		"fn h n { if $n > 0 { print $n; h ($n - 1) } }\nh 2 | cat": "2\n1\n",
		// output goes on, the value goes to let
		"fn f { print debug; 42 }\nlet x = f\nprint \"x=$x\"": "debug\nx=42\n",
		// $( ) takes the output and drops the value; ( ) the reverse
		"fn f { print out; 42 }\nlet s = $(f)\nprint \"s=$s\"": "s=out\n",
		"fn f { print out; 42 }\nprint (f)":                    "out\n42\n",
		// a wrapper reads like the external it wraps
		"fn e ...a { ^echo ...$a }\ne a b | tr a-z A-Z":    "A B\n",
		"fn e ...a { ^echo ...$a }\nprint (e x y | lines)": "x y\n",
		// | lines streams the output of a fn too
		"fn two { print a; print b }\ntwo | lines |> each { print \"<$it>\" }": "<a>\n<b>\n",
		// a converter with no data arg reads its stdin, like cat, in a fn too
		"fn eachl f { lines |> each $f }\nprintf 'a\\nb\\n' | eachl { print \"<$it>\" }": "<a>\n<b>\n",
		"fn parse { from_json }\nprint (echo '[1]' | parse)":                             "[1]\n",
		// | $f calls a fn value, like |> $f
		"let xs = $lines\nprint (printf 'a\\nb\\n' | $xs |> list)": "['a' 'b']\n",
		"let up = { ^tr a-z A-Z }\necho hi | $up":                  "HI\n",
		// a lambda after |> or | is called the same way
		"...[1 2 3] |> { print (count) }":                          "3\n",
		"[1 2 3] |> each { $it[1:] } |> each {|xs| print $xs[0] }": "2\n",
		"printf 'a\\nb\\n' | { lines |> count } |> print":          "2\n",
		// a lambda with no |params| and no $it ignores its args
		"...[1 2 3] |> each { print hi }": "hi\nhi\nhi\n",
		"{ print hi } 1 2":                "hi\n",
		// $name[...] indexes, whatever the name; ${name} ends the reference
		"let x = 'a'\nprint \"${x}[0] $x\"": "a[0] a\n",
		"export V=ab\nprint \"${V}[0]\"":    "ab[0]\n",
		// status: an exit code as a value
		"print (status { sh -c 'exit 3' }) (status { true }) (status { false | true })":  "3 0 1\n",
		"match (status { grep -q zzz /dev/null }) { 0 => print found; 1 => print none }": "none\n",
		"status { echo out; 5 } | tr a-z A-Z":                                            "OUT\n",
		"print \"cost \\$(5)\"":                                                          "cost $(5)\n",
		"{|a b| print $a $b } x y":                                                       "x y\n",
		"let n = { $it + 1 } 2\nprint $n":                                                "3\n",
		"[1] |> {|a| print $a (count) } x":                                               "x 1\n",
		// so are call { } and ( ), like $f
		"call { let t = 1; print $t }":                          "1\n",
		"fn add a { { $a + $it } }\nprint ((add 2) 3)":          "5\n",
		"fn add a { { $a + $it } }\n3 |> each (add 2) |> print": "5\n",
		"fn mk { { print made } }\ncall (mk)":                   "made\n",
		// the value of a byte pipeline is that of its last stage
		"print (echo '{\"a\": 1}' | from_json)": "[a: 1]\n",
		// each's output is the output of its bodies
		"...[1 2] |> each { print $it } | tr 12 xy": "x\ny\n",
		"[1 2] |> each { print $it } | tr 12 xy":    "[x y]\n",
		// a call's $nil is success in a condition; a fn's bool is its answer
		"fn big n { $n > 9 }\nif (big 10) && test -n x { print yes }": "yes\n",
		// a command in a condition is tested by its status, a value by itself
		"fn big n { $n > 9 }\nif (big 1) { print y } else { print n }":        "n\n",
		"fn has { grep -q x /dev/null }\nif has { print y } else { print n }": "n\n",
		"let a = $true\nlet b = $false\nprint ($a && $b) ($a || $b) (!$a)":    "$false $true $false\n",
		"fn f { $false && print x; 'r' }\nprint (f)":                          "r\n",
		"fn f { 'v' }\nif (f) == 'v' && !test -f nope { print eq }":           "eq\n",
		"fn quiet { ^true }\nif quiet { print ok }":                           "ok\n",
		// a command on one branch and a value on another is fine
		"fn f c { if $c { print hi } else { 1 } }\nf $true\nprint (f $false)": "hi\n1\n",
		// every statement of a fn emits; ( ) splices them into args and lists
		"fn f { 'a'; 'b' }\nprint ...(f |> list)":                                         "a b\n",
		"fn r a b { if $a >= $b { [] } else { [$a ...(r ($a + 1) $b)] } }\nprint (r 1 4)": "[1 2 3]\n",
		"fn r a b { if $a < $b { $a; r ($a + 1) $b } }\nprint (r 1 4 |> list)":            "[1 2 3]\n",
		"fn f { 1 }\nfn g { f; 2 }\nprint (g |> list)":                                    "[1 2]\n",
		"...[1 2 3] |> each { if $it != 2 { $it } } |> print":                             "1\n3\n",
		"if $false { 1 }\nprint done":                                                     "done\n",
	}
	for src, want := range cases {
		in, out := newTest(t)
		if err := in.RunScript(src, "t"); err != nil {
			t.Errorf("%q: %v", src, err)
			continue
		}
		if got := out(); got != want {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}

func TestHighlight(t *testing.T) {
	in, _ := newTest(t)
	if _, err := in.Eval("fn top { 1 }"); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ src, want string }{
		{"ls | lines |> count", "ext:ls pipe:| fn:lines pipe:|> fn:count"},
		{"fn f x { ^ls $x }; f 1", "kw:fn fn:f ext:^ls fn:f"},
		{"if test -f x { top } else { print 'no' }", "kw:if ext:test fn:top kw:else fn:print"},
		{"let r = try { ./build } catch e { 1 }", "kw:let kw:try ext:./build kw:catch"},
		{"print $(git log | wc -l)", "fn:print ext:git pipe:| ext:wc"},
		{"match $args { [x] if test -f $x => cat $x; _ => print no }", "kw:match kw:if ext:test kw:=> ext:cat kw:=> fn:print"},
		// unfinished: resolved by name
		{"fn g { 1 }; ls | each {\n  g", "kw:fn fn:g ext:ls pipe:| fn:each fn:g"},
		{"top | ", "fn:top pipe:|"},
	}
	names := map[SpanKind]string{SpanKeyword: "kw", SpanFn: "fn", SpanExternal: "ext", SpanPipe: "pipe"}
	for _, c := range cases {
		src := []rune(c.src)
		var got []string
		for _, s := range in.Highlight(c.src) {
			got = append(got, names[s.Kind]+":"+string(src[s.Start:s.End]))
		}
		if g := strings.Join(got, " "); g != c.want {
			t.Errorf("%q:\n got  %s\n want %s", c.src, g, c.want)
		}
	}
}

func TestSource(t *testing.T) {
	in, out := newTest(t)
	write := func(name, src string) {
		t.Helper()
		p := filepath.Join(in.cwd, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/a.ent", "source b.ent\nfn greet n { print \"hi $n\" }\nlet version = 1")
	write("lib/b.ent", "fn twice x { $x * 2 }")
	write("main.ent", "source lib/a.ent\ngreet (twice $version)")
	if err := in.RunScript("source main.ent", "t"); err != nil {
		t.Fatal(err)
	}
	if got := out(); got != "hi 2\n" {
		t.Errorf("sourced output = %q", got)
	}

	// At the REPL, sourcing again re-reads the file: fns defined earlier
	// that call a sourced fn see the new definition.
	in, out = newTest(t)
	write("lib.ent", "fn f { 1 }")
	for _, src := range []string{"source lib.ent", "fn g { f }"} {
		if _, err := in.Eval(src); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
	}
	write("lib.ent", "fn f { 2 }\n'not printed'")
	if got, err := in.Eval("source lib.ent"); len(got) != 0 || err != nil {
		t.Errorf("source at the REPL = %q, %v; want no value", got, err)
	}
	if got, _ := evalLast(in, "g"); got != "2" {
		t.Errorf("g after reload = %q, want 2", got)
	}

	write("c1.ent", "source c2.ent")
	write("c2.ent", "source c1.ent")
	err := in.RunScript("source c1.ent", filepath.Join(in.cwd, "top.ent"))
	if err == nil || !strings.Contains(err.Error(), "source cycle") {
		t.Errorf("cycle: got %v", err)
	}
	write("bad.ent", "let x = ")
	err = in.RunScript("source bad.ent", "t")
	if err == nil || !strings.Contains(err.Error(), "bad.ent:1:") {
		t.Errorf("an error in a sourced file should point into it, got %v", err)
	}
	write("shadow.ent", "fn source p { print $p }")
	err = in.RunScript("source shadow.ent", "t")
	if err == nil || !strings.Contains(err.Error(), "cannot define fn source") {
		t.Errorf("fn source in a sourced file: got %v", err)
	}
	if err := in.RunScript("fn source p { print $p }\nsource x", "t"); err != nil || out() != "x\n" {
		t.Errorf("a fn named source should shadow the builtin: %v", err)
	}
}

func TestReport(t *testing.T) {
	report := func(in *Interp, src string) string {
		t.Helper()
		_, err := in.Eval(src)
		var r *Raise
		if !errors.As(err, &r) {
			t.Fatalf("%q: want a raise, got %v", src, err)
		}
		return r.Report()
	}
	cases := []struct{ src, want string }{
		{"false | false | true", `'false' failed (exit 1)
 --> 1:9
  |
1 | false | false | true
  | ^^^^^ exit 1
  |         ^^^^^ exit 1`},
		{"fn g x { if $x > 2 { false } }\n...[1 2 3] |> each { |v| g $v }", `'false' failed (exit 1)
 --> 1:22
  |
1 | fn g x { if $x > 2 { false } }
  |                      ^^^^^
  in fn g, called at 2:26: g $v }
  in each, value #3: 3`},
		{"fn f n { if $n == 0 { false } else { f ($n - 1) } }\nf 3", `'false' failed (exit 1)
 --> 1:23
  |
1 | fn f n { if $n == 0 { false } else { f ($n - 1) } }
  |                       ^^^^^
  in fn f, called at 1:38: f ($n - 1) } } (3 times)
  in fn f, called at 2:1: f 3`},
	}
	for _, c := range cases {
		in, _ := newTest(t)
		if got := report(in, c.src); got != c.want {
			t.Errorf("%q:\ngot:\n%s\nwant:\n%s", c.src, got, c.want)
		}
	}

	// A fn from an earlier input shows its own line, not the line of the
	// same number in the current input.
	in, _ := newTest(t)
	if _, err := in.Eval("fn h { false }"); err != nil {
		t.Fatal(err)
	}
	if got := report(in, "h"); !strings.Contains(got, "1 | fn h { false }") {
		t.Errorf("got:\n%s", got)
	}
}

func TestMatchBracket(t *testing.T) {
	in, _ := newTest(t)
	cases := []struct {
		src       string
		pos, want int
	}{
		{"f { (1 + 2) }", 2, 12},
		{"f { (1 + 2) }", 12, 2},
		{"f { (1 + 2) }", 4, 10},
		{"print '(' (1)", 10, 12}, // not the ( in the string
		{"print '(' (1)", 7, -1},
		{"f { # }\n}", 2, 8}, // not the } in the comment
		{"if $x {", 6, -1},   // unfinished
		{"$xs[0]", 3, 5},
		{"print a", 2, -1},
	}
	for _, c := range cases {
		if got := in.MatchBracket(c.src, c.pos); got != c.want {
			t.Errorf("%q at %d: got %d, want %d", c.src, c.pos, got, c.want)
		}
	}
}
