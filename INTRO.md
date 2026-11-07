# entish

A small shell with a strict, functional language. Not POSIX-compatible.

- One call syntax for fns and external commands; fns are resolved first, `^name` forces the external.
- Every call emits two streams, output (bytes) and values; `|` carries output, `|>` carries values. Nothing is converted implicitly.
- Mistakes are compile errors, found before anything runs.
- Failing commands raise; there is no silent `$?`.

`entish` is a placeholder name. A short tour is [LEARN.md](LEARN.md), the stream model [STREAMS.md](STREAMS.md), the design and its rationale [DESIGN.md](DESIGN.md), a review [REVIEW.md](REVIEW.md). This file describes what is implemented.

## Build and run

```sh
go build -o entish .

entish                    # interactive (or a script on stdin)
entish script.ent a b     # run a file; $args is ['a' 'b']
entish -c 'ls | lines |> count'
entish -x script.ent      # log each external command to stderr before it runs
```

Startup file: `~/.entrc`.

## The language

```sh
let name = 'world'
let langs = ['go' 'bash' 'nix']
let n = len $langs
print "hello $name, $n langs"                 # hello world, 3 langs

fn add a b { $a + $b }                        # the body's values are the fn's values
print (add 1 2) (0.1 + 0.2 == 0.3) (7 / 2)    # 3 $true 3.5

let mode = if test -d /etc { 'release' } else { 'debug' }

ls *.go | lines |> each { print "go: $it" }   # output -> values -> each
find . -print0 | lines0 |> each { file $it }  # each NUL-separated record
...[1 2 3] |> each { $it * $it } |> print     # 1 4 9, one per line

fn ll ...args { ^ls -1 ...$args }             # wrapper; ^ reaches the external
let count = ll | lines |> count               # a wrapper reads like the external

fn build { go build -o app .; 'app' }         # output goes on, 'app' is the value
let bin = build

false || print 'fallback'
let cfg = try { $(cat config.json) } catch e { print "exit $e[code]"; 'default' }

export PATH="$HOME/bin:$PATH"
env GOOS=linux go build ./...                 # env for one command: the external env
```

### Rules

**Keywords (7):** `let fn if else try catch match`. There are no loops; use `each` (it maps and filters too) or recursion.

**Calls**
- `name args...` calls the fn `name` if one is defined, else the external `name` from PATH. `^name` is always the external; a word with `/` is a path. `^$cmd args...` runs the external named by the str `$cmd`.
- Resolution is static. Calling a fn or builtin with the wrong number of args is a compile error, on every branch.

**Output and values**
- Every call emits output (stdout bytes) and values (0 or more). A fn emits every value its statements emit, in order, as its output is everything its statements write; an external, `print`, `to_json` and `cd` emit none. There are no fn kinds: a fn may print and emit values.
- Each operator takes one of the two:

  | | takes | the rest |
  |---|---|---|
  | `a \| b` | a's output → b's stdin | a's values are dropped |
  | `a \|> b` | a's values → b's value input, one at a time | a's output goes on to stdout |
  | `$( a )` | a's output as one `str`, trailing newline trimmed | a's values are dropped |
  | `( a )`, `let x = a` | a's value, exactly one, else raise | a's output goes on to stdout |
  | `a \|> list` | a's values as one list | |
  | `...$xs` | a list's items, as args, list items or values | |

- Only what the source alone makes certain is a compile error:
  - `let x = ls` → `ls has no value: capture its output with $( ) or read it with | lines`; `ls |> lines` → `pipe its output with |: ls | lines`
  - `'text' | cat`, `len $x | cat` → a value cannot be piped; `print` it. `1 | to_json` → pass it with `|>`.
  - `$xs |> jq .` → `jq reads stdin, not values; print the values: $x |> print | jq`.
  - `ls | len` → `len does not read stdin; it takes its data as an arg: len $x`; `$xs |> len` → `len takes its data as an arg, not values`.
- The rest is the caller's job: `$(f)` of a fn that only emits values is `''`; `let x = ll` of a wrapper raises `ll emitted no value`.
- At the REPL, the values of each statement you typed are shown after the command, marked `▶`. They go only to the screen, never into a pipe or file. Values dropped on purpose are not shown: before `|` (`f | cat`), in `$( )`, in a condition. A script drops them.

