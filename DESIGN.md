# entish — minimal functional shell language design

Status: mostly implemented; README lists what works and sections marked TODO do not exist yet. STREAMS.md has the full rules of the two-stream model; LEARN.md is a short tour.

## Principles

entish is at a very early stage. If a better decision exists, overturning existing rules and principles, these included, is fine. No principle is absolute; the goal is a language that makes sense and is predictable. Convenience for the user and less typing matter too.

1. **One call syntax; fns first, externals second.**
   - `name a b` calls a fn if `name` is a fn, otherwise the external `name` from PATH.
   - Resolution is static and lexical: decided before running, never by a runtime lookup.
   - `^name` always means the external. Same model as nushell.
2. **Values are immutable; effects come from externals and a few effect builtins** (`print`, `to_json`, `cd`, `export`, `exit`).
3. **Values and callables never mix.**
   - values: always `$`-prefixed — `$x` (lowercase: variable), `$HOME` (uppercase: env var), `$true`, `$nil`
   - callables: bare word at the head — keyword > fn > external
   - a variable never shadows a callable
4. **Fns exist only inside entish.** For use from `sudo`, `xargs`, `find -exec` or other shells, write a script on PATH.
5. **Two streams, one operator each.** Every call emits output (stdout bytes) and values (0 or more).
   - `|` carries output to the next stdin
   - `|>` carries values to the next call's value input
   - `|%` runs the next call once per value, the value as its last arg or where `$_` is
   - `$( )` captures output as a `str`; `( )` is exactly one value
6. **Typed boundary to externals.**
   - in: values must be stringable; lists need explicit `...$xs`; maps/fns are errors
   - out: always `str`; convert explicitly with `num`, `lines`, `from_json`
7. **Errors are loud.** Nonzero exit raises unless the command is in condition position. Pipefail is on.
8. **No POSIX compatibility. No job control.**

## Keywords (7)

```
let  fn  if  else  try  catch  match
```

- Reserved only at statement head and at the start of a value. `print if try` prints `if try`.
- `else` and `catch` are recognized only right after a `}` on the same line.
- No `for`: `|%` runs a stage once per value, so it also maps and filters (`|% $_ * 2`, `|% if $_ > 0 { $_ }`); so does `each` (`|> each { $it * 2 }`); or recurse.
- No `set`, `return`, `break`, `continue`.
  - no mutation: rebind with `let` (shadowing)
  - every expression statement emits its values; a block's values are those of its statements, in order
  - a value nobody receives is dropped; the REPL shows those of each statement
  - early exit: `if`; loops: `|%`, `each`, recursion

## Builtin fns

All builtins are fns. A user `fn` of the same name shadows a builtin (planned: a warning).

| kind | implemented | planned |
|---|---|---|
| effect | `print prompt cd nav export exit` | |
| control | `fail status call eval source` | |
| meta | `src` | `explain` (TODO) |
| scope | | `in_dir` |
| editor | `abbr bind` | |
| convert | `num str quote round lines lines0 from_json to_json as_file` | `split_by join_with` |
| stream (read the value input, after `\|>`) | `each count take list pick` (and `print`, `to_json` with no args) | |
| collection (data as an arg) | `len keys assoc` | `sort_by count_by get ends_with` |

- Examples in this document use planned builtins too.
- `prompt question` displays a string on `/dev/tty`, reads one line there, and emits it as a string. Enter accepts even an empty answer. Ctrl+C interrupts; EOF or a missing terminal raises.
- Converters read stdin: `lines`, `lines0` (NUL-separated) and `from_json` with their data arg left out read their stdin, like `cat` with no file, and emit one value per record as it arrives. After `|` that is the previous stage's output: `ls | lines`, `find . -print0 | lines0`, `curl -s $u | from_json`. In a fn it is the fn's stdin, so `fn eachl f { lines |% $f }` gives `ls | eachl {..}`. `ls | len` is a compile error: len takes its data as an arg. `lines` alone at the prompt waits for terminal input (end it with Ctrl-D).
- `print` writes its args, separated by spaces, and a newline; `to_json` writes its one arg as JSON on a line. With no args they write each value of their value input on its own line: `xs |> print`, `$xs |> to_json | jq .` (one JSON value per line). They emit no values. `$(to_json $x)` gives the JSON as a str. Map keys keep their order. A num with no exact decimal (`1/3`) and a fn raise.
- `from_json` emits each JSON value of a str or of its stdin (NDJSON streams; an array document is one value): numbers are exact nums (`0.1` is 1/10), objects keep key order, `null` is `$nil`; a duplicate key raises.
- `each f` calls f with each value of its value input and emits what f emits; `xs |> each f` is `xs |% f`, so `each` is for a fn that processes its own value input (`fn tag { each { ... } }`). `count` emits how many values came in, `list` emits them as one list; `...$xs` is the reverse.
- `pick` lets the user pick values of its value input on the terminal (`/dev/tty`), like fzf over lines, and emits the picked ones unchanged: `git branch | lines |> pick |% git switch $_`. Each value is shown as `print` would show it. Tab marks several; Enter emits the marked ones in input order, or the selected one. Esc emits nothing; Ctrl+C interrupts like SIGINT. No values: no picker, nothing emitted.
  Ctrl+T is a separate editor mode: it uses the existing prompt and input rendering, with the picker below. It finds files asynchronously with `fd`, using the shell's cwd and environment, and previews quoted paths at the saved cursor as selection changes. Tab marks multiple files; Enter commits one undoable edit without executing the input; Esc or Ctrl+C restores the original input and cursor. User `bind ctrl-t` bindings override this default mode.
  While traversing history with Ctrl+P/Up, Ctrl+P/Ctrl+N and Up/Down take precedence over user bindings and select previous/next history entries. Returning to the unfinished input or pressing another editing key leaves this mode, restoring ordinary bindings and Ctrl+N (the navigator).
- `nav [path]` browses directories on `/dev/tty`, in a panel below the cursor (half the terminal, like fzf `--height=50%`; no alternate screen), starting at the shell's cwd or the given path (relative to cwd). It shows parent/current/preview columns, with directories first; narrow terminals show only the current column. `h/j/k/l` or arrows navigate, `/` filters the current directory, `.` toggles hidden entries, and Ctrl+L reloads. Enter finishes a filter; outside filtering, Enter changes cwd to the displayed directory through `cd`. Esc clears an active filter; otherwise Esc or `q` cancels without changing cwd. Ctrl+C interrupts and is not caught by `try`. It emits neither values nor output, reads neither stream, and raises without a terminal. In the editor, Ctrl+N opens the same navigator as an editor mode, like Ctrl+D: the input stays drawn above it, and Enter changes cwd and keeps the input. File previews read at most 16 KiB of regular files; special files are never previewed, and terminal control characters are rendered inert.
- `as_file` copies its stdin byte for byte to a new temp file and emits the path, for commands that take only files (process substitution without new syntax): `diff (sort a | as_file) b`, `comm -12 (sort x | as_file) (sort y | as_file)`, `docker run --env-file (pass show env | as_file) img`.
  - It reads only stdin: `$s |> as_file` and `as_file $s` are compile errors, since only `print` and `to_json` turn values into bytes (`$s |> print | as_file`).
  - The file is a real file, so commands that seek or read twice work (zsh `=( )`, not bash `<( )`). The producer runs to the end first, so it does not stream.
  - A failing producer raises at `as_file`. The file is in `$XDG_RUNTIME_DIR` (else the system temp dir), mode 0600, and is removed when the input ends: a REPL input, or the whole script, so `let p = (cmd | as_file)` can be used on later lines.
  - bash's `<(cmd)` is a redirect of `( )` here and a compile error with a hint to `as_file`.

- Builtins that read the value input (`each count take list pick`) go after `|>`; the others take their data as args, last: `len $xs`, `keys $m`, `round 2 $x`, so after `|%` the value fills it: `$x |% round 2`.
- Names avoid coreutils (`split`, `join`, `fold`), so builtins never hide a common external.
- `^$cc -o prog prog.c` runs the external named by the str `$cc`, skipping fns (see Call resolution).

### Names

- fn names: `[a-z_][a-z0-9_]*` (snake_case).
- An external with `-` in its name (`git-lfs`) can never be shadowed by a fn.

## Call resolution

### Statement head

| head | meaning |
|---|---|
| keyword | keyword form |
| `^name` | external from PATH, skips fns |
| `^$name` (indexing allowed: `^$m[k]`) | external named by the str in `$name`, from PATH or by path if it has `/`; skips fns |
| word containing `/` (`./x`, `/bin/x`) | external by path |
| bare word bound to a fn (user `fn` or builtin) | fn call |
| any other bare word | external from PATH |
| `$name`, `{ }` or `( )` followed by args | call a fn value (lambda) |
| `call $name`, `call { }`, `call ( )` | call a fn value with no args |
| `$` `(` `[` `"` `'` number, glob | expression statement (REPL prints value) |

- fn definitions are visible in their whole enclosing block (hoisted), so resolution never depends on line order. At the REPL a definition applies to later lines.
- `[` at the head is a list, so the external `[` is not reachable as `[ -f x ]`. Use `test -f x`.
- The arg count of every call to a named fn or builtin is checked before running, on every branch; the value from a preceding `|%`, and stdin for a converter after `|`, counts as the last arg; `|>` adds none. Only args whose count is unknown until run time (a glob, `...$xs`) defer the check. Externals and fn values (`$f`) are not checked; a lambda written after `|`, `|>` or `|%` is, since its params are in the source.
- A bare `$f` is always the value itself, never a call; that keeps `if $c { $a } else { $b }` returning `$a` whatever it holds. `$f x`, `x |% $f` (x as its arg), `x |> $f` (no args; its value input is the stream), `cmd | $f` (no args; its stdin is the pipe) and `try $f` call it. A lambda `{ }` or a group `( )` stands where `$f` does, with the same meaning: alone it is a value; `{|x| ... } 1`, `(twice $inc) 1`, `x |% { ... }`, `x |% (adder 2)`, `x |> { ... }` and `cmd | { ... }` call it. `call` calls one of them with no args: `call $f`, `call $m[k]`, `call { ... }` (like a JS IIFE; its `let`s stay inside), `call (mk)`. A `( )` whose value is not a fn raises at run time, like `$f`. A bare `$f` as a statement emits the fn value instead of calling it; to call it: `call $f`.
- Fns are values. `fn f` also binds `$f`, from its definition on, like `let` (the call name `f` is hoisted, `$f` is not); inside its body `$f` is the fn itself. A later `let f` shadows `$f` only; the call `f` is unaffected. Builtins are values too (`map $lines`) unless a variable of that name is in scope. `call` has no value. See NAMESPACES.md for the alternatives considered.

### Shadowing

```sh
fn ls ...args { ^ls --color=auto ...$args }   # wrapper; ^ reaches the external
ls -la                                        # fn ls
^ls -la                                       # external ls
sudo ls                                       # sudo is external; it runs /usr/bin/ls, fns don't exist outside entish
```

