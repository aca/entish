# entish — minimal functional shell language design

Status: design only, not implemented. `entish` is a placeholder name.

## Principles

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
5. **Two pipes, one channel each.** Every call writes output (stdout) and returns a value.
   - `|` carries output to the next stdin
   - `|>` carries the value to the next fn's last arg
   - `$( )` captures output as a `str`; `( )` yields the value
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
- No `for`: iterate with `each` (effects) and `map` / `filter` / `reduce` (values).
- No `set`, `return`, `break`, `continue`.
  - no mutation: rebind with `let` (shadowing)
  - value of a block = its last expression
  - a value statement that is not the last of its block is a compile error (`unused value`): its value would be dropped silently. In a script (a file, `-c`, stdin) the last top-level statement is too, since nothing shows it: `entish -c '$(pwd)'` is an error, not a silent no-op. At the REPL the last value is printed.
  - early exit: `if`; loops: collection fns, recursion

## Builtin fns

All builtins are fns. A user `fn` of the same name shadows a builtin (planned: a warning).

| kind | implemented | planned |
|---|---|---|
| effect | `print cd export exit` | |
| meta | | `explain` |
| control | `fail call eval source` | |
| meta | `src` | |
| scope | | `in_dir` |
| editor | `abbr bind` | |
| convert | `num str round lines lines0 from_json to_json as_file` | `split_by join_with` |
| collection (data-last) | `len map filter each keys assoc` | `reduce chunk sort_by count_by get ends_with` |

- Examples in this document use planned builtins too.
- Converters read stdin: `lines`, `lines0` (NUL-separated) and `from_json` with their data arg left out read their stdin, like `cat` with no file. After `|` that is the previous stage's output: `ls | lines`, `find . -print0 | lines0`, `curl -s $u | from_json`. In a fn it is the fn's stdin, so `fn eachl f { lines |> each $f }` gives `ls | eachl {..}`. Only converters do this: `ls | len` is a compile error (it would count bytes). `lines` alone at the prompt waits for terminal input (end it with Ctrl-D). Removed builtins: `eachl` / `each0`, now `ls | lines |> each {..}`.
- `to_json` prints a value as compact JSON plus a newline, like `print`, and returns `$nil`: `$xs |> to_json | jq .`. Its inverse is `from_json` (bytes or a str → value); `$(to_json $x)` gives the JSON as a str. Map keys keep their order. A num with no exact decimal (`1/3`) and a fn raise.
- `from_json` reads one JSON value: numbers are exact nums (`0.1` is 1/10), objects keep key order, `null` is `$nil`; a duplicate key or trailing data raises.
- `as_file` copies its stdin byte for byte to a new temp file and returns the path, for commands that take only files (process substitution without new syntax): `diff (sort a | as_file) b`, `comm -12 (sort x | as_file) (sort y | as_file)`, `docker run --env-file (pass show env | as_file) img`.
  - It reads only stdin: `$s |> as_file` and `as_file $s` are compile errors, since only `print` and `to_json` turn values into bytes (`print $s | as_file`).
  - The file is a real file, so commands that seek or read twice work (zsh `=( )`, not bash `<( )`). The producer runs to the end first, so it does not stream.
  - A failing producer raises at `as_file`. The file is in `$XDG_RUNTIME_DIR` (else the system temp dir), mode 0600, and is removed when the input ends: a REPL input, or the whole script, so `let p = (cmd | as_file)` can be used on later lines.
  - bash's `<(cmd)` is a redirect of `( )` here and a compile error with a hint to `as_file`.

- Collection fns take the data as the **last** argument so they compose with `|>`.
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
- The arg count of every call to a named fn or builtin is checked before running, on every branch; the value from a preceding `|>`, and stdin for a converter after `|`, counts as the last arg. Only args whose count is unknown until run time (a glob, `...$xs`) defer the check. Externals and fn values (`$f`) are not checked; a lambda written after `|>` or `|` is, since its params are in the source.
- A bare `$f` is always the value itself, never a call; that keeps `if $c { $a } else { $b }` returning `$a` whatever it holds. `$f x`, `x |> $f`, `cmd | $f` (no args; its stdin is the pipe) and `try $f` call it. A lambda `{ }` or a group `( )` stands where `$f` does, with the same meaning: alone it is a value; `{|x| ... } 1`, `(twice $inc) 1`, `x |> { ... }`, `x |> (add 2)` and `cmd | { ... }` call it. `call` calls one of them with no args: `call $f`, `call $m[k]`, `call { ... }` (like a JS IIFE; its `let`s stay inside), `call (mk)`. A `( )` whose value is not a fn raises at run time, like `$f`. Before `call`, `{ $f; ... }` dropped the fn silently; now that is an `unused value` error with a hint.
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
$f |> eval 'rm ...$args'               # a value from |> is the last arg
let double = eval '{ |x| $x * 2 }'     # the last statement is the value, here a fn
```

- The source is parsed and checked when the call runs, as the body of `fn _ ...args { src }` at the top level. Its errors raise at the call (`eval: <eval>:1:5: ...`) and can be caught with `try`.
- It sees top-level fns, builtins, env vars and `$args`; never the caller's variables. Its own `fn` and `let` do not leak out.
- Its result is that of a fn body: its output, and the value of its last statement.
- The source is any str: `eval $(cmd)`, `eval "rm $f"`. Interpolating a value into the source splices it into code, so `$f` holding `x; rm -rf ~` runs both; pass values as args instead: `eval 'rm ...$args' $f`. Only the source's position is checked: `'src' |> eval` is a compile error, the piped value would be the last arg.
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
    let s = src $body
    fail "check failed: $s"
  }
}
check { $n > 0 }                    # check failed: { $n > 0 }
```

