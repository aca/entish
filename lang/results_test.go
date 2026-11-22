package lang

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResultsStreams(t *testing.T) {
	for _, tc := range []struct{ src, want, stdout string }{
		{"1 + 2", "[3]", ""},
		{"[1 2]", "[[1 2]]", ""},
		{"1; 2\n3", "[1 2 3]", ""},
		{"$nil", "[$nil]", ""},
		{"let x = 7", "[]", ""},
		{"print hello", "[]", "hello\n"},
		{"printf hello", "[]", "hello"},
		{"fn f { print log; 1; 2 }; f", "[1 2]", "log\n"},
		{"printf '1\\n2\\n' | from_json", "[1 2]", ""},
		{"printf '1\\n2\\n' | from_json |> count", "[2]", ""},
		{"fn f { print bytes; 99 }; f | lines", "['bytes']", ""},
		{"...['a' 'b'] |> print", "[]", "a\nb\n"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			in, out := newTest(t)
			original, err := in.Eval(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got := out(); got != tc.stdout {
				t.Fatalf("stdout = %q, want %q", got, tc.stdout)
			}
			if got, err := in.Eval("$1"); !slices.Equal(got, original) || err != nil {
				t.Fatalf("replay = %v, %v; want %v", got, err, original)
			}
			if got, err := evalLast(in, "$1 |> list"); got != tc.want || err != nil {
				t.Fatalf("$1 = %q, %v; want %q", got, err, tc.want)
			}
			if got := out(); got != "" {
				t.Fatalf("recalling values repeated stdout: %q", got)
			}
		})
	}
}

