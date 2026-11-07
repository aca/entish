# AGENTS.md

entish is a non-POSIX shell with a strict, functional language, written in Go. README.md describes what is implemented. DESIGN.md is the design spec and records decisions, including deferred and rejected ones. REVIEW.md lists known gotchas and tradeoffs.

Copy the best behavior from modern shells.

- /home/rok/src/github.com/aca/elvish
- es-shell /home/rok/src/github.com/wryun/es-shell
- nushell
- schemesh /home/rok/src/github.com/cosmos72/schemesh
- rash /home/rok/src/github.com/willghatch/racket-rash



## Commands

```sh
go build -o entish .                      # module root is package main
go vet ./... && go test ./...
go test -race ./...                    # streams, async completion and suggestions are concurrent
go test ./lang -run TestStreaming      # a single test
./entish -c 'ls | lines |> count'        # run source; -x logs each external before it runs
```

Interactive behavior (raw mode, escape sequences, async UI) can't be tested with `-c`. Drive the binary through a pseudo-terminal, e.g. Python `pty.fork()`. Set `XDG_CONFIG_HOME` and `XDG_DATA_HOME` to temp dirs so the user's rc file and history DB are not touched.

## Architecture

```
main.go / term.go   wiring: rc file, signals, nushell-style prompts, OSC 7/133, slow-command timing
repl/               loop; depends only on its own Editor and Language interfaces
lineedit/           multi-line vi-mode editor: history search, suggestions, async completion
complete/           completers: command names (async PATH scan) and files
history/            sqlite history (modernc.org/sqlite, pure Go)
lang/               the language
```

### Separation rules

- `repl` imports neither `lineedit` nor `lang`, and `lineedit` knows nothing about the language. Both the editor and the language must stay replaceable.
- Glue lives in `main.go`. For example, `complete.Shell` is given `in.Cwd`, `in.Getenv` and `in.Names`.

### `lang` pipeline

`parse.go` → `check.go` + `scope.go` → `eval.go` / `exec.go`.

**Parser (`parse.go`)**
- Scannerless. How a word lexes depends on its position: call args vs. value expressions vs. list/map data.

**Checker**
- Runs on the whole program before anything executes, so most mistakes are compile errors.
- Binds every call to a fn, a builtin or an external. Precedence: keyword > fn > builtin > PATH; `^name` forces the external.
- Fns have no kinds. Every call emits an output stream and a value stream (empty for externals and `print`); behavior never depends on inference. STREAMS.md is the spec of this model.
- Checks are syntactic and reject only what the source makes certain: an external or a `NoValues` builtin used as a value (`let x = ls`, `ls |> each`), an expression or a builtin without `Out` on the left of `|`, an external or a builtin without `VIn` on the right of `|>`, a non-converter builtin after `|`, a glob where one value is needed, arity.
- `scope.go` checks that every `$name` is lexically bound.

**Interpreter**
- Never calls `os.Chdir` or `os.Setenv`. The shell's cwd and env live in `Interp` and are applied per `exec.Cmd`.
- Variables are immutable `Env` chains; closures capture by value.
- `$lower` is a lexical variable. `$Upper` and `$env:x` are env vars.
- Errors raised by commands are `*Raise` holding a plain error map (`cmd code signal msg pos`).
- Every expression emits its values to `ioCtx.vout` (`emit` in `eval.go`); `one` collects exactly one where a single value is needed (`let`, operands, `( )` everywhere). `...` spreads one list; `|> list` collects a stream.
- Each operator takes one stream: `|` output → stdin, `|>` values → value input (`ioCtx.vin`), `$( )` output as a str, `( )` the values. All stages of a pipeline run concurrently (`exec.go`); the stream an operator does not take is dropped (values) or goes on to the pipeline's output (bytes).
- `$_` is the variable named `_`. A stage after `|>` that uses it (`Pipeline.topic`, set in `scope.go`) runs once per value with `$_` bound (`topicStage` in `exec.go`); lambdas and fn bodies don't see the `$_` around them.
- In "condition position", a failing command yields `false` instead of raising (not one killed by SIGINT; `try` does not catch Ctrl+C either): `if` conditions, the left of `||`, the operand of `!`. A command operand is tested by its status, any other (`( )` included) must be one `bool` (`test` in `eval.go`); `if big 1` on a fn whose body ends in a value is a compile error.

**Streaming (`stream.go`)**
- Values flow between stages through a `vpipe` (a buffered channel). A send after the reader stopped fails with `errVClosed`, which, like EPIPE, is not a failure of a stage before the last.
- Streams are never values: a statement ends when all its stages have, so variables hold complete values.
- When the last stage of a pipeline ends, stages still running after 200ms are killed via processes recorded through `ioCtx.onStart`.

**Builtins (`builtins.go`)**
- A builtin's `Fn` emits its values through `io.emit`. Flags on each builtin:
  - `Stdin`: a converter (`lines lines0 from_json as_file`); with its data arg left out it reads stdin, like `cat`.
  - `VIn`: reads the value input (`each count print to_json`), so it belongs after `|>`.
  - `Out`: may write output, so it can go left of `|`. `NoValues`: emits no values, so it cannot be used as one.
  - `Min`/`Max`: arity.
- Keep the set minimal; the user explicitly wants few builtins.

### `lineedit`

- **Key loop:** one buffer for the whole input; `ReadInput(prompt, rprompt, cont, more)`. Enter accepts unless `more` (the language's `NeedsMore`) reports the input unfinished.
- **Async work:** completion (`Completer`) and suggestions (`Suggester`) run in goroutines with a `context.Context`.
  - Any key other than Tab cancels them.
  - Results are dropped if the buffer changed meanwhile.
- **Waking the loop:** a self-pipe (`wakeR`/`wakeW`). The loop `select`s on the terminal and the pipe and receives `keyWake`.
- **Tests:** feed keys through `e.replay` and call `insertKey`/`normalKey` directly, as in `vi_test.go`.

## Design principles

The user cares strongly about these; check DESIGN.md before changing semantics.

- Minimal keywords (currently `let fn if else try catch match`) and minimal builtins.
- No implicit conversions:
  - Values pass between fns only via `|>`; `print` and `to_json` are the only ways from a value to bytes.
  - Bytes pass between commands only via `|`.
  - Never add rules that convert lists to text or text to lists.
- No special-case exceptions to rules. Prefer a compile error with a hint over silently doing something.
- Avoid "gotcha" behavior. Silent wrong results are the worst outcome.
- Strings: `'raw'`, `"..."` (interpolates variables only, never code), and Zig-style multiline `\\` / `$\\` lines.
- Discuss significant language changes before implementing them. The user decides design questions; present options with tradeoffs.