- Defining a fn with the name of a PATH executable or a builtin → warning, unless the body calls `^name` (an intentional wrapper).
- A fn that calls itself by its own name without `^` → warning: `did you mean ^ls?`

### eval

`eval src ...args` runs a str as entish code, like an in-process `entish -c src args...`.

```sh
eval 'print (1 + 2)'                   # 3
eval 'rm ...$args' $f                  # values go in as $args, never spliced into the code
$f |% eval 'rm ...$args'               # a value from |% is the last arg
let double = (eval '{ |x| $x * 2 }')   # the last statement is the value, here a fn
```

- The source is parsed and checked when the call runs, as the body of `fn _ ...args { src }` at the top level. Its errors raise at the call (`eval: <eval>:1:5: ...`) and can be caught with `try`.
- It sees top-level fns, builtins, env vars and `$args`; never the caller's variables. Its own `fn` and `let` do not leak out.
- Its result is that of a fn body: its output, and the value of its last statement.
- The source is any str: `eval $(cmd)`, `eval "rm $f"`. Interpolating a value into the source splices it into code, so `$f` holding `x; rm -rf ~` runs both; pass values as args instead: `eval 'rm ...$args' $f`. `'src' |% eval` is `eval 'src'`.
- To run a command whose name is a value, use `^$name args...`, not `eval`: the name is not parsed, and the args are passed as they are.

### source

`source path` reads a file of entish code into the program, as if its statements were written in place of the `source` statement.

```sh
source ~/.config/entish/git.ent     # its fns and lets are visible after this line
source lib/util.ent                 # relative to the file this line is in
```

- Expanded before checking, so the file is checked with the program: its errors are compile errors that point into it.
- The path must be literal (bare word, `'str'`, `"str"` without variables, `~/...`), so the file is known before anything runs. `source $f` is a compile error.
- Only a statement at the top level of a file, `-c`, a REPL input or `eval`; `source` inside a fn, block or pipeline is a compile error. Its file is read once per run of the program that contains it.
- A relative path is relative to the directory of the file containing the statement; at the REPL, in `-c`, stdin and `eval`, to the shell's cwd when the input is compiled, so a `cd` earlier in the same input does not change it.
- A sourced file cannot define `fn source` (compile error): it would change what `source` means in the file that sources it.
- A cycle (`a` sources `b` sources `a`) is a compile error.
- At the REPL, running `source f` again re-reads `f`; its fns replace the old ones, and fns defined earlier that call them by name see the new definitions. That is the reload of REPL-driven development.
- A missing file is a compile error; there is no optional `source`.
- A `fn source` shadows it, like any builtin.

### src

`src $f` returns the source of a fn or lambda as written, a str: `fn add a b { $a + $b }` or `{ $n > 0 }`. Variables appear as written, not as their captured values. A builtin raises. It lets a fn report the code it was given:

```sh
fn check body {
  if call $body { $nil } else {
    let s = (src $body)
    fail "check failed: $s"
  }
}
check { $n > 0 }                    # check failed: { $n > 0 }
```

`src` only reads code. Text it returns can be run with `eval $s`, which gives back a lambda for a lambda's source; it is never spliced.

### explain

**TODO: not implemented; the spec is not settled.**

`explain` shows how a line resolves, without running it.

```
$ explain 'xx; ^xx; gofmt -l $xs; make || print failed'
fn        xx     → ~/.entrc:3   (shadows /usr/bin/xx)
external  ^xx    → /usr/bin/xx
external  gofmt  → /run/current-system/sw/bin/gofmt
  error   $xs    → list passed to external, use ...$xs
external  make   → /run/current-system/sw/bin/make   (condition position: failure → false)
builtin   print  (effects: stdout)
```

## Output and values

Every call — fn, builtin, external — produces three things:

| | what |
|---|---|
| **output** | stdout bytes, written to wherever the call's stdout is connected |
| **values** | 0 or more. A fn: every value its body's statements emit, in order. An external, `print`, `to_json`, `cd`, `export`: none |
| **status** | success, or failure: raises, except in condition position |

Everything a fn's body writes is the fn's output and everything it emits is its values, as a bash fn's stdout is everything in it. Output and values are independent, so a fn can do both:

```sh
fn build target {
    go build -o "dist/$target" ./cmd/app      # output: goes to the caller's stdout
    "dist/$target"                            # value
}
let bin = (build linux)                       # build output on the terminal, $bin is the path
build linux | tee build.log                   # output to tee, the path dropped
```

Each operator connects exactly one of them:

| syntax | connects | the rest |
|---|---|---|
| `a \| b` | a's output → b's stdin | a's values are dropped |
| `a \|> b` | a's values → b's value input, one at a time | a's output goes on to the pipeline's output |
| `a \|% b` | each of a's values → b's `$_` or last arg; b runs once per value | a's output goes on to the pipeline's output |
| `$( a )` | a's output → one `str`, trailing newline trimmed | a's values are dropped |
| `( a )`, `let x = (a)` | a's value: exactly one, else raise | a's output goes on |
| `a \|> list` | a's values → one list | |
| `...$xs`, `...[1 2]`, `...( a )` | a list's items → args, list items or values | |

There are no fn kinds. A wrapper is just a fn, and behaves like the external it wraps in every position, because the external has no values either:

```sh
fn ls ...args { ^ls --color=auto ...$args }
ls | lines             # output → values, one per line
cat app.log | grep ERR # a fn on the right of |: its externals read the pipe
let s = $(ls)          # output as str
let x = (ls)           # raises: ls emitted no value (^ls itself would be a compile error)
```

### Why not kinds (v1, rejected)

v1 classified every fn as a **value fn** or a **command fn** from its last statement, and the classification decided behavior: `f |> g` passed f's value or f's stdout, `let x = f` was a value or a compile error. The kind was inferred and never written, so behavior depended on something the user could not see:

- A recursive fn whose only evidence was a call to itself had no kind; the checker guessed `value`, and `fn f n { if $n > 0 { print hi; f ($n - 1) } }` failed with "needs an else". Swapping its two statements made it pass.
- A debug `print` as the last line turned a value fn into a command fn and broke its callers.
- An expression mixing kinds (`let x = if $c { print hi } else { 1 }`) slipped past the fn-level check and bound `$nil`.

In v2 inference never changes behavior; checks use syntax only.

### What is checked, and what is the user's responsibility

Compile errors only where the source alone makes the mistake certain:

| source | error |
|---|---|
| `let x = f`, `match f { ... }` — a call written bare where a value goes | `a call where a value goes needs ( ): (f) for its values, or $(f) for its output` |
| `let x = ls` | `ls has no value; for its output: $(ls)` |
| `(ls)` — an external or `print`/`cd` used as a value | `ls has no value: capture its output with $( ) or read it with \| lines` |
| `ls \|> count` — the same, left of `\|>` or `\|%` | `ls has no value; make its output values: ls \| lines \|> count` |
| `$xs \|> jq .` — an external right of `\|>` | `jq reads stdin, not values; pass each value as an arg: \|% jq $_, or print the values: $x \|> print \| jq` |
| `$xs \|% jq .` — an external right of `\|%` without `$_` | `jq is an external, whose arg order is its own; write where each value goes with $_: \|% jq $_` |
| `$xs \|% 3 * 2` — a stage right of `\|%` that is not a call and has no `$_` | `a stage after \|% that is not a call must use $_, the value: \|% $_ * 2` |
| `$xs \| jq .`, `len $x \| cat` — an expression or a builtin that writes nothing, left of `\|` | `a value cannot be piped into \|; print it: $x \|> print \| jq` |
| `ls \| len`, `ls \| print` — a builtin other than a converter after `\|` | `len does not read stdin; it takes its data as an arg: len $x` |
| `$xs \|> len` — a builtin that takes args, right of `\|>` | `len takes its data as an arg, not values: len $x, or for each value: \|% len` |
| `$xs \|% tag` with `fn tag { each { ... } }` — a stage that reads its value input, right of `\|%` | `tag reads the value input, but a stage after \|% runs once per value and gets none` |
| `$xs \|% count` — the same, with the value as an arg too many | `count takes 0 args, got 1; \|% passes each value as the last arg; to give it all the values: \|> count` |
| `$xs \|> add1` with `fn add1 x` | `fn add1 takes 1 args, got 0; \|> feeds the value input, not args; to pass each value as the last arg: \|% add1` |
| `xs \|% print (...$ys \|% $_ + 1 \|> list)` — the only `$_` is an inner stage's | `the $_ in this stage is an inner stage's; write this stage's own $_ where its value goes` |
| `x$(len $xs)` — `$( )` around something that writes nothing | `len writes no output, and $( ) captures output; for a value use ( )` |
| `if big 1` where `fn big` ends in a value | `big ends in a value, so as a command it always succeeds; to test its value: (big ...)` |
| `let xs = *.go` — a glob where one value is needed | `a glob gives several paths, not one value; to collect them into a list: [*.go]` |
| `"today $(date)"` | `a string interpolates variables, never code; bind it first: let s = $( ... )` |

The pipe checks are one rule: `|` needs output on its left and a stdin reader on its right; `|>` and `|%` need values on their left; `|>` needs a reader of values on its right, `|%` a call that takes the value as an arg (not an external) or uses `$_`, and not one that reads its value input, since it gets none. A side is rejected only when the source makes certain it lacks the stream; a user fn may have both, so it passes, except after `|%` when its body reads its value input. A failing side has two fixes, the other operator and an explicit conversion (`print`, `$( )`, `| lines`), and the hint lists those that pass the same checks: `1 | to_json` → `pass it with |>: $x |> to_json`, `$x | jq` → only `print it`, `ls |> jq` → only `pipe its output with |`, `ls |> lines` → only `ls | lines`.

Calls to user fns and fn values are never classified by what they emit, so they are errors here only for arity, or after `|%` when the body reads its value input. Dropping values or output is not an error either:

| mistake | result |
|---|---|
| `$(f)` where f only emits values | `''` |
| `f \| cat` where f only emits values | cat reads nothing |
| `let x = (f)` where f only writes output | raises: `f emitted no value` |
| `$xs \|% {..}` with a list in `$xs` | runs once, with the whole list; spread it: `...$xs \|% {..}` |
| `ls \| lines \|% len` | each line's length, not the count: `\|> count` |
| `\|% { gofmt -l }`, `$it` forgotten | gofmt runs without the item: a lambda with no params ignores its args |
| `xs \|> f` where f does not read its value input, `xs \|> print hi` | the values are dropped; for each value: `\|% f` |
| `if $f x` with a fn value that emits a bool | tested by status, so always true; write `if ($f x)` |
| a helper called for its effect inside a fn | its values join the fn's values, as its output joins the output |
| a debug `print` in a fn used with `$( )` or `\|` | the debug text joins the output; write debug output to stderr |
| `a \| b \|> c` | b's output goes to the pipeline's output, not to c |
| `cd` / `export` in one stage of a pipeline | stages run at the same time, so other stages may or may not see it |