func TestResultsReferences(t *testing.T) {
	in, _ := newTest(t)
	if _, err := in.Eval("10; 20; 30"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, want string }{
		{"($1 |> list)[1]", "20"},
		{"($1 |> list)[1:]", "[20 30]"},
		{"$1 |> count", "3"},
		{"$1 |% $_ * 2 |> list", "[20 40 60]"},
		{"${1} |> count", "3"},
	} {
		if got, err := evalLast(in, tc.src); got != tc.want || err != nil {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
	if _, err := in.Eval("0"); err != nil {
		t.Fatal(err)
	}
	// The result number is also recognized within another variable's index.
	n := in.results.last
	src := "let names = ['first']; $names[$" + strconv.Itoa(n) + "]"
	if got, err := evalLast(in, src); got != "first" || err != nil {
		t.Fatalf("nested reference = %q, %v", got, err)
	}
}

func TestResultsKeepTypes(t *testing.T) {
	in, _ := newTest(t)
	for _, src := range []string{
		"1 / 3",
		"$1 * 3 == 1",
		"let n = 7; {|x| $x + $n }",
		"$3 5",
		"[name: 'api' nested: [1 2]]",
		"($5)[nested][1]",
	} {
		if _, err := in.Eval(src); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
	if got, err := in.Eval("$2; $4; $6"); err != nil || !slices.Equal(got, []string{"$true", "12", "2"}) {
		t.Fatalf("typed results = %v, %v", got, err)
	}
}

func TestResultsOneValueContexts(t *testing.T) {
	for _, tc := range []struct{ src, failure string }{
		{"let nothing = 1", "emitted no value"},
		{"10; 20", "emitted 2 values"},
	} {
		t.Run(tc.src, func(t *testing.T) {
			in, out := newTest(t)
			if _, err := in.Eval(tc.src); err != nil {
				t.Fatal(err)
			}
			for _, src := range []string{"let x = $1", "($1)", "1 + $1", "print $1", "printf %s $1", "[$1]", `[key: $1]`, `"answer=$1"`} {
				if _, err := in.Eval(src); err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Errorf("%s: %v; want %s", src, err, tc.failure)
				}
			}
			if got := out(); got != "" {
				t.Fatalf("an invalid argument produced output: %q", got)
			}
		})
	}
	in, _ := newTest(t)
	if _, err := in.Eval("10"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ src, want string }{
		{"let x = $1; $x", "10"},
		{"($1) + 2", "12"},
		{`"answer=$1"`, "answer=10"},
		{`$\\answer=$1` + "\n", "answer=10"},
		{"[$1]", "[10]"},
	} {
		if got, err := evalLast(in, tc.src); got != tc.want || err != nil {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

func TestResultsIndexAndSpread(t *testing.T) {
	in, out := newTest(t)
	if _, err := in.Eval("[10 20]"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"$1[0]", "$1[:1]", `"$1[0]"`} {
		if _, err := in.Eval("print should_not_run; " + src); err == nil || !strings.Contains(err.Error(), "collect it before indexing: ($1 |> list)[0]") {
			t.Fatalf("%s: %v", src, err)
		}
	}
	if got := out(); got != "" {
		t.Fatalf("index error executed code: %q", got)
	}
	for _, tc := range []struct{ src, want string }{
		{"$1 |> count", "1"}, // a list remains one value
		{"$1 |> list", "[[10 20]]"},
		{"($1 |> list)[0]", "[10 20]"},
		{"($1)[0]", "10"},       // explicitly extract the single list, then index it
		{"...$1 |> count", "2"}, // ... still spreads exactly one list
	} {
		if got, err := evalLast(in, tc.src); got != tc.want || err != nil {
			t.Errorf("%s = %q, %v; want %q", tc.src, got, err, tc.want)
		}
	}
}

func TestResultsReplayStopsWithReader(t *testing.T) {
	in, _ := newTest(t)
	if _, err := in.Eval("...[" + strings.Repeat("1 ", 1024) + "]"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := evalLast(in, "$1 |> { 9 }")
		done <- result{value, err}
	}()
	select {
	case got := <-done:
		if got.value != "9" || got.err != nil {
			t.Fatalf("early consumer = %q, %v", got.value, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not stop after the consumer exited")
	}
	// Reading the same result again starts at the beginning.
	if got, err := in.Eval("$1 |> count; $1 |> count"); err != nil || !slices.Equal(got, []string{"1024", "1024"}) {
		t.Fatalf("repeated replay = %v, %v", got, err)
	}
}

func TestResultsNumbersAndFailures(t *testing.T) {
	in, out := newTest(t)
	if in.NextResult() != 1 {
		t.Fatal("first prompt must be 1")
	}
	if _, err := in.Eval(" \n "); err != nil || in.NextResult() != 1 {
		t.Fatal("blank input consumed a number")
	}
	if err := in.RunScript("let setup = 1", "rc.ent"); err != nil || in.NextResult() != 1 {
		t.Fatal("startup script consumed a number")
	}
	if _, err := in.Eval("1; fail broken"); err == nil || in.NextResult() != 2 {
		t.Fatal("failed input must consume a number")
	}
	if got, err := in.Eval("$1"); err == nil || !strings.Contains(err.Error(), "input #1 failed") || len(got) != 0 {
		t.Fatalf("partial result was reused: %v, %v", got, err)
	}
	for _, src := range []string{"$0", "$999", "$999999999999999999999999999999"} {
		if _, err := in.Eval("print should_not_run; " + src); err == nil || !strings.Contains(err.Error(), "not available") {
			t.Fatalf("%s: %v", src, err)
		}
		if got := out(); got != "" {
			t.Fatalf("bad result reference executed a command: %q", got)
		}
	}
	if _, err := in.Eval("$" + strconv.Itoa(in.NextResult())); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("current input result: %v", err)
	}
	in.Interrupt()
	id := in.NextResult()
	if _, err := in.Eval("123"); err == nil || in.NextResult() != id+1 {
		t.Fatal("interrupted evaluation must consume a number")
	}
	if _, err := in.Eval("$" + strconv.Itoa(id)); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("interrupted result: %v", err)
	}
}

func TestResultsOnlyDirectREPL(t *testing.T) {
	in, out := newTest(t)
	if _, err := in.Eval("7"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"$1", "...$1", `"$1[0]"`, "fn f { $1 }", "${1}", "$HOME[$1]"} {
		if err := in.Check(src, ""); err == nil || !strings.Contains(err.Error(), "only available in direct REPL input") {
			t.Fatalf("check %s: %v", src, err)
		}
		if err := in.RunScript("print should_not_run; "+src, "script.ent"); err == nil || !strings.Contains(err.Error(), "only available in direct REPL input") {
			t.Fatalf("script %s: %v", src, err)
		}
	}
	if in.NextResult() != 2 || out() != "" {
		t.Fatal("script/check changed REPL numbering or executed")
	}
	path := filepath.Join(in.cwd, "lib.ent")
	if err := os.WriteFile(path, []byte("fn f { $1 }"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"source lib.ent", "eval '$1'"} {
		if _, err := in.Eval(src); err == nil || !strings.Contains(err.Error(), "only available in direct REPL input") {
			t.Fatalf("%s: %v", src, err)
		}
	}
	if got, err := evalLast(in, "eval '$args[0]' $1"); got != "7" || err != nil {
		t.Fatalf("passing a result to eval = %q, %v", got, err)
	}
	if got, err := evalLast(in, "'$1'"); got != "$1" || err != nil {
		t.Fatalf("raw string = %q, %v", got, err)
	}
}

func TestResultsExpireButBindingsSurvive(t *testing.T) {
	in, _ := newTest(t)
	for _, src := range []string{"42; 43", "let saved = ($1 |> list); fn remembered { $1 }; let closure = { $1 }"} {
		if _, err := in.Eval(src); err != nil {
			t.Fatal(err)
		}
	}
	for range resultHistorySize - 2 {
		if _, err := in.Eval("let padding = 0"); err != nil {
			t.Fatal(err)
		}
	}
	// Exactly 100 completed inputs: #1 is still available during #101.
	if got, err := evalLast(in, "$1 |> list"); got != "[42 43]" || err != nil {
		t.Fatalf("oldest retained result = %q, %v", got, err)
	}
	if _, err := in.Eval("$1"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired reference: %v", err)
	}
	if got, err := in.Eval("$saved; remembered; call $closure"); err != nil || !slices.Equal(got, []string{"[42 43]", "42", "43", "42", "43"}) {
		t.Fatalf("retained bindings = %v, %v", got, err)
	}
	other, _ := newTest(t)
	if _, err := other.Eval("$1"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("results leaked between sessions: %v", err)
	}
}