`src` only reads code. Text it returns can be run with `eval $s`, which gives back a lambda for a lambda's source; it is never spliced.

### explain

`explain` shows how a line resolves, without running it.

```
$ explain 'xx; ^xx; gofmt -l $xs; make || print failed'
fn        xx     → ~/.entrc:3   (pure, shadows /usr/bin/xx)
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
| **value** | a fn: the value of its last statement. An external, `print`, `to_json`, `cd`, `export`, `each`: `$nil` |
| **status** | success, or failure: raises, except in condition position |

Everything a fn's body writes is the fn's output, as in bash. Output and value are independent, so a fn can do both:

```sh
fn build target {
    go build -o "dist/$target" ./cmd/app      # output: goes to the caller's stdout
    "dist/$target"                            # value
}
let bin = build linux                         # build output on the terminal, $bin is the path
build linux | tee build.log                   # output to tee, the path dropped
```

Each operator connects exactly one of them:

| syntax | connects | the rest |
|---|---|---|
| `a \| b` | a's output → b's stdin | a's value is dropped |
| `a \|> f` | a's value → f's last arg | a's output goes to the enclosing stdout |
| `$( a )` | a's output → one `str`, trailing newline trimmed | a's value is dropped |
| `( a )` | a's value | a's output goes to the enclosing stdout |

There are no fn kinds. A wrapper is just a fn, and behaves like the external it wraps in every position, because the external has no value either:

```sh
fn ls ...args { ^ls --color=auto ...$args }
ls | lines             # output → list
cat app.log | grep ERR # a fn on the right of |: its externals read the pipe
let s = $(ls)          # output as str
let x = ls             # $nil: the user's mistake, not checked (^ls itself would be a compile error)
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
| `let x = ls`, `(ls)`, `ls \|> lines` — an external or `print`/`cd`/`each` used as a value | `ls has no value: capture its output with $( ) or read it with \| lines` |
| `$xs \|> jq .` — an external right of `\|>` | `jq reads stdin, not a value: print $x \| jq` |
| `$xs \| jq .`, `len $x \| cat` — an expression or a value-only builtin left of `\|` | `a value cannot be piped into \|; print it` |
| `ls \| len` — a builtin other than a converter after `\|` | `len takes a value, not stdin: ls \| lines \|> len` |
| `1 + 1`, `$f` as a statement that is not the value of its block, or the last statement of a script | `unused value` |

Calls to user fns are never classified, so they are never errors here. Dropping a value or output is not an error either:

| mistake | result |
|---|---|
| `$(f)` where f returns a value and prints nothing | `''` |
| `(f)` / `let x = f` where f only writes output | `$nil` |
| `add 1 2 \| cat` | cat reads nothing |
| `$xs \|> each { $it + 1 }` | nothing (use `map`) |
| a fn that sometimes returns `$nil`, used in a condition | `$nil` counts as success |
| a debug `print` in a fn used with `$( )` or `\|` | the debug text joins the output; write debug output to stderr |
| `a \| b \|> c` | b's output goes to stdout, not to c |

A `$nil` that reaches an external arg, a string or arithmetic still raises, so most of these surface soon. A `$nil` compared with `==` does not.

## Brackets

| syntax | meaning |
|---|---|
| `{ ... }` | block = zero-arg lambda |
| `{\|x y\| ... }` | lambda with params |
| `{ ... $it ... }` | one-param lambda: a block that uses `$it` |
| `['a' 1 $x]` | list; items are values: strings must be quoted |
| `[k: v  k2: v2]`, `[:]` | map; keys are bare names, values are values |
| `( ... )` | the value of an expression or a pipeline; its output goes on to stdout |
| `$( ... )` | the output of a pipeline as `str`, trailing newline trimmed; its value is dropped |
| `'...'`, `"..."`, `\\...`, `$\\...` | strings — see Quoting and strings |

### `[` : list vs index

Decided by whether `[` is glued to the previous atom.

| shape | meaning |
|---|---|
| `[` after space / start / `(` / `[` | list or map |
| `[` glued to `$name` or `( )`, or to another index | index: `$xs[0]`, `$m[k][0]`, `(f x)[0]`, `(cmd \| from_json)[name]` |
| `[` glued to any other atom | compile error: `*.[ch]`, `"x"[0]` |

- There are no glob character classes; `*.[ch]` is an error with a hint: write `*.c *.h`, or `?` for one char. They took a third meaning for `[`, and `(f)[0]` read as a glob. Richer patterns are deferred (see Not for now).
- `( )` is a value like `$name`, so it indexes the same way: `let out = (xx 3 4)[0]`.
- Inside quotes, `[` is literal.

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
| `"..."` | one | variables | `\n \t \e \0 \xHH \u{...} \" \\ \$` |
| `\\...` | many | none | none |
| `$\\...` | many | variables | none |