The value-side mistakes mostly raise when the value is used (`emitted no value`, `cannot use a list in a string`); the output-side ones (`''`, an empty stdin) do not.

## Brackets

| syntax | meaning |
|---|---|
| `{ ... }` | lambda with no params; it ignores any args it is given |
| `{\|x y\| ... }` | lambda with params |
| `{ ... $it ... }` | one-param lambda: a block that uses `$it` |
| `['a' 1 $x]` | list; items are values: strings must be quoted |
| `[k: v  k2: v2]`, `[:]` | map; keys are bare names, values are values |
| `( ... )` | the value of an expression or a pipeline, exactly one; its output goes on to stdout |
| `$( ... )` | the output of a pipeline as `str`, trailing newline trimmed; its values are dropped |
| `'...'`, `"..."`, `\\...`, `$\\...` | strings — see Quoting and strings |

### `[` : list vs index

Decided by whether `[` is glued to the previous atom.

| shape | meaning |
|---|---|
| `[` after space / start / `(` / `[` | list or map |
| `[` glued to `$name` (any name: `$xs`, `$HOME`, `$env:x`) or `( )`, or to another index | index: `$xs[0]`, `$m[k][0]`, `(f x)[0]`, `(cmd \| from_json)[name]` |
| `[` glued to any other atom | compile error: `*.[ch]`, `"x"[0]` |

- There are no glob character classes; `*.[ch]` is an error with a hint: write `*.c *.h`, or `?` for one char. They took a third meaning for `[`, and `(f)[0]` read as a glob. Richer patterns are deferred (see Not for now).
- `( )` is a value like `$name`, so it indexes the same way: `let out = (xx 3 4)[0]`.
- Inside quotes, `[` is literal except right after `$name`, where it indexes as outside: `"$p[title]"`. `${name}` ends the reference, so `"${x}[0]"` is `$x` followed by the text `[0]`.

## Quoting and strings

Bash quoting hurts because values are re-interpreted after expansion: word splitting, then globbing. entish never re-interprets a value. Quotes are only needed for special characters written literally in the source.

| bash | entish |
|---|---|
| `"$x"` everywhere (word splitting, glob) | `$x` is always one arg, never split or globbed |
| `"$(cmd)"` | `$(cmd)` is one `str` |
| `"${arr[@]}"` | `...$xs` |
| `'it'\''s'` | `'it''s'` |
| `"$(foo "$(bar "$x")")"` | `$( )` nests without quotes |
| `$'\n'` | `"\n"` |
| `IFS` | none |
| `<<'EOF'` / `<<EOF` | `\\` / `$\\` lines |

### String literals

| literal | lines | interpolation | escapes |
|---|---|---|---|
| `'...'` | one | none | `''` is one `'` |
| `"..."` | one; a `\` at a line's end continues it | variables | `\n \t \e \0 \xHH \u{...} \" \\ \$`, and `\` + line break, which adds nothing |
| `\\...` | many | none | none |
| `$\\...` | many | variables | none |

