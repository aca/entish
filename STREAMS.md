# Two streams: the redesign

Status: implemented. It replaced the earlier value model (one value per call, `|>` as the last arg); DESIGN.md follows it.

## The rule

> Every call emits an output stream and a value stream. The receiver picks one with an operator.

Everything below follows from it.

## Streams

| | output stream | value stream |
|---|---|---|
| carries | bytes | values, 0 or more |
| read through | stdin | value input |
| connected by | `\|` | `\|>` |
| in a fn | everything its body writes | everything its body emits |
| received by nobody | goes to the terminal | dropped; the REPL shows a statement's, marked `▶` |

What emits what:

- an external (`ls`): output only; no values
- `print x`, `to_json x`: output only
- an expression (`'a'`, `1 + 2`, `$x`): one value
- `...$xs`: each item of the list, as a value
- `lines`: one value per line of its stdin
- a fn: the output and values of every statement of its body, in order. Like a bash fn's stdout, for values too.

```sh
fn slow { sleep 1; '1'; sleep 1; '2'; sleep 1; '3' }
slow |> each { print "got $it" }      # got 1 at 1s, got 2 at 2s, got 3 at 3s
```

The stream an operator does not pick: in `a | b` a's values are dropped; in `a |> b` a's output goes on to the statement's output (the terminal, or the enclosing fn's output). a runs to the end either way.

## Receiving

| syntax | gets |
|---|---|
| `a \| b` | a's output → b's stdin |
| `a \|> b` | a's values → b's value input |
| `$(a)` | a's output as one `str`, trailing newline trimmed |
| `let x = a`, `(a)` | a's value; exactly one, else raise: `f emitted 3 values; to collect them into a list: ... \|> list` |
| `a \|> list` | a's values as one list |
| `...$xs`, `...[1 2]`, `...(a)` | the items of a list, spliced into args, a list or the value stream |

`( )` is one value wherever it is, so a call that emits none or several raises instead of silently dropping or adding args, and the checker can count args. `...` always spreads one list, so `[$a ...(range ($a + 1) $b)]` concatenates lists. A stream is spread through a list: `rm ...(old_files |> list)`. This is the Python and JS split: a stream and a list are different, collecting is a fn (`list(gen)`, `Array.from`), spreading takes a list (`*xs`, `...xs`). A glob is several paths wherever it is: args, a list (`[*.go]`), the value stream (`*.go |> each`); where one value is needed it is an error, whatever matches, so its type never depends on the files.

## Reading input

A fn body reads the stdin and the value input of its call, as a bash fn reads its stdin. A builtin that reads values reads the value input.

```sh
fn tag { each { "x$it" } }                    # each reads tag's value input
cmd | lines |> tag |> each { print $it }      # streams through tag
```

## The item: `$_`

A stage after `|>` that uses `$_` runs once per value with `$_` bound to it, so a command or an expression takes the item where it needs it: `ls | lines |> mv $_ $_.bak`, `...$xs |> $_ * 2`. It is `each` without a block; the rules are in DESIGN.md, under Pipes.

## Streaming

- Every stage of a pipeline runs concurrently; values flow one at a time, like bytes through a pipe. The value pipe buffers 64 values, so a writer runs at most that far ahead.
- A stage that stops reading stops the stages before it (like `yes | head`).
- A statement ends when everything it started has ended. A variable therefore always holds a complete value; streams are never values.
- A failing stage raises when the statement ends (pipefail), also if its stream was dropped.

## Conversions, all explicit

| direction | how |
|---|---|
| bytes → values | `lines`, `lines0`, `from_json` (one value per JSON document) |
| values → bytes | `print`, `to_json` with no args: each value of the value input on its own line |
| stream → list | `a \|> list` |
| list → stream | `...$xs` |

## Checks

Rejected only when the source makes it certain:

- left of `|` writes no output: an expression or a value-only builtin. `1 | cat`
- right of `|` reads no stdin: a builtin that is not a converter. `ls | len`
- left of `|>` emits no values: an external, `print`, `to_json`. `ls |> each`
- right of `|>` reads no value input and does not use `$_`: an external, or a builtin that takes args only. `$x |> jq`, `$xs |> len`
- arity as now; `|>` no longer adds an arg, so `$xs |> f` with `fn f xs` is `f takes 1 args, got 0`.

A user fn can do both on each side, so it always passes; picking the operator is the caller's job.

## Builtins

| | builtins |
|---|---|
| read value input | `each` (emits what its body emits, so it is also map and filter), `count`, `list`, `print` and `to_json` with no args |
| read stdin | `lines`, `lines0`, `from_json`, `as_file` |
| args only | `len`, `keys`, `str`, `num`, `round`, `assoc`, `src`, `cd`, `export`, ... |
| removed | `map`, `filter` |

```sh
... |> each { $it * 2 }                  # map
... |> each { if $it > 0 { $it } }       # filter
... |> $_ * 2                            # map, with $_
... |> if $_ > 0 { $_ }                  # filter, with $_
```

## Other consequences

- `if` without `else` emits nothing when the condition is false; the "needs an else" check goes. `let x = if ...` raises if it gets no value.
- `unused value` goes: a value nobody receives is dropped. At the REPL a statement's values are shown.
- Condition position (`if`, `&&`, `||`, `!`, a match guard): a command is tested by its status, its values dropped; anything else, `( )` included, must be one `bool`. So `if grep -q x f` tests the status and `if (big 1)` the value. A fn whose body ends in a value always succeeds as a command, so `if big 1` for `fn big n { $n > 9 }` is a compile error.
- `&&` and `||` between values are a bool: `let ok = $a && $b`. With a command on either side they are control flow and emit only what the operand that runs emits: `$verbose && print x` adds no value to a fn's.
- A helper's values join its caller's: `fn g { helper; 'x' }` emits helper's values and `'x'`. Same rule as output.
- A list emitted as a value is one value: `$xs |> each` runs once with the whole list. `...$xs |> each` runs per item. Not checkable: the type of `$xs` is not known.
- A lambda after `|>` is called with no args and reads the values as its value input: `|> { print (count) }`. `$it` is the item of `each`.

## Before and after

| now | new |
|---|---|
| `ls \| lines \|> len` | `ls \| lines \|> count` |
| `$xs \|> map { $it * 2 }` | `...$xs \|> each { $it * 2 } \|> list` |
| `$xs \|> filter { $it > 0 }` | `...$xs \|> each { if $it > 0 { $it } } \|> list` |
| `let n = $xs \|> len` | `let n = len $xs` |
| `*.go \|> each { gofmt -l $it }` | the same: a glob emits its paths |
| `let xs = cmd \| lines` | `let xs = cmd \| lines \|> list` |
| `'{"a":1}' \|> from_json` | `from_json '{"a":1}'` |
| `1 / 3 \|> round 4` | `round 4 (1 / 3)` |
| `$xs \|> to_json \| jq .` | `$xs \|> to_json \| jq .` (one JSON value per line) |
| `fn slow { ...; '1'; ...; '2' }` | works; now `unused value` |
| `cmd \| lines \|> myfn` collects first | streams |