A single-line string that is not closed by the end of the line is an error at that line.

### Interpolation: variables only, never code

Interpolation expands variable references and nothing else:

| form | example |
|---|---|
| `$name`, `$NAME`, `$env:name` | `"hello $name"`, `"$HOME/bin"` |
| index with a literal or a variable | `"$p[number] $p[title]"`, `"$xs[$i]"` |
| `${name}` to glue a suffix | `"${name}_backup"` |

- No `${ expr }`, no `$( )` inside strings. Bind first, then interpolate:

  ```sh
  let today = $(date +%F)
  let n = len $files
  print "today $today, $n files"
  ```

- `$` not followed by a letter, `_` or `{` is a literal `$`: `"cost $5"`, `"$ ls"`.
- In `$\\` lines a literal `$name` is written on a raw `\\` line instead; lines can be mixed, so multiline strings need no escapes at all.

Why: a string that can contain code can contain strings, so the lexer needs a stack of states (Kotlin and Scala need ~13 lexer states for strings alone; Ruby allows a heredoc inside an interpolation). With variables only, strings are a flat finite state machine: one-pass lexing, trivial syntax highlighting, and no nested quotes for the user. Zig goes further and has no interpolation at all (`std.fmt` with `{}` placeholders); entish keeps variable interpolation because placeholder braces collide with `{` in configs and JSON.

### When quotes are needed

A bare word needs quotes only if it contains characters that are entish syntax:

- whitespace, `| ; & < > ( ) [ ] { } $ ' "`
- glob characters `* ?` when no globbing is wanted
- `#` at the start of a word (comment); `feat#1` and URLs need no quotes

```sh
jq '.[] | .name' data.json
awk '{print $1}' log
find . -name '*.go'
find . -name '*.tmp' -exec rm '{}' ';'        # or: find . -name '*.tmp' | lines |> each {|f| rm $f }
```

Strings passed to another shell (`ssh host '...'`, `bash -c '...'`) are re-interpreted by that shell; entish cannot protect them.

### Multiline strings

Zig-style: every line of the string carries a prefix. No closing delimiter, no escapes, nothing in the content can end the string.

1. A line starting with `\\` is raw content: everything after `\\` up to the end of the line. Whitespace before `\\` is ignored.
2. A line starting with `$\\` is interpolated content: variable references are expanded (see Interpolation); there are no escapes.
3. Consecutive `\\` / `$\\` lines form one `str`; raw and interpolated lines can be mixed. Lines are joined with `\n`; there is no trailing newline (`print` adds one).
4. The string ends at the first line that does not start with `\\` or `$\\`.
   - If that line starts with `|`, `|>`, `&&`, `||`, it continues the same statement.
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
- `$x`, `$x[i]`, `$( )`, `( value )`, quoted strings, globs, lists, maps, blocks, lambdas
- adjacent atoms concatenate: `-$n`, `$dir/out.txt`, `GOOS=$os`
- bare words containing `(` must be quoted: `'foo(1).txt'`

**Value context** — right of `let`, inside `( )`, `|>` stages, block statements:

- head bare word → call; it consumes words to the end of the call; its value
  - an external → **compile error**: `ls has no value: capture its output with $( ) or read it with | lines`
- head `$`, `(`, literal → expression with operators

**Data literals** — list items and map values:

- strings must be quoted: `['go' 'bash']`, `[host: 'localhost']`
- bare word → **compile error**: `bare word in a list: quote it, 'https://a.com'`
- allowed unquoted: numbers, `$x`, `$( )`, `( value )`, globs, nested lists/maps, blocks, lambdas
- map keys are bare names `[a-zA-Z0-9_-]+` followed by `:` and whitespace; any other key is quoted: `['content type': 'x']`

So bare words are strings only in call args, where shell syntax needs them. Everywhere a value is written as data, a string looks like a string.

A call inside an expression needs parens: `(len $xs) + 1`. `len $xs + 1` passes three args to `len`.

A call ends at end of line, `;`, `|`, `|>`, `&&`, `||`, `)`, and — in `if` heads — `{`.

**Globs** are source text, not values, so expanding them is not re-interpretation:

- in the args of any call, fn or external: expand to separate args, as if the names were typed — `ls *.go` works whether `ls` is an external or a wrapper fn
- inside a list literal: expand to elements — `[*.go *.md]` is one flat list
- in value position (right of `let`, left of `|>`, head of a statement): one `list` value — `let fs = *.go`, `*.go |> each {...}`
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
- Reserved names: `$it` (implicit lambda param) and `$_` (last REPL result). `let it = ..`, `let _ = ..` and params named `it` or `_` are compile errors.

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
  - to `str` (printing, args to externals): integers as `3`; terminating values as exact decimals (`7/2` → `3.5`); a value with no exact decimal (`1/3`) shows as `1/3` at the REPL and raises when passed to an external — no silent rounding; round explicitly: `1 / 3 |> round 4` is `0.3333` (half away from zero)
