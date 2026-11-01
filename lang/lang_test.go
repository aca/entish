package lang

import (
	"errors"
	"os"
	"path/filepath"
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
		{"[1 'a' [x: 2]]", "[1 'a' [x: 2]]"},
		{"echo hi | lines", "['hi']"},
		{"fn f { print x; 42 }; f", "42"},
		{"fn f { ^true }; f", ""},
		{"[1 2 3] |> map {|x| $x * 10 }", "[10 20 30]"},
		{"[1 2 3 4] |> filter {|x| $x % 2 == 0 }", "[2 4]"},
		{"[1 2 3] |> map { $it * $it }", "[1 4 9]"},
		{"[1 2] |> map { |x| $x + 1 }", "[2 3]"},
		{"if 1 < 2 { 'yes' } else { 'no' }", "yes"},
		{"try { false } catch e { $e[code] }", "1"},
		{"let x = 1", ""},
		{"1 / 3 |> round 4", "0.3333"},
		{"-5 / 2 |> round 0", "-3"},
		{"0.125 |> round 2", "0.13"},
		{"$([a: [1 $true $nil]  'b c': 'x\"<y>'  f: (7 / 2)] |> to_json)", `{"a":[1,true,null],"b c":"x\"<y>","f":3.5}`},
		{"$(0755 |> to_json)", "755"},
		{"$([[:] []] |> to_json)", "[{},[]]"},
		{`'{"b": [1, 1e-3], "a": {"x": null}}' |> from_json`, "[b: [1 0.001]  a: [x: $nil]]"},
		{`('0.1' |> from_json) + 0.2 == 0.3`, "$true"},
		{"let x = [a: [1 2]  b: 'q']; (to_json $x | from_json) == $x", "$true"},
		{"eval '1 + 2'", "3"},
		{"eval '$args' 'a b' 2", "['a b' 2]"},
		{"'x' |> eval '$args'", "['x']"},
		{"fn sq x { $x * $x }\neval 'sq 7'", "49"},
		{"let src = 'echo hi'; eval $src | lines", "['hi']"},
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
		{"[a: 1 b: 2] |> keys", "['a' 'b']"},
		{"let m = [a: 1 b: 2]; $m |> assoc a 9", "[a: 9  b: 2]"},
		{"let m = [a: 1]; let n = $m |> assoc b 2; $m", "[a: 1]"},
		{"fn double x { $x * 2 }; [1 2 3] |> map $double", "[2 4 6]"},
		{"['a b' 'c'] |> map $lines", "[['a b'] ['c']]"},
		{"fn adder n { {|x| $x + $n } }; let add3 = adder 3; [1 2] |> map $add3", "[4 5]"},
		{"fn fact n { if $n <= 1 { 1 } else { $n * ($fact ($n - 1)) } }; $fact 5", "120"},
		{"fn f { 1 }; let f = 2; $f", "2"},
		{"let f = 2; fn f { 1 }; $f", "<fn f>"},
		{"let lines = 3; $lines", "3"},
		{"let g = { 41 + 1 }; call $g", "42"},
		{"let f = { false }; if call $f { 'y' } else { 'n' }", "n"},
		{"echo hi", ""}, // commands print themselves; nothing extra
	}
	for _, c := range cases {
		in, _ := newTest(t)
		got, err := in.Eval(c.src)
		if err != nil {
			t.Errorf("%q: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %q, want %q", c.src, got, c.want)
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
	if got, _ := in.Eval("$y"); got != "21" {
		t.Errorf("$y = %q", got)
	}
	if got, err := in.Eval("[1] |> map {|v| $add $v 1 }"); got != "[2]" {
		t.Errorf("$add from an earlier input = %q, %v", got, err)
	}
}

func TestNeedsMore(t *testing.T) {
	in, _ := newTest(t)
	more := []string{"if $true {", "let x =", "ls |", "[1 2", "print (1 +", `\\a`, "fn f {"}
	done := []string{"print hi", "if $true { 1 }", "\\\\a\n", "'unterminated"}
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
		"let x = ls":                             "ls has no value",
		"[1] |> jq .":                            "jq reads stdin, not a value",
		"ls |> lines":                            "ls has no value",
		"print x |> len":                         "print has no value",
		"let s = [1] |> to_json":                 "to_json has no value",
		"let x = (each { print $it } [1])":       "each has no value",
		"ls | lines a":                           "lines takes 0 to 1 args, got 2 (including stdin)",
		"ls | lines | cat":                       "cannot be piped",
		"ls | len":                               "len takes a value, not stdin",
		"let x = [https://a.com]":                "bare word in a list",
		"let MAX = 3":                            "env var name",
		"if $true { 1 }\nelse { 2 }":             "else must follow",
		"[1 2]":                                  "unused value",
		"ls | each { echo $it }":                 "each takes a value, not stdin",
		"if $true { 1 + 1 } else { 2 }\nprint x": "unused value",
		"'hello' | tr a-z A-Z":                   "cannot be piped",
		"let x = if $true { 1 }":                 "needs an else",
		"fn add a b { $a + $b }\nfn f { add 1 }": "fn add takes 2 args, got 1",
		"[1] |> map":                             "map takes 2 args, got 1 (including the value from |>)",
		"lines a b":                              "lines takes 0 to 1 args, got 2",
		"fn v x ...r { 1 }\nv":                   "fn v takes at least 1 args, got 0",
		"if $true { let x = 1 }\nprint $x":       "undefined variable $x",
		"fn f { $y }\nlet y = 1":                 "undefined variable $y",
		"fn show { print $v }\nfn run { let v = 1; show }": "undefined variable $v",
		"print $it":                                      "$it is only defined",
		"try { false } catch e { 1 }\nprint $e":          "undefined variable $e",
		"fn f x { if $x { 'pos' } }":                     "needs an else",
		"let x = if $true { 1 } else if $false { 2 }":    "needs an else",
		"[1] |> map { if $it > 0 { $it } }":              "needs an else",
		"[1] |> map { $it } | cat":                       "cannot be piped",
		"let m = [a: 1 a: 2]":                            "duplicate key a",
		"let xs = [1]; $xs[]":                            "empty index",
		"let f = 'x'; eval \"print $f\"":                 "eval takes its source first, as a 'literal' or a $var",
		"eval $(echo 1)":                                 "eval takes its source first",
		"'print 1' |> eval":                              "eval takes its source first",
		"print $eval":                                    "undefined variable $eval",
		"let xs = [1]; $xs[len $xs]":                     "a call in an index needs parens: $xs[(len ...)]",
		"let xs = [1]; $xs[(ls)]":                        "ls has no value",
		"let xs = [1]; $xs[($i)]":                        "undefined variable $i",
		"let xs = [1]; print \"$xs[(len $xs) - 1]\"":     "a string interpolates variables, never code",
		"let xs = [1]; let i = 0; print \"$xs[$i + 1]\"": "a string interpolates variables, never code",
		"print $call":                                    "undefined variable $call",
		"fn a { $b }\nfn b { 1 }":                        "undefined variable $b",
		"[1] |> map {|x| $it }":                          "use them instead of $it",
		"let f = { 1 }\nlet r = { $f; print x }":         "to call a fn value: call $f",
		"let r = { 1 + 2; print x }":                     "unused value",
		"call { print x }":                               "call takes one fn variable",
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
let n = ll *.txt | lines |> len
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

func TestRuntimeErrors(t *testing.T) {
	cases := map[string]string{
		"false":                                "'false' failed (exit 1)",
		"echo $NOPE_UNSET_VAR_X":               "$nil passed to an external",
		"print (1 == 'a')":                     "cannot compare num with str",
		"print (1 / 0)":                        "division by zero",
		"echo ...([1 2])":                      "",
		"print *.nomatch":                      "no matches",
		"!true":                                "the command succeeded",
		"let xs = [1]; echo $xs":               "list passed to an external",
		"sleep (1 / 3)":                        "no exact decimal",
		"if 'x' { 1 }":                         "if needs a bool",
		"let xs = [1 2]; print (str $xs[1:3])": "slice [1:3] out of range",
		"let m = [1: 'x']; let n = 1; print $m[$n]": "a map key must be a str",
		"let m = [a: 1]; print (str $m[0:1])":       "cannot slice a map",
		"[1] |> keys":                               "keys needs a map",
		"1 / 3 |> to_json":                          "to_json: 1/3 has no exact decimal",
		"[{ $it }] |> to_json":                      "to_json: cannot encode a fn",
		`'{"a":1,"a":2}' |> from_json`:              "from_json: duplicate key 'a'",
		`'[1] x' |> from_json`:                      "unexpected data after the value",
		`'' |> from_json`:                           "from_json: no JSON value",
		"1 |> from_json":                            "from_json needs a str",
		"eval 'print (1 +'":                         "eval: <eval>:1:",
		"let local = 1; eval '$local'":              "eval: <eval>:1:1: undefined variable $local",
		"eval 'if $true { 1 }'":                     "needs an else",
		"let n = 1; eval $n":                        "eval needs a str, got a num",
		"eval 'fn inner { 5 }'; inner":              "inner: command not found",
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

	cases := map[string]string{
		"seq 1 6 | lines |> map { num $it } |> filter { $it % 2 == 0 } |> each { print $it }": "2\n4\n6\n",
		"let xs = seq 1 3 | lines\nprint $xs (len $xs)":                                       "['1' '2' '3'] 3\n",
		"seq 3 | lines |> len |> print":                                                       "3\n",
		"printf 'a b\\0c\\0' | lines0 |> each { print \"<$it>\" }":                            "<a b>\n<c>\n",
		"fn f xs { len $xs }\nseq 4 | lines |> f |> print":                                    "4\n",
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
		"fn e ...a { ^echo ...$a }\nprint (e x y | lines)": "['x y']\n",
		// | lines streams the output of a fn too
		"fn two { print a; print b }\ntwo | lines |> each { print \"<$it>\" }": "<a>\n<b>\n",
		// a converter with no data arg reads its stdin, like cat, in a fn too
		"fn eachl f { lines |> each $f }\nprintf 'a\\nb\\n' | eachl { print \"<$it>\" }": "<a>\n<b>\n",
		"fn parse { from_json }\nprint (echo '[1]' | parse)":                             "[1]\n",
		// | $f calls a fn value, like |> $f
		"let xs = $lines\nprint (printf 'a\\nb\\n' | $xs)": "['a' 'b']\n",
		"let up = { ^tr a-z A-Z }\necho hi | $up":          "HI\n",
		// the value of a byte pipeline is that of its last stage
		"print (echo '{\"a\": 1}' | from_json)": "[a: 1]\n",
		// each's output is the output of its bodies
		"[1 2] |> each { print $it } | tr 12 xy": "x\ny\n",
		// a call's $nil is success in a condition; a fn's bool is its answer
		"fn big n { $n > 9 }\nif big 10 && test -n x { print yes }": "yes\n",
		"fn quiet { ^true }\nif quiet { print ok }":                 "ok\n",
		// a command on one branch and a value on another is fine
		"fn f c { if $c { print hi } else { 1 } }\nlet x = f $true\nprint $x": "hi\n$nil\n",
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
		{"ls | lines |> len", "ext:ls pipe:| fn:lines pipe:|> fn:len"},
		{"fn f x { ^ls $x }; f 1", "kw:fn fn:f ext:^ls fn:f"},
		{"if test -f x { top } else { print 'no' }", "kw:if ext:test fn:top kw:else fn:print"},
		{"let r = try { ./build } catch e { 1 }", "kw:let kw:try ext:./build kw:catch"},
		{"print $(git log | wc -l)", "fn:print ext:git pipe:| ext:wc"},
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