A single-line string that is not closed by the end of the line is an error at that line. In `"..."` a `\` right before the line break continues it, as in bash, C, Python and JS: the `\` and the line break are dropped and the next line is kept as written, indentation included. `'...'` has no escapes, so it cannot be continued.

### Interpolation: variables only, never code

Interpolation expands variable references and nothing else:

| form | example |
|---|---|
| `$name`, `$NAME`, `$env:name` | `"hello $name"`, `"$HOME/bin"` |
| index with a literal or a variable | `"$p[number] $p[title]"`, `"$xs[$i]"` |
| `${name}` to glue a suffix; it ends the reference | `"${name}_backup"`, `"${x}[0]"` |

- No `${ expr }`, no `$( )`, no `\( )` inside strings: they are compile errors, since they would otherwise stay text without a word (`"today $(date)"` printing `$(date)` is the bash habit this catches). A literal `$(` is `\$(` in `"..."`, or goes on a raw `\` line. Bind first, then interpolate:

  ```sh
  let today = $(date +%F)
  let n = (len $files)
  print "today $today, $n files"
  ```

- `$` not followed by a letter, digit, `_` or `{` is a literal `$`, as in `"$ ls"`.
  Digits refer to numbered REPL results; write a literal price as `'cost $5'`
  or `"cost \$5"`.
- In `$\\` lines a literal `$name` is written on a raw `\\` line instead; lines can be mixed, so multiline strings need no escapes at all.

Why: a string that can contain code can contain strings, so the lexer needs a stack of states (Kotlin and Scala need ~13 lexer states for strings alone; Ruby allows a heredoc inside an interpolation). With variables only, strings are a flat finite state machine: one-pass lexing, trivial syntax highlighting, and no nested quotes for the user. Zig goes further and has no interpolation at all (`std.fmt` with `{}` placeholders); entish keeps variable interpolation because placeholder braces collide with `{` in configs and JSON.

### When quotes are needed

A bare word needs quotes only if it contains characters that are entish syntax:

- whitespace, `| ; & < > ( ) [ ] { } $ ' "`
- glob characters `* ?` when no globbing is wanted
- `#` at the start of a word (comment); `feat#1` and URLs need no quotes

One exception: an arg `{}` on its own is the str `"{}"`, not an empty fn, so `fd -x rm {}` and `find -exec rm {} ';'` need no quotes. Only call args: `let f = {}`, `[{}]` and `{ }` are still fns, and a glued `{}.bak` still needs quotes.

```sh
jq '.[] | .name' data.json
awk '{print $1}' log
find . -name '*.go'
find . -name '*.tmp' -exec rm {} ';'          # or: find . -name '*.tmp' | lines |% rm $_
```

Strings passed to another shell (`ssh host '...'`, `bash -c '...'`) are re-interpreted by that shell; entish cannot protect them.

`quote` turns a str into an arg that reads back as the same str (YSH J8 strings, fish `string escape`):

| str | `quote` |
|---|---|
| `a/b.go` | `a/b.go` |
| `x y.go` | `'x y.go'` |
| `it's` | `'it''s'` |
| newline, control chars | `"a\nb"`, `"\e[0m"`, with `$` as `\$` |
| invalid UTF-8 | `"\xff"` |

- For Ctrl+T, `eval` and any text put into the input line: `bind ctrl-t { join ' ' (fd -0 | lines0 |> pick |% quote |> list) }`.
- TODO: `quote --sh` quotes for POSIX sh, for `ssh host` and `sh -c`.
- Considered and not taken: tagged strings (`sh"rm $f"`), since an identifier glued to a quote already concatenates and Java withdrew its string templates; and Kotlin/C# multi-dollar interpolation (`$$"... $$name"`), until gluing raw strings and variables (`'.[] | select(.name == "'$who'")'`) proves not enough.

### Multiline strings

Zig-style: every line of the string carries a prefix. No closing delimiter, no escapes, nothing in the content can end the string.

1. A line starting with `\\` is raw content: everything after `\\` up to the end of the line. Whitespace before `\\` is ignored.
2. A line starting with `$\\` is interpolated content: variable references are expanded (see Interpolation); there are no escapes, and `$( )` is a compile error as in `"..."`.
3. Consecutive `\\` / `$\\` lines form one `str`; raw and interpolated lines can be mixed. Lines are joined with `\n`; there is no trailing newline (`print` adds one).
4. The string ends at the first line that does not start with `\\` or `$\\`.
   - If that line starts with `|`, `|>`, `|%`, `&&`, `||`, it continues the same statement.
   - Otherwise it starts a new statement.
5. A string can start on the line after an incomplete `let x =`.
6. `#` inside a string line is content, not a comment.

```sh
# bash: cat > nginx.conf <<'EOF'
let conf =
    \\server {
    \\    listen 80;
    \\    location / { return 200 'ok'; }
    \\}
print $conf > nginx.conf

# bash: kubectl apply -f - <<EOF
let svc =
    $\\apiVersion: v1
    $\\kind: Service
    $\\metadata:
    $\\  name: $name
     \\  annotations: { cost: "$0" }
print $svc | kubectl apply -f -

# as an argument
let sql =
    \\SELECT name, 'x' AS tag
    \\FROM users WHERE note LIKE '%$%'
psql -c $sql

# another language, verbatim
let py =
    \\import sys, json
    \\print(json.dumps({"argv": sys.argv}))
print $py | python3 - a b

# indented with the surrounding code
fn write_unit name {
    let unit =
        $\\[Unit]
        $\\Description=$name
    print $unit > /etc/systemd/system/$name.service
}

# anything, no escaping
\\''' """ \\ $HOME `cmd` ' "
```

- The string is a value like any other: bind it with `let`, pass it as an argument, or `print` it into a pipe or file. A value is never written into `|` directly.
- Nothing can follow it on its last line, so it must be the last argument of a call or go through a variable.
- Written in an editor (e.g. vim block insert `Ctrl-v` `I\\`); not meant for pasting at the prompt.

## Contexts

**Word context** — args of fns and externals:

- bare word → `str`; number → `num`
- `$x`, `$x[i]`, `$( )`, `( value )` (exactly one), `...$xs` (a list's items), quoted strings, globs, lists, maps, blocks, lambdas
- adjacent atoms concatenate: `-$n`, `$dir/out.txt`, `GOOS=$os`
- bare words containing `(` must be quoted: `'foo(1).txt'`

**Value context** — inside `( )`, `|>` and `|%` stages, block statements:

- head bare word → call; it consumes words to the end of the call; its values
  - an external → **compile error**: `ls has no value: capture its output with $( ) or read it with | lines`
- head `$`, `(`, literal → expression with operators

**Where one value goes** — right of `let`, the value of `match`: a call or pipeline must be in `( )` (its values) or `$( )` (its output). Expressions, literals, `$x`, `if`, `try` and lambdas are written as they are.

- `let x = (f)`, `let x = $(f)`, `let n = (ls | lines |> count)`, `match (f) { ... }`
- bare → **compile error**: `let x = f` → `a call where a value goes needs ( ): (f) for its values, or $(f) for its output`; `let x = ls` → `ls has no value; for its output: $(ls)`

**Data literals** — list items and map values:

- strings must be quoted: `['go' 'bash']`, `[host: 'localhost']`
- bare word → **compile error**: `bare word in a list: quote it, 'https://a.com'`
- allowed unquoted: numbers, `$x`, `$( )`, `( value )`, globs, nested lists/maps, blocks, lambdas
- map keys are bare names `[a-zA-Z0-9_-]+` followed by `:` and whitespace; any other key is quoted: `['content type': 'x']`

So bare words are strings only in call args, where shell syntax needs them. Everywhere a value is written as data, a string looks like a string.

A call inside an expression needs parens: `(len $xs) + 1`. `len $xs + 1` passes three args to `len`.

Why `( )` right of `let` too (decided 2026-11-17): a fn has two outputs, and `let x = f` did not show which one it takes (the values; `$(f)` takes the output). With no args it also read like an alias for `f` (that is `$f`). `( )` was already required in every other place a value goes (operands, list items, args), so `let` and `match` were the exceptions. elvish requires `(f)` too, but there a bare `f` is the string `'f'`; here it is a compile error.

A call ends at end of line, `;`, `|`, `|>`, `|%`, `&&`, `||`, `)`, and — in `if` heads — `{`.

**Globs** are source text, not values, so expanding them is not re-interpretation:

- in the args of any call, fn or external: expand to separate args, as if the names were typed — `ls *.go` works whether `ls` is an external or a wrapper fn
- inside a list literal: expand to elements — `[*.go *.md]` is one flat list
- as a statement or left of `|>` or `|%`: its paths as values — `*.go |% {...}`
- where one value is needed (right of `let`, an operand, `( )`): a compile error, whatever matches, so the type never depends on the files: `let fs = [*.go]` is the list
- no match → error
- values are never expanded: a list in `$xs` still needs `...$xs`
- only `*`, `?`, `[...]`; no `**`
- `~` and `~/...` at the start of an unquoted word are `$HOME` (`~/*.go` globs). Not `'~'`, `a~b`, `--o=~/x`; `~user` is a compile error. An empty `$HOME` raises, so `~/x` never becomes `/x`. At the head of a command `~/bin/x` runs that file; as a value write `$HOME/x` (like `./x`, a bare path at the head is a command)

## Variables and environment

The first character of a `$name` decides what it is. Fixed lexical rule, no lookup.

| form | meaning |
|---|---|
| `$name` — starts with `[a-z_]` | language variable (`let`, params, `catch` bindings) |
| `$NAME` — starts with `[A-Z]` | env var of the current scope frame |
| `$env:name` — any name | env var; for lowercase env names (`$env:http_proxy`) |

- Language variables must start with `[a-z_]`. `let MAX = 3` → **compile error**: `MAX is an env var name; use export MAX=3`.
- Env vars are written only with `export NAME=value`, never with `let`. Every env var is passed to externals; there is no unexported env.
- For one command only, use the external `env`: `env GOOS=linux go build ./...`.
- Env values are always `str`.
- Built-in values `$true`, `$false`, `$nil`, `$args` are lowercase language values.
- Reserved names: `$it` (implicit lambda param) and `$_` (the item of a stage after `|%`). `let it = ..`, `let _ = ..` and params named `it` or `_` are compile errors.

```sh
print "$HOME/bin"
let mode = if $CI == 'true' { 'release' } else { 'debug' }
export GOFLAGS=-trimpath
print $env:http_proxy
```

## Values and memory

`str  num  bool  list  map  fn  nil`

Errors are plain maps (see Errors); there is no error type.

- All values are immutable. `assoc` returns a new map. Lists/maps use structural sharing internally.
- Closures capture by value.
- Variables are lexically scoped and resolved before running. A `$name` must be bound by an enclosing `let` or `fn` (earlier in the same or an outer block), a param, or a `catch`, or name a builtin; otherwise it is a compile error, even on a branch that never runs. A fn body sees only variables bound before the fn definition, never its caller's. Env vars (`$NAME`) are not lexical: they belong to the scope frame (see Scope: cwd and env).
- Consequence: reference cycles are impossible.
  - Go host: Go GC, nothing to write.
  - Other host: refcount is exact, no tracing GC.
- Processes, fds, pipes are not GC-managed: closed/waited when the command or block ends, including on error.
- `num` is an exact rational with arbitrary precision (Go `math/big.Rat`), like Raku. Integers are rationals with denominator 1. There are no floats.
  - literals `3`, `-3`, `0.1`, `1e-3` are exact (`0.1` is 1/10); `0.1 + 0.2 == 0.3` is `$true`
  - `/` is exact (`7 / 2` is 7/2), `%` is `a - b * floor(a / b)`, `**` needs an integer exponent
  - no overflow; division by zero raises; an index must be an integer
  - to `str` (printing, args to externals): integers as `3`; terminating values as exact decimals (`7/2` → `3.5`); a value with no exact decimal (`1/3`) shows as `1/3` at the REPL and raises when passed to an external — no silent rounding; round explicitly: `round 4 (1 / 3)` is `0.3333` (half away from zero)
- Number literals lex as `num` everywhere and keep source text for argv (`0755` stays `0755`).
- No truthiness. `if`, `&&`, `||`, `!` on values require `bool`.
- Indexing: `$xs[0]`, `$xs[1:3]`, `$m[key]`. `.` is never field access, so `$name.txt` concatenates.
  - an index or slice end is a literal, a `$var` or a value expression: `$xs[(len $xs) - 1]`, `$xs[$i + 1:]`; a call needs parens; out of range raises; no negative indices
  - inside a string only a literal or `$var` index: `"$xs[$i]"`; `"$xs[$i + 1]"` is an error, since interpolation is never code
  - slices are half-open, either end may be left out (`$xs[2:]`, `$xs[:2]`); only lists slice
  - a map key is a str: `$m[1]` is the key `'1'`, as in `[1: x]`, but a num in `$m[$n]` raises (`let k = (str $n)`)
  - a key twice in a map literal is a compile error
- Strings are never word-split.

## Expressions

```sh
let mode = if $CI == 'true' { 'release' } else { 'debug' }        # if is an expression
let sq   = (...[1 2 3] |% {|x| $x * $x } |> list)                 # [1 4 9]
fn add a b { $a + $b }                                            # the body's values are the fn's
let n    = (add 1 2)                                              # 3
```

- An `if` without `else` emits nothing when its condition is false. Where one value is needed that raises: `let x = if $c { 1 }` gives `this if emitted no value` when `$c` is false. In a stream it is a filter: `|% if $_ > 0 { $_ }`.
- Recursion is the only unbounded loop. Tail calls are guaranteed not to grow the stack.

## Conditions, `&&`, `||`, `!`

Same operators everywhere. The operand's syntax decides what is tested, never an inferred kind:

| operand | tested by | otherwise |
|---|---|---|
| a command: a call or pipeline (`grep -q x f`, `has_x`, `$f x`) | its status: success is true, failure is false (in condition position); its values are dropped | |
| anything else: `$x`, `( )`, a literal, operators | its value | raises unless it is one bool |

So `if test -f x` tests a status and `if (big 1)` a value, as in YSH. `if big 1` where `fn big` ends in a value is a compile error, since as a command it always succeeds; a fn value (`if $f x`) cannot be checked, see the user's responsibility table.

Between values, `&&` and `||` are bool operators: `let ok = $a && $b`. With a command on either side they are control flow: the left is tested, the right runs if needed and emits what it emits, so `$verbose && print x` adds no value to a fn's values.

Precedence, high to low: `!`, `&&`, `||`. Short-circuit.

### Condition position

An external in condition position does not raise on failure; failure is `false`. A command killed by SIGINT (Ctrl+C) is not a failure here: it still raises, so `cmd || fallback` does not run the fallback on Ctrl+C.

Condition position:
- the condition of `if`
- the left operand of `||`
- the operand of `!`
- operands of `&&` when the `&&` itself is in condition position

Everywhere else a failing external raises.

| statement | a succeeds | a fails |
|---|---|---|
| `a && b` | runs b | **raises** |
| `a \|\| b` | skips b | runs b; b failing raises |
| `a && b \|\| c` | b fails → runs c | runs c |
| `!a` | **raises** | ok |
| `if a && b { }` | — | false |

```sh
make && make install                     # stops loudly if make fails
grep -q TODO main.go || print 'clean'
git pull --ff-only || git pull --rebase
if test -f .env && !grep -q SECRET .env { print 'ok' }
if $n > 1 && (ends_with .go $f) && grep -q TODO $f { print $f }
```

A bash guard idiom fails loudly with a hint:

```
$ test -f .env && cat .env
error: 'test -f .env' failed (exit 1)
  hint: to run only if it succeeds, use: if test -f .env { ... }      # the hint is planned
```

## match

`let` destructuring (TODO). Allow list and map patterns on the left of `let`, including nested patterns and `...rest`. The right side must produce exactly one value. A mismatch raises before any names from the pattern are bound. Duplicate binding names are a compile error, as in `match`.

`match` checks the shape of a value and takes it apart in one step. It runs the first arm whose pattern matches.

```sh
fn g ...args {
  match $args {
    []             => git status -sb
    ['co' branch]  => git checkout $branch        # exactly 2 items; the second is $branch
    ['l' ...rest]  => git log --oneline ...$rest
    _              => git ...$args
  }
}

match (curl -s $url | from_json) {
  [status: 'ok' data: d]    => ...$d |% print $_[name]
  [status: 'error' msg: m]  => fail $m
}
```

| pattern | matches |
|---|---|
| `_` | anything |
| `name` | anything, binds `$name` |
| `'lit'`, `"lit"`, `42` | an equal str or num |
| `$v` | a value equal to `$v` (`$nil`, `$true` too) |
| `[a b]`, `[]` | a list of exactly that length |
| `[a ...rest]`, `[a ..._]` | a list of at least that length; the rest is bound to `$rest`, or dropped |
| `[k: pat]` | a map with key `k` whose value matches `pat`; other keys are ignored, so `[:]` is any map |

- `pattern if cond => body`: the arm runs only if the guard also holds. The guard is in condition position (a command is tested by its status, anything else must be a bool: `if ($f $x)`) and sees the pattern's names.
- A body is one expression or a `{ }` block, starting on the same line as `=>` (on the next line it could not be told apart from the next arm). It sees the pattern's names, which are not visible after the match. To give a fn, put it in a block: `=> { {|x| ... } }`.
- Arms are separated by newlines or `;`.
- No arm matches → raises `match: no arm matches <value>`. There is no silent fall-through.
- Values of different types never match: `'1'` does not match `1`, and comparing them does not raise.
- A match emits what its arm emits, as `if` does.
- Compile errors: an arm after an unguarded `_` or `name` (unreachable), a name bound twice in a pattern, a bare word (`foo.txt`; a name binds, a str is quoted), `true`/`false`/`nil` (write `$true`), interpolation or `$( )` in a pattern, a match with no arms.
- `match` is a keyword, not a fn, so after `|%` it gets no implicit arg: write `|% match $_ { ... }`, `match $x { ... }` or `match (cmd | from_json) { ... }`.
- No patterns for types (any str, any num); use a name and a guard.

## Pipes

- A pipeline is stages joined by `|`, `|>` and `|%`, left-associative. They bind tighter than `&&` / `||`.
- All stages run at the same time. A statement ends when all its stages have, so variables always hold complete values.
- Each `|` connects the output of the stage before it to the stdin of the stage after it. The values of the stage before it are dropped.
- Each `|>` connects the values of the stage before it to the value input of the stage after it, one value at a time. The output of the stage before it goes on to the pipeline's output.
  - right of `|>`: a fn, a fn value or a builtin that reads values (`each count list pick`, and `print`, `to_json` with no args). A fn body reads the value input of its call, as it reads its stdin: `fn tag { each { "x$it" } }` then `cmd | lines |> tag`. An external is a **compile error** (`jq reads stdin, not values; pass each value as an arg: |% jq $_, or print the values: $x |> print | jq`), and so is a builtin that takes its data as an arg (`len takes its data as an arg, not values: len $x, or for each value: |% len`) and a stage that is not a call (`the right side of |> must be a call; for each value: |% $_ * 2`).
  - `$_` is not bound after `|>`.
- Each `|%` runs the stage after it once per value of the stage before it, as the values arrive, with `$_` bound to the value (below). The stage gets no value input. The output of the stage before it goes on to the pipeline's output.
- left of `|>` and `|%`: anything that emits values. An external there is a **compile error**: `ls has no value; make its output values: ls | lines |> count`.
- A lambda, `$f` or `( )` right after `|>` or `|` is called with no args, like `$f`: `|> { print (count) }`, `cmd | { lines |> count }`. Right after `|%` it is called with each value as its arg: `|% { $it * 2 }`, `|% $f`.
- A stage's stdin not preceded by `|` is the pipeline's stdin. The first stage's value input is the enclosing fn's value input; a stage after `|` or `|%` has none.
- bytes → values: a converter reading stdin (`ls | lines`, `| from_json`), or `$( ... )` for one str.
- values → bytes: only `print` and `to_json`. With no args they write each value of their value input on its own line; with args they write those. An expression or a builtin that writes nothing on the left of `|` is a **compile error**: `a value cannot be piped into |; print it: $x |> print | cmd`. No implicit conversion, no conversion rules.
- values → one list: `|> list`; a list → values: `...$xs`.
- Pipefail is on; a stage before the last that lost its reader (SIGPIPE, or a value pipe closed by the next stage) is not a failure.
- A statement cannot start with `|`, `|>` or `|%`: `expected a command before |%; to continue the line before, end it with |%`.
- `|%` is one token: `|%xx` is `|%` then `xx`. An external named `%xx` needs `| %xx` or `^%xx`.

```sh
ls | lines |> count
git log --oneline | head -5 | lines
print hello | tr a-z A-Z
...$xs |> print | tr a-z A-Z             # one item per line
$xs |> to_json | jq .                    # one JSON value per line
*.go |% wc -l $_ | sort -n               # the output of all runs
```

### Streaming

`cmd | lines |% f |% g` handles each line as `cmd` prints it; endless producers (`yes`, `tail -f`) work. This holds through fns too: a fn's statements emit as they run, and a fn that reads its value input with `each` handles each value as it comes.

- Values flow through a small buffer, so a writer runs at most a few values ahead of its reader.
- A value nobody reads is dropped, but the code that emits it still runs: `f | cat` runs all of f.
- If a stage stops reading (it ended, or raised), the stage before it gets an error on its next write and stops, as `yes | head` does; one that isn't writing is killed 200 ms after the last stage ends.
- A failing producer raises when the statement ends; values before that were already handled.

### `|%` and `$_`: one run per value (from Hack and Perl)

A stage after `|%` runs once per value, with `$_` bound to it. It is `|> each { ... }` without the block: `|% rm $_` is `|> each { rm $it }`, `|% f a` is `|> each { f a $it }`. The operator decides that the stage runs per value; `$_` only says where the value goes.

```sh
...$xs |% round 2                        # round 2 x: the value is the last arg
ls | lines |% rm $_                      # an external needs $_
ls | lines |% mv $_ $_.bak               # anywhere in the args, any number of times
...$xs |% $_ * 2 |> list                 # an expression: map
...$xs |% if $_ > 0 { $_ } |> list       # filter
[1 2] |% len                             # one value: len [1 2]
```

- If the stage uses `$_`, the value goes there: `|% f $_ b`, `|% $_ * 2`, `|% $xs[$_]`, `|% ($_ * 2)`, `|% match $_ { ... }`.
- If it does not, and the stage is a call of a fn, a builtin, `$f`, `{ }` or `( )`, the value is its last arg: `|% f a` is `|% f a $_`, `$x |% round 2` is `round 2 $x`, `xs |% add 10` calls `fn add a b` as `add 10 x`, `|% call` is `call $_`, `'src' |% eval` is `eval 'src'`. The added arg counts for arity, checked at compile time for fns, builtins and `{ }`, at run time for `$f`: `xs |% len $ys` → `len takes 1 args, got 2; |% passes each value as the last arg`. Placement never depends on arity: without `$_` the value is always last.
- An external must use `$_`: `rm is an external, whose arg order is its own; write where each value goes with $_: |% rm $_`. Its signature is unknown and arg orders vary: `|% mv dst/` would run `mv dst/ FILE`. This is how externals are treated elsewhere: no arity check, no values, no value input.
- A stage that is not a call (an expression, a keyword form) must use `$_`: `a stage after |% that is not a call must use $_, the value: |% $_ * 2`.
- `$_` is the variable named `_` and interpolates like any variable: `$_.bak`, `'/tmp/'$_`, `"<$_>"`, `$_[name]`, `...$_`; `${_}x` is the item followed by `x` (`$_x` is the variable `_x`). `_` is reserved: `let _ = ..` and params named `_` are compile errors.
- It belongs to the innermost stage after `|%`: in `xs |% print $_ (...$ys |% $_ + 1 |> list)` each stage has its own. A stage whose only `$_` is in an inner stage is a compile error, since it would otherwise get its value as the last arg silently: `xs |% print (...$ys |% $_ + 1 |> list)` → `the $_ in this stage is an inner stage's; write this stage's own $_ where its value goes`.
- A `{ }` lambda or a fn body does not see the `$_` around it; that is a compile error, and a lambda's item is `$it`. `if`, `match` and `try` bodies are not lambdas and see it.
- Anywhere else it is a compile error, after `|>` too: `$_ is the item of a stage after |%: ... |% rm $_`.
- The stage gets no value input. A builtin that reads it (`each count list pick`), or a fn or `{ }` whose body reads it, is a **compile error**: `tag reads the value input, but a stage after |% runs once per value and gets none`. A builtin that takes fewer args fails on arity first: `count takes 0 args, got 1; |% passes each value as the last arg; to give it all the values: |> count`.
- Its output and values go on as `each`'s would; it streams, and it stops at the first value it fails on, whose error names the value (`in a $_ stage, value #2: ...`).
- `each` stays for a fn that processes its own value input: `fn tag { each { ... } }`, used as `xs |> tag`.
- After `|>`, a fn that takes the value as an arg: `xs |> add1` → `fn add1 takes 1 args, got 0; |> feeds the value input, not args; to pass each value as the last arg: |% add1`.
- `ls | lines |% len` gives each line's length, not the count (`|> count`). The type of the value is not known, so it is not checked.

Why: in F#, OCaml, Elm, Elixir and R, `x |> f` is a call with the whole of x. Here `|>` gives the whole stream to one call as its value input, so it still streams (`tail -f`), as PowerShell, nushell and elvish pipes do; `|%` is one call per value. Where the value is not the last arg, `$_` places it, the way Hack (`$$`), the JS pipeline proposal (`%`), R's native pipe (`x |> f(y, data = _)`) and Gleam's captures (`f(_, 1)`) do. The name is Perl's and PowerShell's for the current item (`ForEach-Object { $_ }`, alias `%`), and R's, Gleam's and Scala's `_` with a `$`, since a shell arg is a bare word (see the bare `_` below). In bash and zsh `$_` is the previous command's last arg (`mkdir d && cd $_`); outside a stage it is a compile error here, so that habit does not run silently.

Data last: entish fns and builtins take their data as the last arg (`round 2 $x`, `assoc k v $m`, `string:replace ',' '' $s`, `each $f` reads its data from the value input), so `|%` can append it. This is the rule for builtins and the stdlib, and recommended for user fns: with no types, a data-first fn whose params have the same type gets its args swapped silently. First (Elixir, Gleam, R) mainly helps type inference and autocomplete (ReScript switched to it for that); entish has neither.

```sh
'Fun to learn, delightful to ship' |%
    string:lowercase |%
    string:replace ',' '' |%
    string:split |> list                 # ['fun' 'to' 'learn' 'delightful' 'to' 'ship']
```

Tried on 2026-11-17, recorded so they are not proposed again:

1. `|>` passing each value as the last arg, and `|>>` for the stream: reverted. In F#, Elixir and Hack `x |> f` passes the whole thing, so `xs |> len` meaning "each length" was a trap; and it needed a second operator for the stream anyway.
2. `|>` per value when the stage uses `$_` (the presence of `$_` decides the mode; R `_`, magrittr `.`, TC39 "smart pipelines"): rejected. The mode was found by scanning the stage; nested pipelines made it ambiguous (`xs |> print (...[10 20] |> $_ + 1 |> list)` silently dropped xs), and a stage without `$_` that ignored its input dropped values silently. magrittr's nested `.` is a known trap; TC39 rejected the smart mix.
3. Decided: an operator decides the mode (`|%`); `$_` only says where the value goes. The last arg for calls whose signature the checker knows (F#; xargs appends too); externals must write `$_` (unknown signature and arg order).
   - Not by arity (a call one arg short gets the value): changing a fn's param count elsewhere would silently change a pipe.
   - Precedents for a per-element form of a pipe: Julia `.|>` (the dot is elementwise), Haskell `&`/`<&>`; PowerShell's `%` is an alias of `ForEach-Object`, used with `$_`.
   - `.|>` was not used: `find .|> f` reads as an arg `.`; an operator that starts with `|` always ends a word.
   - Also rejected: `|>>` (Clojure's `->>` and rash's `|>>` mean other things; it looks like `|>`), `|:`, `|*` (glob), `|&` (bash: stdout and stderr), `||>` (`||`).
   - `each` alone (elvish, nushell): no ambiguity, but every per-value step needs `each { ... $it }`.
   - The name `|%` is provisional. No external named `%...` is on PATH; in bash `%1` is a job spec.

Rejected for the item's name:

- `$it` as the item: it is also a lambda's param, so `each { ... |% f $it }` would be ambiguous.
- `$$` (Hack): the PID in sh, bash, Perl and Ruby, so `|% cp $$ /tmp/bak.$$` would silently be the item. Hack is its only precedent.
- `$@`: all args in sh; it reads as "all the items", so `|% tar czf all.tgz $@` looks like one archive of all files but writes one per item, each overwriting the last.
- `$in` (nushell): reads as an ordinary variable.
- A sigil without `$`: `%` (JS) is modulo inside `( )` and part of args (`date +%s`, `%03d.png`); `@` is in `user@host`, `HEAD@{1}`; `_` alone is `match`'s wildcard. Interpolated inside words they break such args, and recognized only as a word of their own `|% mv % %.bak` would rename every file to `%.bak`.
- The REPL's last value was `$_`; it was dropped for the item.

## Functional extras

### `$it`

A block that uses `$it` is a one-param lambda. `{|x| ... }` stays the general form.

```sh
*.go |> each { gofmt -l $it }
...[1 2 3] |% { $it * $it }
gh pr list --json number,title | from_json |% { print "$it[number] $it[title]" }
```

- `$it` belongs to the innermost block that uses it. Using `$it` in a block nested inside another `$it` block → warning: name the outer param with `{|x| ... }`.
- A block with explicit params cannot use `$it`.
- `$_` is the item of a stage after `|%`, not of a block; a block does not see it (see Pipes).
- A block with no params and no `$it` ignores its args, however many: `... |% { echo hi }`. A named `fn` always takes exactly its params.

### `peach`

**TODO: not implemented.**

`|> peach n f` is `|> each f` with up to `n` items running at once. It emits the same values, the same output and the same error as `each`; only the timing and the side effects of items after a failure differ.

```sh
...$urls |> peach 8 { curl -s $it | from_json }
git ls-files '*.go' | lines |> peach 4 {|f| gofmt -l $f }
```

- `n` is required, a whole number >= 1. There is no default: CPU count is wrong for IO-bound work (`curl`).
- Values come out in input order. A slow item holds back the ones after it; at most `n` items are running and finished-but-waiting at once, so memory is bounded.
- Output: each item's stdout goes to its own pipe and is written to the stage's output in input order. The item first in order is copied as it runs, the others are buffered until their turn. Interleaved stdout would be silent corruption for a reader after `|`. Children's stdout is not a tty (no colors, no progress bars); `peach` is for batch work. stderr passes through unbuffered; lines from different items may mix.
- Errors: when item k fails, no new item starts and items after k are killed as the stages of a finished pipeline are. Items before k finish and emit; then the error of the first failing item in input order is raised, naming it (`in peach, value #k: ...`). Items after k may have run partly.
- `cd` and `export` change the shell's state, so they race in parallel items: a compile error where the checker sees them in the body, a runtime error when reached through a fn value.
- Ctrl+C kills every running item.
- No parallel `|%` stage: `|> peach 8 { curl $it }`.

Rejected:

- `each -j n`: builtins have no flags; it would add flag parsing for one case.
- Values in completion order (elvish `peach`): the result changes from run to run.
- Passing stdout through (`xargs -P`): bytes of different items interleave, mid-line.
- Passing stdout through only when it is a tty: behavior that depends on where the output goes.

### Purity

TODO, maybe: infer which fns are pure (no externals, no effect builtins), for memoization or a parallel `peach`. Not designed.

### Values at the REPL

After an input runs, the REPL shows the values of each statement, in order, each marked `▶`. Values dropped on purpose are not shown: those of a stage before `|` (`f | cat`, `f | lines`), of `$( )`, of a condition. Writing `|` says the values are not wanted.

```sh
> len [1 2]
▶ 2
> fn f { [a: 1] }
> f | cat > out.txt         # out.txt is empty; f's value is dropped, not shown
```

- Values are shown by the REPL itself after the command ends (after the OSC 133 `D` mark), so they never reach a pipe, a redirect or `$( )`: there is no value→bytes conversion.
- Only what was typed: statements of a `source`d file are not shown.
- A script drops them.
- Other shells: elvish marks values with `▶` but writes them to stdout, and drops values sent into `|`; nushell renders the last value to stdout and converts values sent to externals by type; rash shows `Result N:`.

### Reusing results at the REPL

At the REPL, `$42` replays the values shown for input number 42 as a value
stream. Each evaluation starts at the beginning of the saved values. The prompt
shows the next input's number, starting at `[1]` in each session. Numbers are
never reused. Every nonblank accepted input consumes one number, including a
comment, a definition, a compile error or a failed/interrupted execution. Blank
input, discarded edits, scripts and key bindings do not consume numbers.

```sh
[1] > 1 + 2
▶ 3
[2] > $1
▶ 3
[3] > $1 |% $_ * 2
▶ 6
[4] > let saved = ($1 |> list)
```

- `$42 |> count` counts the saved values; `$42 |> list` collects them. No
  values is an empty stream; a saved list remains one value, never implicitly
  spread. `...$42` still requires exactly one list and spreads that list.
- In a single-value position (`let x = $42`, `($42)`, an argument, a list/map
  element, an operand or string interpolation), exactly one saved value is
  required. Zero or multiple values raise, as with other value expressions.
- Direct indexing or slicing (`$42[0]`, `$42[1:]`) is a compile error with a
  hint to collect first: `($42 |> list)[0]`. If the result itself is exactly
  one list/map, `($42)[0]` / `($42)[key]` indexes that value normally.
- Only the final values that the REPL shows are retained, in order, across all
  statements of that input. Intermediate pipeline values and stdout/stderr are
  not recorded, and looking up a result never reruns a command.
- The last 100 inputs are retained in memory for this session. This bounds the
  number of records, not their byte size. An expired, unknown, current or future
  result reference is a compile error. Failed inputs also have no reusable
  result, even when partial values were shown.
- A reference is resolved to an immutable replay buffer before execution,
  not a live channel or a first-class stream value. A variable collecting it
  (`let saved = ($42 |> list)`) or a closure holding the reference keeps its
  values alive after the history entry expires. Referencing an expired number
  in new input still fails.
- Numbered references are available only in code typed directly at the REPL,
  including functions defined there. A script, sourced file or `eval` source
  containing `$42` is a compile error; pass the value explicitly instead.
- `$_` remains the item of a `|%` stage. There is no implicit last-result variable.

TODO: complete the keys of a live value: Tab after `$m[` offers the keys of the map the variable holds. It only reads the value, so nothing runs.

To pipe the last input again, type `|` on an empty line: the editor inserts the last input before it (`echo 3 |`), so what reruns is shown before Enter.

## Scope: cwd and env

The shell keeps a stack of scope frames. Each frame has a cwd and an env.

- `in_dir $path $block` (planned, not implemented) pushes a frame with a new cwd, runs the block, pops it, and emits the block's values.
- `cd` and `export` modify the innermost frame. At top level that is the shell itself.
- A fn call does not push a frame, so `fn z q { cd ... }` changes the caller's cwd.
- Externals started in a frame get that frame's cwd and env.
- `$NAME` reads the env of the innermost frame.
- The stages of a pipeline run at the same time in one frame, so a `cd` in one stage may or may not be seen by another.
- There is no scoped env block; for one command use the external `env`: `env GOOS=linux go build ./...`.

```sh
export GOFLAGS=-trimpath
in_dir web { npm ci; npm run build }
let files = (in_dir /etc { ls | lines })
```

Implementation note (Go): never call `os.Chdir` / `os.Setenv`; set `exec.Cmd.Dir` and `exec.Cmd.Env` per command and resolve relative paths against the frame's cwd.

## Errors

An error is a plain map with fixed keys. A failing external raises, and so do `fail` and language errors (division by zero, an index out of range, a wrong type):

| key | value |
|---|---|
| `cmd` | the command line |
| `code` | exit code, or `$nil` if the process was killed by a signal |
| `signal` | e.g. `'SIGINT'`, or `$nil` if the process exited |
| `msg` | message |
| `pos` | source position |

For a failing command exactly one of `code` / `signal` is non-nil. For `fail` and language errors both are `$nil`; `fail` with a map raises that map, with any keys.

An error that is not caught is shown with more than the map holds: the source line with the failed stages of its pipeline marked (pipefail raises the rightmost failure; the others are marked too), and the fn calls it came out of, innermost first. This is display only; the map stays the same.

```
'grep -q x' failed (exit 1)
 --> -c:1:12
  |
1 | git log | grep -q x | wc -l
  | ^^^ exit 128
  |           ^^^^ exit 1
```

TODO: also show the values of the variables in the failed call's args, from the env it failed in (as pytest and Python tracebacks do):

```
2 |   ls $dir | wc -l
  |   ^^ exit 2
  |   $dir = '/nonexistent'
```

### try / catch

```
try <block or $fn> catch <name> <block>
```

- It emits what the body emits; if anything in the body raises, it then emits what the handler emits, with the error map bound to `$name`. Values the body emitted before the raise are not taken back: `try { 'a'; fail x } catch e { 'b' }` emits both.
- `catch` must follow the body's `}` on the same line, like `else`.
- No `finally`: processes, fds and scope frames are cleaned up on error anyway.
- Re-raise with `fail $e`.
- Ctrl+C is not caught: a command killed by SIGINT and the shell's own interrupt pass through every `try` to the prompt. Otherwise a `try` in `retry` or inside `each` would take one Ctrl+C per attempt or item.
- `fail $x` raises: a map is raised as is; any other value becomes `[cmd: $nil  code: $nil  signal: $nil  msg: $x  pos: ..]`.
- At the REPL, a child killed by Ctrl+C returns to the prompt without a stack trace.

```sh
try { go test ./... } catch e {
    if $e[signal] != $nil { print "killed ($e[signal])"; exit 1 }
    print "tests failed, exit $e[code]"
    exit 1
}

let cfg = try { cat config.json | from_json } catch e { [port: 8080] }
```

For commands, `||` is still the short form: `make || print 'build failed'`.

Checking an exit code, for commands whose 1 is not an error (`diff`, `grep`):

```sh
if grep -q TODO main.go { ... }                          # success or failure only
match (status { diff -q a b }) {                         # the code itself
    0 => print same
    1 => print differ
    c => fail "diff failed: $c"
}
```

`status f` calls f and emits its exit code: 0 if it succeeds, else the code of the command that failed. f's output goes on and its values are dropped. A failure with no code (a signal, `fail`, a language error) and Ctrl+C still raise.

## Grammar sketch

```
stmt      = kw_stmt | chain
kw_stmt   = 'let' NAME '=' value
          | 'fn' NAME params block
          | kw_expr
kw_expr   = 'if' chain block { 'else' 'if' chain block } [ 'else' block ]
          | 'try' ( block | '$' NAME ) 'catch' NAME block
          | 'match' chain '{' { arm ( NL | ';' ) } '}'
arm       = pattern [ 'if' chain ] '=>' ( block | chain )
pattern   = '_' | NAME | STR | NUM | '$' NAME { '[' idx ']' }
          | '[' { pattern } [ '...' NAME ] ']' | '[' { KEY ':' pattern } ']' | '[:]'
chain     = and { '||' and }
and       = not { '&&' not }
not       = '!' not | pipeline
pipeline  = stage { ( '|' | '|>' | '|%' ) stage }
stage     = call | expr | lambda                # lambda only after | |> |%: |% passes the value, | and |> no args
call      = head { word | redir }            # fn or external, resolved statically from head
head      = [ '^' ] NAME | PATHWORD
          | '$' NAME                          # call a fn value; needs >= 1 word, or a | / |> / |% stage
redir     = '>' word | '>>' word | '<' word | '2>' word | '2>&1'
expr      = literal | GLOB | '$' NAME { '[' idx ']' } | '$(' pipeline ')'
          | '(' value ')' | list | map | lambda | kw_expr | expr OP expr | OP expr
          | '...' ( '$' NAME | '(' value ')' | list )   # a list's items as values
value     = kw_expr | chain
word      = atom { atom }
atom      = BARE | NUM | STR | '$' NAME { '[' idx ']' } | '$(' pipeline ')'
          | '(' value ')' | list | map | block | lambda | '...' ( '$' NAME | '(' value ')' | list ) | GLOB
block     = '{' { stmt ( NL | ';' ) } '}'
lambda    = '{' '|' NAME* '|' { stmt ( NL | ';' ) } '}'
          | block                               # uses $it → one param; else it ignores its args
params    = NAME* [ '...' NAME ]
STR       = "'" { CHAR | "''" } "'"              # one line
          | '"' { CHAR | ESC | VARREF } '"'      # one line; VARREF = $name, $name[..], ${name}; $( and \( are errors
          | { ( '\\' TEXT | '$\\' { CHAR | VARREF } ) NL }+  # multiline, one prefix per line
```

Redirections apply to fn calls too (the fn's stdin/stdout).

### Line continuation

A statement continues on the next line only when:
- a bracket `( [ { $(` is still open, or
- the line ends with `|`, `|>`, `|%`, `&&`, `||`, or with a `\` word (bash-style, so pasted commands work; blanks after it are ignored), or
- the line ends inside a `"..."` string with `\` (the string continues; see String literals), or
- the statement is incomplete (`let x =`), or
- the line is a multiline string line (`\\` / `$\\`); the statement stays open until a line that is not part of the string, which continues it only if it starts with `|`, `|>`, `|%`, `&&`, `||`.

A `\` continues a line only as a word of its own at its end: `foo\` before a newline and a `\` followed by blanks and more on the line are compile errors. In a comment or a `\\` line it is text.

The REPL uses the same rule to decide whether Enter runs the line or opens a new one. It never has to look ahead: every decision is made from the line just entered.

Separators: newline or `;`. Comments: `#` at the start of a word, to the end of the line.

## One-liners

The language is typed at a prompt, so everything must also fit on one line.

- `;` separates statements everywhere: top level and inside blocks.
- Blocks, lambdas, `if` / `else`, `try` / `catch` all fit on one line.
- fns are called like commands: no parens, no commas.
- At the REPL, each value nobody received is shown, marked `▶`. A `str` shows raw; other values in entish literal syntax. See Values at the REPL.
- `...(value)` spreads any list into args, not only `...$name`.
- From another shell: `entish -c '...'`.

```sh
# loop over a glob
*.go |% gofmt -l $_

# count files (REPL prints 42)
git ls-files | lines |> count

# total lines of go files
git ls-files '*.go' | lines |% {|f| num $(wc -l < $f) } |> reduce 0 {|a x| $a + $x }

# chain with fallback
make && make install || print 'build failed'
git pull --ff-only || git pull --rebase

# if / else on one line
if test -f .env { print 'has env' } else { print 'no env' }

# scoped cwd, one-command env
in_dir web { npm ci; npm run build }
env GOOS=linux GOARCH=arm64 go build -o app ./cmd/app

# rename
*.jpeg |% {|f| mv $f $(basename $f .jpeg).jpg }

# files containing TODO
git ls-files '*.go' | lines |% {|f| if grep -q TODO $f { print $f } }

# JSON from an external
gh pr list --json number,title | from_json |% {|p| print "$p[number] $p[title]" }

# values -> bytes -> external
git log --format=%an | lines |> list |% count_by {|x| $x } |> to_json | jq 'to_entries | sort_by(-.value) | .[:5]'

# try / catch on one line
try { curl -fsS -o /dev/null https://example.com; print up } catch e { print "down: $e[code]" }

# arithmetic
...[1 2 3] |% {|x| print ($x * 10) }
entish -c 'print (2 ** 10)'
```

### xargs replacement

No dedicated syntax: spread for one call, `|%` for one call per item, `chunk` for long lists.

| task | bash | elvish | entish |
|---|---|---|---|
| all items as args to one call | `git ls-files '*.go' \| xargs gofmt -l` | `gofmt -l (git ls-files '*.go')` | `gofmt -l ...(git ls-files '*.go' \| lines \|> list)` |
| one call per item | `... \| xargs -n1 wc -l` | `git ls-files '*.go' \| each {\|f\| wc -l $f }` | `git ls-files '*.go' \| lines \|% wc -l $_` |
| batches (ARG_MAX) | `... \| xargs rm` (automatic) | — | `find . -name '*.log' \| lines \|> chunk 500 \|% rm ...$_` |
| kill by name | `pgrep -f srv \| xargs kill` | `kill (pgrep -f srv)` | `kill ...(pgrep -f srv \| lines \|> list)` |
| parallel | `xargs -P4` | `peach` | `... \|> peach 4 {\|f\| wc -l $f }` (TODO, see `peach`) |
| NUL-separated | `xargs -0` | `from-terminated "\x00"` | `find . -print0 \| lines0 \|% {\|f\| ... }` |

Like elvish, `cmd | lines |% f` streams: it starts on the first line while the producer still runs.

## rc fns and scripts on PATH

Helpers and wrappers used at the prompt are fns in the rc file:

```sh
# ~/.entrc
fn ls ...args { ^ls --color=auto ...$args }
fn z q { cd $(zoxide query $q) }
fn proj name { cd $HOME/src/github.com/$name }
```

```sh
$ z shell
$ proj aca/shell
$ ls -la
```

Commands that other programs must be able to run are scripts on PATH:

```sh
#!/usr/bin/env entish
# ~/bin/gco
if (len $args) != 1 { print 'usage: gco <branch>'; exit 2 }

let branch = $args[0]
if git show-ref --quiet "refs/heads/$branch" {
    git checkout $branch
} else {
    git checkout -b $branch
}
```

- `$args` is the script's argv as a list of `str`.
- On nix: package with `writeScriptBin`.
- A script wrapper must not reuse the name of the external it wraps (it would call itself): use `ll`, not `ls`.

## Examples

### hello

```sh
#!/usr/bin/env entish

let name  = 'world'
let langs = ['go' 'bash' 'nix']

let n = (len $langs)
print "hello $name, $n langs"

fn add a b { $a + $b }
print (add 1 2)                     # 3

...$langs |% {|l|
    print (if $l == 'nix' { "$l: declarative" } else { $l })
}

let today = $(date +%F)             # stdout -> str
let files = (ls | lines |> list)    # output -> values -> list
let count = (len $files)
print "$today: $count files"

ls -la | grep go                    # byte pipe
ls *.c *.h                          # globs; no match is an error
grep -q TODO main.go || print 'no todos'

if (ends_with .go $name) && grep -q TODO $name {
    gofmt -l $name
}
```

### immutable data

```sh
let cfg = [host: 'localhost'  port: 8080]
let cfg = (assoc port 8081 $cfg)            # new value, old one unchanged
print "listening on $cfg[host]:$cfg[port]"

let pkgs = ['git' 'curl' 'jq']
nix profile install ...$pkgs                # explicit spread to external
```

### value pipeline

```sh
let big = (git ls-files | lines |%
    {|f| if (ends_with .go $f) { [file: $f  lines: (num $(wc -l < $f))] } } |%
    {|r| if $r[lines] > 300 { $r } } |>
    list |% sort_by {|r| $r[lines] })

let total = (...$big |% {|r| $r[lines] } |> reduce 0 {|acc x| $acc + $x })

print "$total lines in big files"
```

### bytes ↔ values

```sh
git log --format=%an | sort | uniq -c       # bytes only, like any shell

git log --format=%an |                      # output -> stdin
    lines |>                                # stdin -> values, one per line
    list |%                                 # values -> one list
    count_by {|x| $x } |>                   # list -> map
    to_json |                               # value -> bytes
    jq .                                    # stdin of jq
```

### try / catch

```sh
let cfg = try { cat config.json | from_json } catch e {
    print "bad config: $e[msg]"
    exit 1
}
print "port $cfg[port]"
```

### lambdas

```sh
let double = {|x| $x * 2 }
$double 21                                  # REPL prints 42
let hello = { print hi }
call $hello                                 # no args: call; a bare $hello is the fn value
print ($double 21)
...[1 2 3] |% $double                       # 2 4 6

fn inc x { $x + 1 }                         # a named fn is a value too
fn twice g x { $g ($g $x) }
twice $inc 5                                # 7
fn adder n { {|x| $x + $n } }               # returns a closure
...[1 2] |% (adder 3)                       # 4 5
...['a b' 'c'] |% $lines                    # builtins are values: 'a b' 'c'
```

### recursion instead of while

```sh
fn retry n f {
    try $f catch e {
        if $n > 1 { sleep 1; retry ($n - 1) $f } else { fail $e }   # tail call
    }
}

let body = (retry 3 { $(curl -fsS https://example.com) })
```

### build script

```sh
#!/usr/bin/env entish
export CGO_ENABLED=0
let version = $(git describe --tags --always)
let targets = ['linux/amd64' 'darwin/arm64']

fn build target {
    let p = (split_by / $target)
    let os = $p[0]
    let arch = $p[1]
    env GOOS=$os GOARCH=$arch go build -ldflags "-X main.v=$version" -o "dist/app-$os-$arch" ./cmd/app
    print "built $target"
}

go vet ./... && go test ./...                # stops loudly on failure

...$targets |% build

in_dir dist { sha256sum app-* > SHA256SUMS }

let rel = (gh release view --json tagName,url | from_json)
print "latest release: $rel[tagName] $rel[url]"
```

## Errors the compiler catches

```sh
let w = ls                  # ls has no value; for its output: $(ls)
$xs |> jq .                 # jq reads stdin, not values; pass each value as an arg: |% jq $_, or print the values: ...
$xs | jq .                  # a value cannot be piped into |; print it: $x |> print | jq
ls | len                    # len does not read stdin; it takes its data as an arg: len $x
$xs |% jq .                 # jq is an external, whose arg order is its own; write where each value goes with $_: |% jq $_
$xs |> len                  # len takes its data as an arg, not values: len $x, or for each value: |% len
$xs |% tag                  # tag reads the value input, but a stage after |% runs once per value and gets none
x$(len $xs)                 # len writes no output, and $( ) captures output; for a value use ( )
if big 1 { ... }            # big ends in a value, so as a command it always succeeds; to test its value: (big ...)
print "today $(date)"       # a string interpolates variables, never code; bind it first: let s = $( ... ), ...
let fs = *.go               # a glob gives several paths, not one value; to collect them into a list: [*.go]
nix profile install $pkgs   # list passed to an external, use ...$xs  (at run time)
let n = len $xs + 1         # a call where a value goes needs ( ): (len ...); here (len $xs) + 1
fn ls ...a { ls ...$a }     # planned warning: ls calls itself, did you mean ^ls?
fn split x { ... }          # planned warning: shadows /usr/bin/split
```

Runtime: a non-bool value in a condition, a call that emits no value or several where one is needed (`f emitted 2 values; to collect them into a list: ... |> list`), external not found.

## Xonsh pitfalls avoided

| xonsh | entish |
|---|---|
| `ls -l` parsed as Python `ls - l` when vars exist | head is always a callable; vars need `$`; operators only in value context |
| `ls = 1` breaks the `ls` command | values are a separate namespace |
| unclear whether a name is Python or subprocess | static resolution: fn if defined, else external; `^` forces external; `explain` shows it |
| capture forms `$()` `!()` `$[]` `![]` | one: `$( )` |
| `@(expr)` to pass values to commands | `$x`, `(expr)` work directly in words |
| subprocess errors ignored by default | nonzero exit raises, pipefail on |
| Python keywords collide with commands | keywords only at head; `^name` escapes |

## Lessons from other shells

Status: **applied** = already in this design; **validated** = confirms an existing choice; **proposed** = not yet in the design, see Open decisions.

### rash (Racket)

Sources: `racket-rash/rash/scribblings/rash.scrbl`, `shell-pipeline/scribblings/pipeline-macro.scrbl`, `TODO`.

| rash | lesson | entish | status |
|---|---|---|---|
| A line starting with `(` is Racket, anything else is a pipeline. `(if clang? 'clang 'gcc) -o prog prog.c` silently becomes a Racket expression. The author's TODO considers making it a syntax error. | Choosing the mode from the head token leaves no way to run a command whose name is computed. | `^$cc -o prog prog.c`. | applied |
| `$lower` is a lexical variable, `$UPPER` is an env variable. | Short to type; a fixed first-character rule, not guessing. Lowercase env names (`http_proxy`) need an escape. | Same rule; `$env:name` for any env name; uppercase `let` is a compile error. | applied |
| Two value pipes: `\|>` gets a port, `\|>>` converts the port to a string. | Implicit bytes→value conversion needs extra operators when it has more than one meaning. | No implicit conversion: `\|>` takes only values; bytes become a value through `$( )` or a converter after `\|` (`\| lines`). | applied |
| `\|> + 6` appends the piped value; `\|> + _ 6` places it with `_`. | An optional placeholder handles fns whose data argument is not last. | `\|%` appends the value to a call without `$_`; `$_` places it. Externals always need `$_`. | applied |
| Nonzero exit raises. Per-pipeline `#:success` sets which codes count as success; `&strict` / `&permissive` control checking of middle pipeline members. | `diff` (1 = differences) and `grep` (1 = no match) need non-zero success codes. | Pipefail on; `\|\|` and `try` cover most cases. | proposed |
| The command name is checked to be an executable **before** the other arguments are evaluated. | A missing command should fail before `$( )` arguments run their side effects. | Not specified yet. | proposed |
| User-defined line macros, pipeline operators and aliases. Docs: "Rash's names are terrible"; many operators are marked unstable. | User-extensible syntax multiplies the surface area and keeps names unstable. | Fixed syntax, 6 keywords, no macros, no aliases. | validated |
| No job control. TODO: vim "seems to exit immediately", then appears after Ctrl+D. | Even without job control, the shell must give the terminal to the foreground child correctly, or TUI programs (vim, less, fzf) break. | Not specified yet. Needs a Terminal section. | proposed |
| TODO: programs run POSIX code through `$SHELL`. | A non-POSIX shell must not be put in `$SHELL`. | Not specified yet. | proposed |
| TODO: `<()` is still missing. | Process substitution is missed in daily use (`diff <(a) <(b)`). | Not in the design. | proposed |
| Interactive code copied into a file is a working script. | REPL and script grammar must be the same. | Same grammar; line continuation decided at the end of a line. | applied |
| `#{cmd}` captures stdout and trims it. | One capture form, trimmed. | `$( )`, trailing newline trimmed. | validated |

### schemesh (Chez Scheme)

Sources: `schemesh/README.md`, `doc/faq/faq.md`, `doc/shell/syntax.md`, `doc/shell/mixed_syntax.md`, `doc/shell/env.md`.

| schemesh | lesson | entish | status |
|---|---|---|---|
| A line continues only when brackets are unbalanced or it ends with an operator. | The REPL decides at Enter; it can't look at the next line. | Line continuation: open bracket or trailing `\|`, `\|>`, `\|%`, `&&`, `\|\|`, `\\`. | applied |
| `(values '{...})` shows how a shell line was parsed. | Parse results should be inspectable. | `explain` builtin. | applied |
| cwd and env are per job; `{ }` scopes `cd`; `global cd` changes the shell. | Scoped cwd/env fits immutable design. | `in_dir`, scope frames. | applied |
| `[` is a subshell in shell syntax, `(` in Scheme syntax, and a glob class; the docs give `''[a-z]*` as a workaround. | One bracket must not carry several meanings. | `[` glued = index, after space = list/map; no glob classes. | applied |
| Status values: `(ok v)`, `(failed 2)`, `(killed sigint)`, `(stopped sigtstp)`. | Exit code and signal are different outcomes. | An error map has exactly one of `code` / `signal`. | applied |
| POSIX syntax kept for commands: `&&`, `\|\|`, redirections. | Daily command syntax should stay familiar. | `&&`, `\|\|`, `!` with condition-position rule. | applied |
| Two syntaxes switched by `( )` / `{ }` and `#!shell` / `#!scheme`. Documented limits: command args from Scheme must be string / list / closure; REPL accepts what files don't; file syntax guessed from its name. | Mode switching creates many boundary rules. | One grammar; one call syntax for fns and externals. | validated |
| Job control works because Scheme jobs run in a forked subprocess. | Job control needs fork; Go has no fork without exec. | No job control. | applied |
| Structured pipelines over `\|` as NDJSON, table on a TTY, streaming. | Structured data can cross process boundaries and stream. | `\|>` and `\|%` are in-memory; they stream. | proposed |
| FAQ: a new ad-hoc language is "a large endeavour, and a significant obstacle to adoption". | A new language has a learning and maintenance cost. | Keep the core small (6 keywords). | validated |

## Not planned

- job control (`&`, `fg`, `bg`, Ctrl+Z)
- aliases — use fns
- subshells — use `in_dir`
- flag parsing for fns
- POSIX `VAR=x cmd` — use the external `env VAR=x cmd`
- scoped env blocks (`with_env`) — use `export`, or `env` for one command
- auto-currying — `|%` with data-last fns covers most uses, and a wrong arg count would silently yield a fn

Not for now (may be added later):

- modules (`use`) — fns live in the rc file or the script itself
- `**` recursive glob
- richer glob patterns, replacing the removed `[ch]` classes: a `glob 'pattern'` builtin taking the pattern as a str (`...(glob '*.[ch]')`), or elvish-style modifiers glued to a wildcard (`?[set:ch]`, `*[type:dir]`, `*[nomatch-ok]`); `nomatch-ok` would also let `*.c *.h` pass when one has no match
- floats
- regex
- builtins beyond the current list — added as needed

## Open decisions

1. Number literals as `num` with source text (current), or everything-is-str.
2. Escape hatch for mutable state (`ref` box), or none (current).
3. Host language: Go.
4. ~~`|>` eager or streaming~~ — decided: every call emits a value stream (STREAMS.md). All stages run at the same time; values flow one at a time, through fns too; a statement ends when all its stages have, so variables never hold streams.

From the lessons above:

5. ~~Optional `_` placeholder in `|>` stages~~ — decided: in a `|%` stage `$_` places the value; without it a call gets it as its last arg (see Pipes, `|%` and `$_`).
6. ~~Per-command success codes~~ — decided: `status f` emits the exit code as a value, and `match` decides what is a success.
7. Resolve the external (PATH lookup) before evaluating its arguments.
8. Terminal handling without job control: children stay in the shell's process group, the shell ignores SIGINT/SIGQUIT while a foreground child runs, TUI programs keep the tty.
9. Never set `$SHELL` to entish; tools run POSIX code through it.
10. ~~Process substitution~~ — decided: the converter `as_file`, a temp file, instead of new syntax. `<(cmd)` would make `( )` mean output after `<` only and changes the meaning of `<($path)`; `<$(cmd)` is already a redirect from a computed path; `$<(cmd)` needed its paths kept to call args. A streaming form (pipes, e.g. a builtin that passes `/dev/fd/N` paths to a lambda) can be added if needed.

From one-liners:

11. ~~xargs gaps (NUL-separated input is done: `| lines0`): parallel `peach n f xs`~~ — decided: `|> peach n f`, ordered, reads the value input like `each` (see Functional extras, `peach`). Not implemented.

From removing fn kinds (see Output and values):

12. ~~"No value" is `$nil` or a separate void~~ — decided by the value stream: no value is an empty stream, and `let x = (ll)` raises (`ll emitted no value`).
13. ~~`eval` source restricted to `'literal'` / `$var`~~ — decided: dropped. Binding the source to a variable first passed the same injection (`let s = "rm $f"; eval $s`), so the rule only added friction; values still go in safely as `$args`. A dynamic command name is `^$name`, not `eval`.
14. Lint for the "user's responsibility" table: none (current), or warnings from best-effort inference that never change behavior (e.g. in `explain`).
15. Stdin fallback: only converters (current), or every builtin that takes data. Converters read stdin wherever their data arg is left out, not only right after `|`; a forgotten arg (`let xs = (lines)`) waits for input instead of being a compile error.
16. Optional output types on fns (as Nushell's `def f []: string -> int`), so the checker can catch `$(f)`, `f | cmd` and `if f` mistakes for fns that declare them. Fns stay kindless by default.
17. Undecided: what `try` catches. Now: everything except Ctrl+C, including bugs in the body: a type error (`$(stat -c %s $f) + 1` without `num`), an index out of range, `match` with no arm, `$nil` passed to an external. `let size = try { $(stat -c %s $f) + 1 } catch e { 0 }` is then always `0`, silently. Options:
    - catch only command failures and `fail`; language errors pass through as bugs. The line is unclear: `from_json` on bad input or an index into outside data may be data errors the caller wants to catch.
    - add a `kind` key to the error map (`'cmd'`, `'fail'`, `'type'`, ...) and keep catching everything; the handler decides.
    - keep catching everything (current).

## Deferred designs

Kept for later; not part of the current language.

### Pipeline sections

A parenthesized pipeline that starts with `|>` would be a fn value: `(|> f x |> g)`.

Not needed with value streams: a lambda reads its value input, so it already is a pipeline section.

```sh
let n = { each { num $it } |> count }
git ls-files | lines |> $n
```