- Number literals lex as `num` everywhere and keep source text for argv (`0755` stays `0755`).
- No truthiness. `if`, `&&`, `||`, `!` on values require `bool`.
- Indexing: `$xs[0]`, `$xs[1:3]`, `$m[key]`. `.` is never field access, so `$name.txt` concatenates.
  - an index or slice end is a literal, a `$var` or a value expression: `$xs[(len $xs) - 1]`, `$xs[$i + 1:]`; a call needs parens; out of range raises; no negative indices
  - inside a string only a literal or `$var` index: `"$xs[$i]"`; `"$xs[$i + 1]"` is an error, since interpolation is never code
  - slices are half-open, either end may be left out (`$xs[2:]`, `$xs[:2]`); only lists slice
  - a map key is a str: `$m[1]` is the key `'1'`, as in `[1: x]`, but a num in `$m[$n]` raises (`let k = str $n`)
  - a key twice in a map literal is a compile error
- Strings are never word-split.

## Expressions

```sh
let mode = if $CI == 'true' { 'release' } else { 'debug' }        # if is an expression
let sq   = [1 2 3] |> map {|x| $x * $x }                          # [1 4 9]
fn add a b { $a + $b }                                            # last expression is the value
let n    = add 1 2                                                # 3
```

- An `if` whose value is used — right of `let`, inside `( )`, left of `|>`, the last statement of a fn or lambda — must have an `else` when its then-branch ends in an expression (literal, `$x`, operators, list, map); otherwise a false condition would silently give `$nil`. This is decided from syntax. A then-branch ending in a call (`print`, `fail`, a fn) needs no `else`; the missing branch is `$nil` with no output:

  ```sh
  fn sign x { if $x < 0 { 'neg' } }                       # error: needs an else
  fn check x { if $x < 0 { fail 'neg' } }                 # fine
  fn hanoi n a b c { if $n > 0 { hanoi ..; print ..; hanoi .. } }   # fine
  ```
- Recursion is the only unbounded loop. Tail calls are guaranteed not to grow the stack.

## Conditions, `&&`, `||`, `!`

Same operators everywhere. Each operand is classified:

| operand | true | false | otherwise |
|---|---|---|---|
| a call or pipeline (`grep -q x f`, `exists $p`, `$f x`) | succeeds and returns `$nil` (a command) or `$true` | fails, or returns `$false` | raises: not a bool |
| an expression (`$x`, `( )`, literal, operators) | `$true` | `$false` | raises: not a bool |

The operand's syntax decides, never an inferred kind. `if $x` with `$x` = `$nil` raises; only a call's `$nil` means success.

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
if $n > 1 && ends_with .go $f && grep -q TODO $f { print $f }
```

A bash guard idiom fails loudly with a hint:

```
$ test -f .env && cat .env
error: 'test -f .env' failed (exit 1)
  hint: to run only if it succeeds, use: if test -f .env { ... }
