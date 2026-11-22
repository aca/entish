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
| connected by | `\|` | `\|>` (as the value input), `\|%` (one run per value) |
| in a fn | everything its body writes | everything its body emits |
| received by nobody | goes to the terminal | dropped; the REPL shows a statement's, marked `▶` |

What emits what:

- an external (`ls`): output only; no values
- `print x`, `to_json x`: output only
- an expression (`'a'`, `1 + 2`, `$x`): one value
- `...$xs`: each item of the list, as a value
- `$42` (REPL only): replay the saved final values of input 42; `$42 |> list` collects them. In single-value positions it must emit exactly one value.
- `lines`: one value per line of its stdin
- a fn: the output and values of every statement of its body, in order. Like a bash fn's stdout, for values too.

```sh
fn slow { sleep 1; '1'; sleep 1; '2'; sleep 1; '3' }
slow |% print "got $_"               # got 1 at 1s, got 2 at 2s, got 3 at 3s
```

The stream an operator does not pick: in `a | b` a's values are dropped; in `a |> b` and `a |% b` a's output goes on to the statement's output (the terminal, or the enclosing fn's output). a runs to the end either way.

## Receiving

| syntax | gets |
|---|---|
| `a \| b` | a's output → b's stdin |
| `a \|> b` | a's values → b's value input |
| `a \|% b` | each of a's values → one run of b, as `$_` or its last arg |
| `$(a)` | a's output as one `str`, trailing newline trimmed |
| `let x = (a)`, `(a)` | a's value; exactly one, else raise: `f emitted 3 values; to collect them into a list: ... \|> list` |
| `a \|> list` | a's values as one list |
| `...$xs`, `...[1 2]`, `...(a)` | the items of a list, spliced into args, a list or the value stream |

`( )` is one value wherever it is, so a call that emits none or several raises instead of silently dropping or adding args, and the checker can count args. `...` always spreads one list, so `[$a ...(range ($a + 1) $b)]` concatenates lists. A stream is spread through a list: `rm ...(old_files |> list)`. This is the Python and JS split: a stream and a list are different, collecting is a fn (`list(gen)`, `Array.from`), spreading takes a list (`*xs`, `...xs`). A glob is several paths wherever it is: args, a list (`[*.go]`), the value stream (`*.go |> print`); where one value is needed it is an error, whatever matches, so its type never depends on the files.

## Reading input

A fn body reads the stdin and the value input of its call, as a bash fn reads its stdin. A builtin that reads values reads the value input. Only `|>` gives a stage a value input; a stage after `|%` gets none.

```sh
fn tag { each { "x$it" } }                    # each reads tag's value input
cmd | lines |> tag |> print                   # streams through tag
```

## Per value: `|%` and `$_`

A stage after `|%` runs once per value with `$_` bound to it. It is `|> each { ... }` without the block. If the stage uses `$_`, the value goes there: `ls | lines |% mv $_ $_.bak`, `...$xs |% $_ * 2`. If it does not and is a call of a fn, a builtin, `$f`, `{ }` or `( )`, the value is its last arg: `$x |% round 2` is `round 2 $x`, `'e' |% string:uppercase` is `'E'`. An external must use `$_`, since its arg order is its own: `|% rm` is a compile error, `|% rm $_` is not. The rules are in DESIGN.md, under Pipes.

## Streaming

- Every stage of a pipeline runs concurrently; values flow one at a time, like bytes through a pipe. The value pipe buffers 64 values, so a writer runs at most that far ahead.
- A stage that stops reading stops the stages before it (like `yes | head`).
- A statement ends when everything it started has ended. A variable therefore always holds a complete value; streams are never values.
- A failing stage raises when the statement ends (pipefail), also if its stream was dropped.

## Conversions, all explicit

| direction | how |
|---|---|
| bytes → values | `lines`, `lines0`, `from_json` (one value per JSON document) |
| values → bytes | `print`, `to_json` with no args: `xs \|> print` writes each value of the value input on its own line |
| stream → list | `a \|> list` |
| list → stream | `...$xs` |

## Checks

Rejected only when the source makes it certain:

- left of `|` writes no output: an expression or a value-only builtin. `1 | cat`
- right of `|` reads no stdin: a builtin that is not a converter. `ls | len`
- left of `|>` or `|%` emits no values: an external, `print`, `to_json`. `ls |> count`, `ls |% len`
- right of `|>`: an external, or a builtin that takes its data as an arg. `$x |> jq`, `$xs |> len` (the hint offers `|% len`)
- right of `|%` without `$_`: an external, or a stage that is not a call. `$x |% jq`, `xs |% 3 * 2`
- right of `|%`: a builtin that reads the value input, or a fn or `{ }` whose body reads its own; it would get none. `$xs |% count`
- arity: the value `|%` passes counts as the last arg, so `xs |% add 10` calls `fn add a b` as `add 10 x`; `|>` adds no arg, so `$xs |> f` with `fn f xs` is `f takes 1 args, got 0`.

A user fn may write output and emit values, and read stdin and its value input, so the other checks pass it; picking the operator is the caller's job.

## Builtins

| | builtins |
|---|---|
| read value input (after `\|>`) | `each` (emits what its body emits, so it is also map and filter), `count`, `take n` (the first n values; then it stops reading, so an endless producer ends: `nat 0 \|> take 3`), `list`, `pick`, `print` and `to_json` with no args |
| read stdin | `lines`, `lines0`, `from_json`, `as_file` |
| args only (after `\|%`, the value is the last arg) | `len`, `keys`, `str`, `num`, `round`, `assoc`, `print`, `to_json`, `src`, `cd`, `export`, ... |
| removed | `map`, `filter` |

```sh
... |> each { $it * 2 }                  # map
... |> each { if $it > 0 { $it } }       # filter
... |% $_ * 2                            # map, with $_
... |% if $_ > 0 { $_ }                  # filter, with $_
```

## Other consequences

- `if` without `else` emits nothing when the condition is false; the "needs an else" check goes. `let x = if ...` raises if it gets no value.
- `unused value` goes: a value nobody receives is dropped. At the REPL a statement's values are shown.
- Condition position (`if`, `&&`, `||`, `!`, a match guard): a command is tested by its status, its values dropped; anything else, `( )` included, must be one `bool`. So `if grep -q x f` tests the status and `if (big 1)` the value. A fn whose body ends in a value always succeeds as a command, so `if big 1` for `fn big n { $n > 9 }` is a compile error.
- `&&` and `||` between values are a bool: `let ok = $a && $b`. With a command on either side they are control flow and emit only what the operand that runs emits: `$verbose && print x` adds no value to a fn's.
- A helper's values join its caller's: `fn g { helper; 'x' }` emits helper's values and `'x'`. Same rule as output.
- A list emitted as a value is one value: `$xs |% f` runs f once with the whole list. `...$xs |% f` runs it per item. Not checkable: the type of `$xs` is not known.
- A lambda after `|>` is called with no args and reads the values as its value input: `|> { print (count) }`. After `|%` it is called with each value as its arg: `|% { $it * 2 }`. `$it` is a lambda's one param.
- `each` stays for a fn that processes its own value input: `fn tag { each { "x$it" } }`, then `xs |> tag`.

## Before and after

| first model | now |
|---|---|
| `ls \| lines \|> len` | `ls \| lines \|> count` |
| `$xs \|> map { $it * 2 }` | `...$xs \|% $_ * 2 \|> list` |
| `$xs \|> filter { $it > 0 }` | `...$xs \|% if $_ > 0 { $_ } \|> list` |
| `let n = ($xs \|> len)` | `let n = (len $xs)` |
| `*.go \|> each { gofmt -l $it }` | the same, or `*.go \|% gofmt -l $_`: a glob emits its paths |
| `let xs = (cmd \| lines)` | `let xs = (cmd \| lines \|> list)` |
| `'{"a":1}' \|> from_json` | `from_json '{"a":1}'` |
| `1 / 3 \|> round 4` | `1 / 3 \|% round 4`, or `round 4 (1 / 3)` |
| `$xs \|> to_json \| jq .` | `$xs \|> to_json \| jq .` (one JSON value per line) |
| `fn slow { ...; '1'; ...; '2' }` | works; now `unused value` |
| `cmd \| lines \|> myfn` collects first | streams |
