# entish

A small shell with a strict, functional language. Not POSIX-compatible.

- One call syntax for fns and external commands; fns are resolved first, `^name` forces the external.
- Every call writes output and returns a value; `|` carries output, `|>` carries values. Nothing is converted implicitly.
- Mistakes are compile errors, found before anything runs.
- Failing commands raise; there is no silent `$?`.

`entish` is a placeholder name. The design and its rationale are in [DESIGN.md](DESIGN.md), a review in [REVIEW.md](REVIEW.md). This README describes what is implemented.

## Build and run

```sh
go build -o entish .

entish                    # interactive (or a script on stdin)
entish script.ent a b     # run a file; $args is ['a' 'b']
entish -c 'ls | lines |> len'
entish -x script.ent      # log each external command to stderr before it runs
```

Startup file: `~/.entrc`.

## The language

```sh
let name = 'world'
let langs = ['go' 'bash' 'nix']
let n = len $langs
print "hello $name, $n langs"                 # hello world, 3 langs

fn add a b { $a + $b }                        # the last expression is the value
print (add 1 2) (0.1 + 0.2 == 0.3) (7 / 2)    # 3 $true 3.5

let mode = if test -d /etc { 'release' } else { 'debug' }

ls *.go | lines |> each { print "go: $it" }   # stdout -> list -> each
find . -print0 | lines0 |> each { file $it }  # each NUL-separated record
[1 2 3] |> map { $it * $it } |> print | tr '[]' '()'

fn ll ...args { ^ls -1 ...$args }             # wrapper; ^ reaches the external
let count = ll | lines |> len                 # a wrapper reads like the external

fn build { go build -o app .; 'app' }         # output goes on, 'app' is the value
let bin = build

false || print 'fallback'
let cfg = try { $(cat config.json) } catch e { print "exit $e[code]"; 'default' }

export PATH="$HOME/bin:$PATH"
env GOOS=linux go build ./...                 # env for one command: the external env
```

### Rules

**Keywords (6):** `let fn if else try catch`. There are no loops; use `each`, `map`, `filter`, or recursion.

**Calls**
- `name args...` calls the fn `name` if one is defined, else the external `name` from PATH. `^name` is always the external; a word with `/` is a path.
- Resolution is static. Calling a fn or builtin with the wrong number of args is a compile error, on every branch.

**Output and values**
- Every call writes output (stdout) and returns a value. A fn returns its last statement's value; an external, `print`, `to_json`, `cd` and `each` return `$nil`. There are no fn kinds: a fn may print and return a value.
- Each operator takes one of the two:

  | | takes | drops |
  |---|---|---|
  | `a \| b` | a's output → b's stdin | a's value |
  | `a \|> f` | a's value → f's last arg | nothing; a's output goes on to stdout |
  | `$( a )` | a's output as one `str`, trailing newline trimmed | a's value |
  | `( a )` | a's value | nothing; a's output goes on to stdout |

- What is dropped is dropped silently: `$(f)` of a fn that only returns a value is `''`, `let x = ll` of a wrapper is `$nil`. Only what the source alone makes certain is a compile error:
  - `let x = ls`, `ls |> lines` → `ls has no value: capture its output with $( ) or read it with | lines`
  - `'text' | cat`, `len $x | cat` → a value cannot be piped; `print` it.
  - `$xs |> jq .` → `jq reads stdin, not a value: print $x | jq`.
  - `ls | len` → `len takes a value, not stdin: ls | lines |> len`.

**Pipes**
- `|` carries bytes from the output of one stage to the stdin of the next.
- `|>` passes the value of everything before it as the last arg of a fn. An external takes no value.
- Bytes → value: `$( )`, or a converter with its data arg left out, which reads stdin like `cat`: `ls | lines`, `find . -print0 | lines0`, `curl -s $u | from_json`. In a fn that is the fn's stdin: `fn eachl f { lines |> each $f }`.
- Value → bytes: only `print`, and `to_json` (prints JSON; `from_json` is its inverse): `ls | lines |> to_json | jq .`.
- The value of `a | b` is b's value; a stage's output that is not piped goes to stdout.
- Pipelines stream. `cmd | lines |> map {..} |> filter {..} |> each {..}` handles each line as `cmd` prints it; endless producers (`yes`, `tail -f`) work.
  - `lines`, `lines0`, `map`, `filter` and `each` pass items on one at a time. Anything else (another fn, a variable, the end of the pipeline) collects everything first, so variables never hold lazy values: `let xs = cmd | lines` is a plain list. `$(cmd) |> lines` gives the same list without streaming.
  - A failing producer raises when its output ends; items before that were already handled.
  - If the consumer stops early (an error), the producer is stopped (SIGPIPE, or killed if it isn't writing).

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
| `"x $name ${name}_y $m[key]"` | variables only, never code; escapes `\n \t \e \xHH \u{..}` |
| `\\raw line` | multiline raw string, one prefix per line, no escapes |
| `$\\line $name` | multiline line with variable interpolation |
| `*.go` `img?.png` `*.[ch]` | glob; expands to args in calls, is a list elsewhere; no match is an error |
| `['a' 1 $x]`, `[k: 'v']` | list, map; strings inside must be quoted |
| `...$xs`, `...(expr)` | spread a list into args |
| `$( cmd )` | its output as `str`; its value is dropped |
| `( expr )` | its value; its output goes on to stdout |
| `{ ... }`, `{ \|x\| ... }`, `{ $it }` | blocks and lambdas; a block using `$it` takes one arg |

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
- A call in a condition is true when it succeeds and returns `$nil` (a command) or `$true`, false when it fails or returns `$false`. Any other operand must be a `bool`: `if test -f x`, `if has $k $m`, `if $n > 0`.
- `a && b` raises if `a` fails. `a || b` runs `b` if `a` fails.
- Pipefail is on; a stage killed by SIGPIPE (`yes | head`) is not a failure.
- An `if` whose value is used must have an `else` if its then-branch ends in an expression: `fn sign x { if $x < 0 { 'neg' } }` is an error; a branch ending in a call (`print`, `fail`, a fn) needs none.

**Builtins:** `print cd export exit fail call eval abbr bind num str round lines lines0 from_json to_json len map filter each keys assoc`. Collection fns take their data last, so they compose with `|>`.

## Interactive editor

The editor is multi-line and vi-style, and starts in insert mode. Insert mode also accepts emacs keys (`Ctrl-A E K U W`, arrows).

| key | action |
|---|---|
| Enter | run, or start a new line while the input is unfinished (open bracket, trailing `\|` `\|>` `&&` `\|\|`) |
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

The newest history entry that extends the input is shown in gray after the cursor; Ctrl-F takes it. The source is a generic, asynchronous `Suggester`, so it can be replaced (e.g. by an AI tool) without blocking typing.

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

- Language: `in_dir`, `command $name`, slices `$xs[1:3]`, `explain`, builtins `split_by join_with reduce chunk sort_by count_by get ends_with`, unsetting env vars, regex, modules, floats, `**`, `~`, tail calls.
- Shell: job control (Ctrl-Z leaves the shell waiting); completion beyond names and files.