```

## match

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
  [status: 'ok' data: d]    => $d |> each { print $it[name] }
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

- `pattern if cond => body`: the arm runs only if the guard also holds. The guard is in condition position and sees the pattern's names.
- A body is one expression or a `{ }` block, starting on the same line as `=>` (on the next line it could not be told apart from the next arm). It sees the pattern's names, which are not visible after the match. To give a fn, put it in a block: `=> { {|x| ... } }`.
- Arms are separated by newlines or `;`.
- No arm matches → raises `match: no arm matches <value>`. There is no silent fall-through.
- Values of different types never match: `'1'` does not match `1`, and comparing them does not raise.
- The value of a match is its arm's value, as with `if`. As a statement whose arms end in values that are dropped, it is `unused value`.
- Compile errors: an arm after an unguarded `_` or `name` (unreachable), a name bound twice in a pattern, a bare word (`foo.txt`; a name binds, a str is quoted), `true`/`false`/`nil` (write `$true`), interpolation or `$( )` in a pattern, a match with no arms.
- `match` cannot follow `|>`: it is a keyword, not a fn. Write `match $x { ... }` or `match (cmd | from_json) { ... }`.
- No patterns for types (any str, any num); use a name and a guard.

## Pipes

```
a |> f x y    ≡   f x y a
```

- A pipeline is stages joined by `|` and `|>`, left-associative. `|>` binds tighter than `&&` / `||`.
- Each `|` connects the output of the stage before it to the stdin of the stage after it; both run concurrently.
- Each `|>` passes the value of everything before it as the last arg of the stage after it.
  - left of `|>`: anything with a value. An external there is a **compile error**: `ls has no value: ... | lines`.
  - right of `|>`: any fn or fn value (`|> $f`). An external is a **compile error**: `jq reads stdin, not a value: print $x | jq`.
  - a lambda right after `|>` or `|` is called in place, like `$f`: `ls | lines |> { print $it[0] (len $it) }`, `cmd | { lines |> len }`. It names the whole value, unlike `each`, which calls it per item. After `|>` it takes the value as its param (`$it` or `{|x| }`); after `|` it takes none and reads stdin, so `$it` there is a compile error.
- The pipeline's value is its last stage's value. A stage's output not followed by `|` goes to the pipeline's stdout; a stage's stdin not preceded by `|` is the pipeline's stdin.
- bytes → value: `$( ... )`, or a converter reading stdin: `ls | lines`.
- value → bytes: only `print` and `to_json`. An expression or a value-only builtin on the left of `|` is a **compile error**: `print it: print $x | cmd`. No implicit conversion, no conversion rules.
- Pipefail is on; a stage killed by SIGPIPE is not a failure.

```sh
ls | lines |> len
git log --oneline | head -5 | lines
print hello | tr a-z A-Z
$xs |> print | tr a-z A-Z                # x |> print = print x
$xs |> to_json | jq .                    # to_json prints, like print
*.go |> each {|f| wc -l $f } | sort -n   # each's output: all iterations
```

### Streaming

`cmd | lines |> map {..} |> filter {..} |> each {..}` handles each line as `cmd` prints it; endless producers (`yes`, `tail -f`) work.

- The stages before a converter run concurrently and are passed to it as a stream; `lines` and `lines0` turn it into a lazy list, and `map`, `filter`, `each` pass items on one at a time.
- Anything else (another fn, a variable, the end of the pipeline) collects first, so variables never hold lazy values: `let xs = cmd | lines` is a plain list.
- `$(cmd) |> lines` gives the same list without streaming.
- A failing producer raises when its output ends; items before that were already handled. If the consumer stops early (an error), the producer is stopped (SIGPIPE, or killed if it isn't writing).

## Functional extras

### `$it`

A block that uses `$it` is a one-param lambda. `{|x| ... }` stays the general form.

```sh
*.go |> each { gofmt -l $it }
[1 2 3] |> map { $it * $it }
gh pr list --json number,title | from_json |> each { print "$it[number] $it[title]" }
```

- `$it` belongs to the innermost block that uses it. Using `$it` in a block nested inside another `$it` block → warning: name the outer param with `{|x| ... }`.
- A block with explicit params cannot use `$it`.

### Purity

The compiler infers whether a fn is **pure**: it calls no external, no effect builtin (`print`, `to_json`, `cd`, `export`, `exit`), and no impure fn.

```
$ explain 'fn add a b { $a + $b }; fn ll { ^ls -la }; fn tag v { "v$v" }'
fn  add  → pure
fn  ll   → effects: ^ls
fn  tag  → pure
```

- A call that passes a fn value whose body is not known statically (`map $f`) is treated as impure.
- Purity changes no semantics today. It is what later features can rely on: memoization of pure fns, a dry-run mode that evaluates only pure code, and a parallel `peach` that rejects bodies changing shell state (`cd`, `export`). Externals in `peach` are fine; they run in separate processes.

### Values at the REPL

After an input runs, the REPL shows every value the input produced and did not pass on, in order, each marked `▶`:

- the value of each statement (not `$nil`)
- the value of each stage before a `|` (`f | cat`, `f | lines`); `|` takes only its output

```sh
> [1 2] |> len
▶ 2
> fn f { [a: 1] }
> f | cat > out.txt         # out.txt is empty; f's value went nowhere
▶ [a: 1]
```

- Values are shown after the command ends, by the REPL itself (after the OSC 133 `D` mark), so they never reach a pipe, a redirect or `$( )`: there is no value→bytes conversion.
- Only what was typed: values dropped inside a fn or lambda body (`fn g { f | cat; 2 }`) are not shown, nor statements of a `source`d file. Blocks of a typed `if` / `try` / `match` are typed.
- A script shows nothing; its last statement is checked instead (`unused value`).
- Other shells: elvish marks values with `▶` but writes them to stdout, and drops values sent into `|`; nushell renders the last value to stdout and converts values sent to externals by type; rash shows `Result N:`.

### `$_` at the REPL

The last value printed at the REPL is `$_`.

```sh
$ git ls-files | lines
[...]
$ $_ |> len
42
```

`$_` exists only at the REPL; in a script it is a compile error.

## Scope: cwd and env

The shell keeps a stack of scope frames. Each frame has a cwd and an env.

- `in_dir $path $block` pushes a frame with a new cwd, runs the block, pops it, and returns the block's value.
- `cd` and `export` modify the innermost frame. At top level that is the shell itself.
- A fn call does not push a frame, so `fn z q { cd ... }` changes the caller's cwd.
- Externals started in a frame get that frame's cwd and env.
- `$NAME` reads the env of the innermost frame.
- There is no scoped env block; for one command use the external `env`: `env GOOS=linux go build ./...`.

```sh
export GOFLAGS=-trimpath
in_dir web { npm ci; npm run build }
let files = in_dir /etc { ls | lines }
```

Implementation note (Go): never call `os.Chdir` / `os.Setenv`; set `exec.Cmd.Dir` and `exec.Cmd.Env` per command and resolve relative paths against the frame's cwd.

## Errors

An error is a plain map with fixed keys. A failing external raises:

| key | value |
|---|---|
| `cmd` | the command line |
| `code` | exit code, or `$nil` if the process was killed by a signal |
| `signal` | e.g. `'SIGINT'`, or `$nil` if the process exited |
| `msg` | message |
| `pos` | source position |

Exactly one of `code` / `signal` is non-nil.

### try / catch

```
try <block or $fn> catch <name> <block>
```

- An expression: the body's value, or — if anything in the body raises — the handler's value, with the error map bound to `$name`.
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
pipeline  = stage { ( '|' | '|>' ) stage }
stage     = call | expr | lambda                # lambda only after | or |>: called like $f
call      = head { word | redir }            # fn or external, resolved statically from head
head      = [ '^' ] NAME | PATHWORD
          | '$' NAME                          # call a fn value; needs >= 1 word, or a | / |> stage
redir     = '>' word | '>>' word | '<' word | '2>' word | '2>&1'
expr      = literal | GLOB | '$' NAME { '[' idx ']' } | '$(' pipeline ')'
          | '(' value ')' | list | map | lambda | kw_expr | expr OP expr | OP expr
value     = kw_expr | chain
word      = atom { atom }
atom      = BARE | NUM | STR | '$' NAME { '[' idx ']' } | '$(' pipeline ')'
          | '(' value ')' | list | map | block | lambda | '...' ( '$' NAME | '(' value ')' ) | GLOB
block     = '{' { stmt ( NL | ';' ) } '}'
lambda    = '{' '|' NAME* '|' { stmt ( NL | ';' ) } '}'
          | block                               # uses $it → one param
params    = NAME* [ '...' NAME ]
STR       = "'" { CHAR | "''" } "'"              # one line
          | '"' { CHAR | ESC | VARREF } '"'      # one line; VARREF = $name, $name[..], ${name}
          | { ( '\\' TEXT | '$\\' { CHAR | VARREF } ) NL }+  # multiline, one prefix per line
```