**Pipes**
- `|` carries bytes from the output of one stage to the stdin of the next.
- `|>` carries values to the value input of the next stage: a fn (its body reads them, e.g. with `each`) or a builtin that reads values (`each count list print to_json`). An external reads no values.
- `$_` in a stage after `|>` is the item: the stage runs once per value, and `$_` goes where the item goes: `ls | lines |> mv $_ $_.bak`, `...$xs |> $_ * 2`. A `{ }` block does not see it; anywhere else it is a compile error.
- Bytes → values: a converter with its data arg left out reads stdin like `cat` and emits a value per record: `ls | lines`, `find . -print0 | lines0`, `curl -s $u | from_json`. In a fn that is the fn's stdin: `fn eachl f { lines |> each $f }`. `$( )` gives the whole output as one str.
- Values → bytes: only `print` and `to_json` (`from_json` is its inverse): `ls | lines |> to_json | jq .`.
- All stages run at the same time and values stream, through fns too: `tail -f log | lines |> each {..} |> each {..}` handles each line as it is written. A statement ends when all its stages have, so variables hold complete values.
  - A failing producer raises when the statement ends; values before that were already handled.
  - If a stage stops reading, the one before it stops too (SIGPIPE, a closed value pipe, or killed if it isn't writing).

**Variables**
- `$name` (lowercase) is a variable: immutable, lexically scoped, resolved before running. Rebind with `let`.
- `$NAME` (uppercase) is an env var; `$env:name` reads any env var.
- Env vars are written only with `export NAME=value`. An unset env var is `$nil`, and passing it to a command is an error.

**Values:** `str num bool list map fn nil`
- `num` is an exact rational: no floats, no overflow. A number keeps its spelling until arithmetic (`1.10` stays `1.10` in args).
- There is no truthiness: conditions must be `bool`, and comparing different types is an error.
- Errors are maps: `[cmd: .. code: .. signal: .. msg: .. pos: ..]`.

**Words and quoting:** values are never re-split or globbed. Quotes are only needed for entish's own special characters.

| form | meaning |
|---|---|
| `'raw'` | no interpolation; `''` is one `'` |
| `"x $name ${name}_y $m[key]"` | variables only; `$( )` in a string is a compile error; escapes `\n \t \e \xHH \u{..} \$`; a `\` at a line's end continues the string, as in bash |
| `\\raw line` | multiline raw string, one prefix per line, no escapes |
| `$\\line $name` | multiline line with variable interpolation |
| `*.go` `img?.png` | glob (`*` `?`; no `[ch]` classes); its paths become args, list items or values; where one value is needed it is an error (`[*.go]` is the list); no match is an error |
| `['a' 1 $x]`, `[k: 'v']` | list, map; strings inside must be quoted |
| `...$xs`, `...[1 2]`, `...(expr)` | a list's items, spread into args, a list or a value stream |
| `$( cmd )` | its output as `str`; its values are dropped |
| `( expr )` | its value, exactly one; its output goes on to stdout |
| `{ ... }`, `{ \|x\| ... }`, `{ $it }` | lambdas: `$it` makes one param; with no params and no `$it` the args are ignored |

```sh
let conf =
    \\server {
    $\\    name $name;
    \\}
print $conf > nginx.conf
```

**Conditions and failure**
- A failing command raises.
- In a condition, failure is `false` instead: the condition of `if`, the left side of `||`, and the operand of `!`.
- Ctrl+C is never handled: a command killed by SIGINT raises even in a condition, and `try` does not catch it.
- A command in a condition is tested by its status; anything else, `( )` included, must be one `bool`: `if test -f x`, `if (has $xs $x)`, `if $n > 0`. `if big 1` where `fn big` ends in a value is a compile error.
- `a && b` raises if `a` fails. `a || b` runs `b` if `a` fails. Between values they are bool operators: `$a && $b`.
- Pipefail is on; a stage killed by SIGPIPE (`yes | head`) is not a failure.
- An exit code as a value: `status { diff -q a b }` emits 0, 1, ...; a failure with no code (a signal, `fail`) and Ctrl+C still raise. `match (status { grep -q x f }) { 0 => ..; 1 => ..; c => fail "grep: $c" }`.

**Builtins:** `print cd export exit fail status call eval source src abbr bind num str round lines lines0 from_json to_json as_file len each count list keys assoc`. `each count list print to_json` read the value input, so they go after `|>`; the others take their data as args.

## Interactive editor

The editor is multi-line and vi-style, and starts in insert mode. Insert mode also accepts emacs keys (`Ctrl-A E K U W`, arrows).

| key | action |
|---|---|
| Enter | run, or start a new line while the input is unfinished (open bracket, trailing `\|` `\|>` `&&` `\|\|` `\\`) |
| Esc | normal mode (block cursor) |
| normal mode | `h l w b e W B E 0 ^ $ f F t T ; ,`, `d c y` + motion or text object (`iw a" i(` …), `dd cc yy`, `x X s S C D Y r ~ p P J`, `u` `Ctrl-R` `.`, counts |
| `j` `k` / ↑ ↓ | move between lines, then through history |
| `v` (normal) | edit the input in `$VISUAL` / `$EDITOR`; the result comes back unrun |
| Tab | complete (async); with several matches a menu opens with the first selected and the input unchanged; Tab moves to the next, Enter puts it in without running |
| Ctrl-F, → at the end | take the gray suggestion |
| Ctrl-R (insert), `/` (normal) | history search |
| Ctrl-C | discard the input; while a command runs, interrupt it |
| Ctrl-D | exit on an empty input |

Pasting is bracketed, so pasted newlines don't run anything.

The input is colored as it is typed: keywords magenta, fns and builtins cyan, externals blue, `|` and `|>` yellow. Calls are colored as the checker resolves them, so a fn that shadows an external (`fn ls`) shows as a fn.

### History and search

- History is stored in sqlite at `$XDG_DATA_HOME/entish/history.db` (default `~/.local/share/entish/history.db`), with the command, cwd and time.
- Concurrent shells share it; each sees the others' entries at its next prompt.
- Search uses fzf's query syntax and lists newest first:

| query | matches |
|---|---|
| `gst` | fuzzy |
| `'stat` | exact substring |
| `^git` `log$` `^ls$` | prefix, suffix, whole |
| `!log` `!^git` | exclude |
| `a \| b` | either; space-separated terms must all match |

Matching is smart-case. Enter puts the selected entry into the input without running it; Esc restores the input.

### Suggestions

The history entry that extends the input and is most likely next is shown in gray after the cursor; Ctrl-F takes it. Each match is scored by how often it ran, how often in the current directory and how often right after this shell's previous command; older uses count less (half after a week), and ties go to the newest. The source is a generic, asynchronous `Suggester`, so it can be replaced (e.g. by an AI tool) without blocking typing.

### Completion

Tab completes asynchronously: typing never waits for it, any other key cancels it, and "completing…" appears if it takes over 150 ms.
- Where a command can start: keywords, builtins, fns, and executables on PATH. PATH is scanned in the background at startup and again when it changes. `^na` offers externals only.
- Elsewhere: paths relative to the shell's cwd. Hidden files only after a `.`. Names with special characters are quoted.

### Prompt

nushell-style:
- **Left:** the cwd in green (`~` for home) and `> `.
- **Right:** `[direnv]` when direnv has an `.envrc` loaded (`DIRENV_DIR` is set), the last exit code in red if it was not 0, then the date and time. The right prompt is hidden when the line gets long.
- **Continuation lines:** `::: `.
- **Slow commands:** a command that takes 5 seconds or more is followed by `took 5.3s`.

### direnv

If `direnv` is on PATH, entish runs `direnv export json` before every prompt, in the background:
- The prompt and typing never wait for it. When it finishes, the right prompt updates (`[direnv]`) even mid-edit.
- Its env changes are applied right before the next command runs. If direnv is still running then, the command waits for it, up to 5 seconds; after that it runs without the changes and a note is printed.
- direnv's own messages are held back and printed at that point, so they don't break the line being edited.

### Terminal integration

- The shell emits OSC 7 (the cwd; tmux records it as `pane_path`) and OSC 133 prompt and command marks (A, B, C, D with the exit status).
- The cursor shape follows the mode.
- Escape sequences in the prompt don't count toward its width.

## Layout

```
main.go     wiring, rc file, signals, prompt
term.go     OSC 7 / OSC 133
repl/       read-eval-print loop; knows only the Editor and Language interfaces
lineedit/   editor: vi mode, history search, suggestions, async completion
complete/   completers: command names, files
history/    sqlite history store
lang/       parse → check (names, kinds, arity, scope) → eval
```

The editor and the language are behind small interfaces, so either can be replaced.

## Not implemented yet

- Language: `in_dir`, `explain`, builtins `split_by join_with reduce chunk sort_by count_by get ends_with`, string ops (`fold -w1` stands in for chars), unsetting env vars, regex, modules, floats, tail calls.
- Shell: job control (Ctrl-Z leaves the shell waiting); completion beyond names and files.