Redirections apply to fn calls too (the fn's stdin/stdout).

### Line continuation

A statement continues on the next line only when:
- a bracket `( [ { $(` is still open, or
- the line ends with `|`, `|>`, `&&`, `||`, or
- the statement is incomplete (`let x =`), or
- the line is a multiline string line (`\\` / `$\\`); the statement stays open until a line that is not part of the string, which continues it only if it starts with `|`, `|>`, `&&`, `||`.

The REPL uses the same rule to decide whether Enter runs the line or opens a new one. It never has to look ahead: every decision is made from the line just entered.

Separators: newline or `;`. Comments: `#` at the start of a word, to the end of the line.

## One-liners

The language is typed at a prompt, so everything must also fit on one line.

- `;` separates statements everywhere: top level and inside blocks.
- Blocks, lambdas, `if` / `else`, `try` / `catch` all fit on one line.
- fns are called like commands: no parens, no commas.
- At the REPL, an expression statement or fn call shows its value, marked `▶`, unless it is `$nil`. A `str` shows raw; other values in entish literal syntax. The last shown value is `$_`. See Values at the REPL.
- `...(value)` spreads any list into args, not only `...$name`.
- From another shell: `entish -c '...'`.

```sh
# loop over a glob
*.go |> each {|f| gofmt -l $f }

# count files (REPL prints 42)
git ls-files | lines |> len

# total lines of go files
git ls-files '*.go' | lines |> map {|f| num $(wc -l < $f) } |> reduce 0 {|a x| $a + $x }

# chain with fallback
make && make install || print 'build failed'
git pull --ff-only || git pull --rebase

# if / else on one line
if test -f .env { print 'has env' } else { print 'no env' }

# scoped cwd, one-command env
in_dir web { npm ci; npm run build }
env GOOS=linux GOARCH=arm64 go build -o app ./cmd/app

# rename
*.jpeg |> each {|f| mv $f $(basename $f .jpeg).jpg }

# files containing TODO
git ls-files '*.go' | lines |> each {|f| if grep -q TODO $f { print $f } }

# JSON from an external
gh pr list --json number,title | from_json |> each {|p| print "$p[number] $p[title]" }

# values -> bytes -> external
git log --format=%an | lines |> count_by {|x| $x } |> to_json | jq 'to_entries | sort_by(-.value) | .[:5]'

# try / catch on one line
try { curl -fsS -o /dev/null https://example.com; print up } catch e { print "down: $e[code]" }

# arithmetic
[1 2 3] |> each {|x| print ($x * 10) }
entish -c 'print (2 ** 10)'
```

### xargs replacement

No dedicated syntax: spread for one call, `each` for one call per item, `chunk` for long lists.

| task | bash | elvish | entish |
|---|---|---|---|
| all items as args to one call | `git ls-files '*.go' \| xargs gofmt -l` | `gofmt -l (git ls-files '*.go')` | `gofmt -l ...(git ls-files '*.go' \| lines)` |
| one call per item | `... \| xargs -n1 wc -l` | `git ls-files '*.go' \| each {\|f\| wc -l $f }` | `git ls-files '*.go' \| lines \|> each {\|f\| wc -l $f }` |
| batches (ARG_MAX) | `... \| xargs rm` (automatic) | — | `find . -name '*.log' \| lines \|> chunk 500 \|> each {\|c\| rm ...$c }` |
| kill by name | `pgrep -f srv \| xargs kill` | `kill (pgrep -f srv)` | `kill ...(pgrep -f srv \| lines)` |
| parallel | `xargs -P4` | `peach` | not yet — Open decision 11 |
| NUL-separated | `xargs -0` | `from-terminated "\x00"` | `find . -print0 \| lines0 \|> each {\|f\| ... }` |

Like elvish, `cmd | lines |> each` streams: it starts on the first line while the producer still runs.

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

let n = len $langs
print "hello $name, $n langs"

fn add a b { $a + $b }
print (add 1 2)                     # 3

$langs |> each {|l|
    print (if $l == 'nix' { "$l: declarative" } else { $l })
}

let today = $(date +%F)             # stdout -> str
let files = ls | lines              # output -> list
let count = len $files
print "$today: $count files"

ls -la | grep go                    # byte pipe
ls *.c *.h                          # globs; no match is an error
grep -q TODO main.go || print 'no todos'

if ends_with .go $name && grep -q TODO $name {
    gofmt -l $name
}
```

### immutable data

```sh
let cfg = [host: 'localhost'  port: 8080]
let cfg = $cfg |> assoc port 8081           # new value, old one unchanged
print "listening on $cfg[host]:$cfg[port]"

let pkgs = ['git' 'curl' 'jq']
nix profile install ...$pkgs                # explicit spread to external
```

### value pipeline

```sh
let big = git ls-files | lines |>
    filter {|f| ends_with .go $f } |>
    map {|f| [file: $f  lines: (num $(wc -l < $f))] } |>
    filter {|r| $r[lines] > 300 } |>
    sort_by {|r| $r[lines] }

let total = $big |>
    map {|r| $r[lines] } |>
    reduce 0 {|acc x| $acc + $x }

print "$total lines in big files"
```

### bytes ↔ values

```sh
git log --format=%an | sort | uniq -c       # bytes only, like any shell

git log --format=%an |                      # output -> stdin
    lines |>                                # stdin -> list
    count_by {|x| $x } |>                   # values
    to_json |                               # values -> bytes
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
[1 2 3] |> map $double                      # [2 4 6]

fn inc x { $x + 1 }                         # a named fn is a value too
fn twice g x { $g ($g $x) }
twice $inc 5                                # 7
fn adder n { {|x| $x + $n } }               # returns a closure
[1 2] |> map (adder 3)                      # [4 5]
['a b' 'c'] |> map $lines                   # builtins are values: [['a b'] ['c']]
```

### recursion instead of while

```sh
fn retry n f {
    try $f catch e {
        if $n > 1 { sleep 1; retry ($n - 1) $f } else { fail $e }   # tail call
    }
}

let body = retry 3 { $(curl -fsS https://example.com) }
```

### build script

```sh
#!/usr/bin/env entish
export CGO_ENABLED=0
let version = $(git describe --tags --always)
let targets = ['linux/amd64' 'darwin/arm64']

fn build target {
    let p = split_by / $target
    let os = $p[0]
    let arch = $p[1]
    env GOOS=$os GOARCH=$arch go build -ldflags "-X main.v=$version" -o "dist/app-$os-$arch" ./cmd/app
    print "built $target"
}

go vet ./... && go test ./...                # stops loudly on failure

$targets |> each {|t| build $t }

in_dir dist { sha256sum app-* > SHA256SUMS }

let rel = gh release view --json tagName,url | from_json
print "latest release: $rel[tagName] $rel[url]"
```

## Errors the compiler catches

```sh
let w = ls                  # ls has no value: capture its output with $( ) or read it with | lines
let name = bob              # bob has no value; to write a string, quote it: 'bob'
$xs |> jq .                 # jq reads stdin, not a value: print $x | jq
ls | len                    # len takes a value, not stdin: ls | lines |> len
fn sign x { if $x < 0 { 'neg' } }      # this if is used as a value, so it needs an else
nix profile install $pkgs   # list passed to external, use ...$pkgs
$xs | jq .                  # list on left of |: print it first (to_json, print)
let n = len $xs + 1         # len takes 1 arg, got 3: use (len $xs) + 1
fn ls ...a { ls ...$a }     # warning: ls calls itself, did you mean ^ls?
fn split x { ... }          # warning: shadows /usr/bin/split
{ $f; print done }          # unused value: $f is not called; to call a fn value: call $f
{ print hi }; print done    # unused value: { } is a fn value, not a group; to run it now: call { ... }
```

Runtime: non-bool value in a condition, external not found.

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
| `\|> + 6` appends the piped value; `\|> + _ 6` places it with `_`. | An optional placeholder handles fns whose data argument is not last. | `\|>` always appends. A `_` placeholder would remove the data-last constraint for user fns. | proposed |
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
| A line continues only when brackets are unbalanced or it ends with an operator. | The REPL decides at Enter; it can't look at the next line. | Line continuation: open bracket or trailing `\|`, `\|>`, `&&`, `\|\|`. | applied |
| `(values '{...})` shows how a shell line was parsed. | Parse results should be inspectable. | `explain` builtin. | applied |
| cwd and env are per job; `{ }` scopes `cd`; `global cd` changes the shell. | Scoped cwd/env fits immutable design. | `in_dir`, scope frames. | applied |
| `[` is a subshell in shell syntax, `(` in Scheme syntax, and a glob class; the docs give `''[a-z]*` as a workaround. | One bracket must not carry several meanings. | `[` glued = index, after space = list/map; no glob classes. | applied |
| Status values: `(ok v)`, `(failed 2)`, `(killed sigint)`, `(stopped sigtstp)`. | Exit code and signal are different outcomes. | An error map has exactly one of `code` / `signal`. | applied |
| POSIX syntax kept for commands: `&&`, `\|\|`, redirections. | Daily command syntax should stay familiar. | `&&`, `\|\|`, `!` with condition-position rule. | applied |
| Two syntaxes switched by `( )` / `{ }` and `#!shell` / `#!scheme`. Documented limits: command args from Scheme must be string / list / closure; REPL accepts what files don't; file syntax guessed from its name. | Mode switching creates many boundary rules. | One grammar; one call syntax for fns and externals. | validated |
| Job control works because Scheme jobs run in a forked subprocess. | Job control needs fork; Go has no fork without exec. | No job control. | applied |
| Structured pipelines over `\|` as NDJSON, table on a TTY, streaming. | Structured data can cross process boundaries and stream. | `\|>` is in-memory and eager. | proposed |
| FAQ: a new ad-hoc language is "a large endeavour, and a significant obstacle to adoption". | A new language has a learning and maintenance cost. | Keep the core small (6 keywords). | validated |

## Not planned

- job control (`&`, `fg`, `bg`, Ctrl+Z)
- aliases — use fns
- subshells — use `in_dir`
- flag parsing for fns
- POSIX `VAR=x cmd` — use the external `env VAR=x cmd`
- scoped env blocks (`with_env`) — use `export`, or `env` for one command
- auto-currying — `|>` with data-last fns covers most uses, and a wrong arg count would silently yield a fn

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
4. ~~`|>` eager or streaming~~ — decided: streaming. In `cmd | lines`, cmd runs concurrently; `lines`, `lines0`, `map`, `filter`, `each` pass items one at a time; anything else (another fn, a variable, the end of the pipeline) collects first, so variables never hold lazy values. A failing producer raises when its output ends.

From the lessons above:

5. Optional `_` placeholder in `|>` stages: `$x |> split_by / _`.
6. Per-command success codes, e.g. `ok_codes [0 1] { diff a b }`, or leave it to `||` / `try`.
7. Resolve the external (PATH lookup) before evaluating its arguments.
8. Terminal handling without job control: children stay in the shell's process group, the shell ignores SIGINT/SIGQUIT while a foreground child runs, TUI programs keep the tty.
9. Never set `$SHELL` to entish; tools run POSIX code through it.
10. ~~Process substitution~~ — decided: the converter `as_file`, a temp file, instead of new syntax. `<(cmd)` would make `( )` mean output after `<` only and changes the meaning of `<($path)`; `<$(cmd)` is already a redirect from a computed path; `$<(cmd)` needed its paths kept to call args. A streaming form (pipes, e.g. a builtin that passes `/dev/fd/N` paths to a lambda) can be added if needed.

From one-liners:

11. xargs gaps (NUL-separated input is done: `| lines0`): parallel `peach n f xs` (bodies that call `cd` / `export` rejected, see Purity).

From removing fn kinds (see Output and values):

12. "No value" is `$nil` (current), or a separate *void* that raises when used as a value. `$nil` keeps one concept and no runtime check; the cost is that `let x = ll` is silently `$nil`.
13. ~~`eval` source restricted to `'literal'` / `$var`~~ — decided: dropped. Binding the source to a variable first passed the same injection (`let s = "rm $f"; eval $s`), so the rule only added friction; values still go in safely as `$args`. A dynamic command name is `^$name`, not `eval`.
14. Lint for the "user's responsibility" table: none (current), or warnings from best-effort inference that never change behavior (e.g. in `explain`).
15. Stdin fallback: only converters (current), or every data-last builtin. Converters read stdin wherever their data arg is left out, not only right after `|`; a forgotten arg (`let xs = lines`) waits for input instead of being a compile error.
16. What `try` catches. Now: everything except Ctrl+C, including bugs in the body: a type error (`$(stat -c %s $f) + 1` without `num`), an index out of range, `match` with no arm, `$nil` passed to an external. `let size = try { $(stat -c %s $f) + 1 } catch e { 0 }` is then always `0`, silently. Options:
    - catch only command failures and `fail`; language errors pass through as bugs. The line is unclear: `from_json` on bad input or an index into outside data may be data errors the caller wants to catch.
    - add a `kind` key to the error map (`'cmd'`, `'fail'`, `'type'`, ...) and keep catching everything; the handler decides.
    - keep catching everything (current).

## Deferred designs

Kept for later; not part of the current language.

### Pipeline sections

A parenthesized pipeline that starts with `|>` has no input yet, so it would be a fn value: `(|> f x |> g)` is `{|v| $v |> f x |> g }`.

```sh
let count = (|> lines |> len)
$(git ls-files) |> $count
```

Deferred: a lambda already says it (`{|x| $x |> lines |> len }`), and `(` would gain a second meaning besides grouping.
