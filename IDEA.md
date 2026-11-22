# Ideas

Features considered for entish: done, TODO, maybe, rejected. DESIGN.md stays the spec; an idea moves there once it is decided.

Most ideas lean on what other shells lack: calls are bound before running, effects come only from externals, values are immutable, and bytes and values are separate streams.

## Done

- **Numbered REPL results.** The prompt shows `[42]`; `$42` replays that input's displayed values as a stream (`$42 |> count`). Collect with `let saved = ($42 |> list)`; index with `($42 |> list)[0]`, not `$42[0]`. Single-value positions require exactly one value. Keeps the last 100 inputs in memory; failed, expired and unknown references are errors. Stdout and intermediate values are not retained. Direct REPL input only; named values and closures keep results beyond eviction.
- **Error report.** An uncaught error shows the source line, every failed stage of its pipeline (pipefail raises the rightmost; the others are marked too) and the fn calls it came out of. Display only; the error map is unchanged (`lang/report.go`).
- **`|` on an empty line** inserts the last input of this session before it (`echo 3 |`). It reruns, but the full command is shown before Enter.
- **Bracket pair.** The bracket at the cursor and its pair are underlined. lang finds the pair (`MatchBracket`) and skips strings and comments; nothing past a syntax error is matched.
- **Completion menu narrows as you type** (fuzzy, over the items already received; the completer is not rerun).
- **Description column** for completions (fish descriptions); the menu scrolls to the selected item.
- **Window title** (OSC 2): the directory at the prompt, the command while it runs. Control chars are stripped.
- **`quote`**: a str as an arg that reads back as the same str (`'it''s'`, `"a\nb"`), so Ctrl+T inserts picked file names safely. TODO: `--sh` for POSIX sh.
- **Variable peek.** With the cursor on a `$name`, its value shows dim below the input (`lang.Peek`). Only names bound by earlier inputs, env vars and `$42`: the scope checker tells a name bound in the input (no value yet) from an earlier one, so a shadowed name shows nothing. Big values are cut while rendering.

## TODO

Recorded in DESIGN.md.

- **List and map destructuring in `let`.** Reuse `match` patterns to bind names from one value, e.g. `let [node rest] = (term $ts)` or `let [port: p] = $config`.
- **Variable values in the error report**, from the env it failed in (pytest, Python tracebacks): `$dir = '/nonexistent'` under the failed line.
- **Complete the keys of a live value**: Tab after `$m[` offers the keys of the map the variable holds. It only reads the value.
- **Insert paths from the directory navigator** into the input line, quoted as entish strings. Directory navigation itself is implemented by `nav` / Ctrl+N; it changes cwd on Enter.
- **Stdlib**: `string:uppercase lowercase replace split` so far; the str they work on is the last arg, so they follow `|%`. A builtin's `Sig` shows below the input while a call is typed. Undecided:
  - the namespace name: `string:` vs `str:`, which matches the type name (`str $n`, "needs a str")
  - `$string:uppercase` as a value (`|% $string:uppercase`): only `$env:` parses today
  - `Sig` for the other builtins and for user fns (params are in the source)
  - which other string fns: `trim join contains starts_with ends_with`

- **Conditional argument groups (syntax open).** Include several ordinary call arguments together, such as `--header "Authorization: Bearer $token"` in a `curl` call. A false condition contributes zero arguments and skips evaluation of the group. Revisit the spelling: bare `if`, `...` and `...if` already work as literal arguments, `[]` constructs a list, and `( )` expects one value.

## Maybe

### Static checker

- **Live diagnostics.** Show checker errors under the input while typing (`ls |> lines` → `pipe its output with |`). Highlight already runs the checker and drops its errors.
- **`entish deps script`.** List the externals a script can run (`^$x` and `eval` reported apart).
- **Capability enforcement.** Refuse any exec outside that list; show the list before running a downloaded script.
- **Provable dry-run** (PowerShell `-WhatIf`, `make -n`). `entish -n` prints externals instead of running them. The checker can tell whether any external's output or status is used (`$( )`, `| lines`, a condition). If none is, the dry run is exact; otherwise it is marked as approximate from that line, or refused.
- **External arg warnings.** Catch typos from completion data, e.g. `git comit` → did you mean `commit`. Warnings only: false positives are likely.
- **Did you mean**, for unknown commands and `$vars`, by edit distance over fn, builtin, PATH and bound names.
- **Signature hint** while typing (`fn build dir  (rc:12)`), and **help for the word at the cursor** (fn src, builtin summary, external path and `man -f`).
- **Doc comments.** A comment right above `fn` is its description, shown in the completion menu's description column and in the hints (PowerShell comment-based help).
- **Bash paste hints.** Pasted `for f in ...; do ...; done`, `while read`, `$(( ))`, `[ ]` or `VAR=x cmd` gets a compile error with the entish equivalent, as `<( )` already does.

### nix

- **Command not found → nixpkgs.** An unknown external is known before running; suggest `nixpkgs#ripgrep` from nix-index. Running it through `nix shell` needs a confirmation.
- **`writeEntishApplication`** in the flake: check at build time that every external is in `runtimeInputs`. Needs `deps`.
- **Store paths in history.** Record the resolved path of each external (`/nix/store/…-git-2.47.1/bin/git`) to answer "it worked yesterday".
- **nix context in the prompt**: `IN_NIX_SHELL`, the devShell name.

### Values at the REPL

- **Display.** Shorten big values (`[a b c ... 9,997 more]`), summarize their shape (`list of 120 maps [id: num, title: str, ...]`, nushell `describe`, PowerShell `Get-Member`), show a list of maps with the same keys as a table, and color `▶` values with the input highlighter (they are entish literals). Display only.
- **Decimal next to a rational**: `▶ 4/3  (≈ 1.3333)`.
- **Diff on rerun.** When the same input runs again, show what changed since last time, key by key (`watch -d` for values). Keep the last value of a few inputs (LRU).
- **Last error as a value** (PowerShell `$Error`, Clojure `*e`): the error map of the last uncaught error, at the REPL only. Name undecided.
- **Persist values** across sessions by name (IPython `%store`). Needs a builtin; low priority.
- **Debug at the failure.** Open a REPL with the scope of the failure point (IPython `%debug`, pry). Values are immutable, so the state is intact.

### History

- **History as values**: maps with cmd, cwd, code, duration and git branch (`history |% { ... }`).
- **Richer Ctrl+R.** Show exit code, duration and cwd per entry; dim failed ones; rank entries from this directory or repo first (atuin).
- **History to script.** Pick entries (REPL and script grammar are the same) and write them as a script or fn.
- **No history for an input that starts with a space** (bash `ignorespace`), for secrets. Not checked whether it exists.

### Editor / TUI

- **Spreadsheet at the REPL.** Open a list of maps (or explicitly parsed CSV) as an editable grid, add calculated columns using entish expressions, sort/filter rows, and accept the result as a new value for further pipelines. Keep the original value immutable; external commands require explicit execution, never automatic cell recalculation. Maybe: scope and UI are undecided.
- **`pick` as a table.** `pick` (implemented) could show a list of maps as a table (PowerShell `Out-GridView -PassThru`). It reads all values before showing them, and `fuzzy.go` has no scoring and runs on one thread; for millions of items, stream them in and use fzf's `algo` package.
- **Pickers in the editor**: Ctrl+T (files), Alt+C (cd), reusing `fuzzy.go`.
- **`Alt+.`**: the last argument of the previous input, older ones on repeat; lang gives the argument bounds.
- **History predictions as a list** below the input (PSReadLine ListView), with a key to toggle.
- **which-key.** After `d` `c` `y` in normal mode, list the keys that can follow.
- **Transient prompt.** Redraw the accepted prompt short (`$ cmd`) for a clean scrollback.
- **Prompt-stripping paste** (Julia). Pasted `$ ` prefixes are dropped and `▶` lines skipped, so examples from the docs paste as-is. A leading `$ ` is a syntax error, so nothing conflicts.
- **Glob count hint**: `rm *.log` shows `*.log → 37 files` while typing. It only reads directories.

### Running

- **Undo shell state.** Revert cwd, env and variables to before the last input. Cheap: they are immutable chains. It never touches files.
- **Record and replay externals** for script tests. Record argv, cwd, stdout and exit code of each external; replay the script without running any, and diff the calls after a refactor.
- **Per-stage timing** for slow pipelines: `took 12.3s (curl 11.8s | from_json 0.4s | each 0.1s)`.
- **Pipeline meter.** Values per second through each `|>`, and which stage is blocked (`pv` on every stage).
- **Remote closures**: `on host { ... }` runs a block on another machine and returns its values through `|>`.
  - Local variables go as values, so there is no quoting or injection; local fns go along; the block is checked locally; errors come back as error maps.
  - Needs entish on the remote host, and the remote PATH is known only after connecting.
  - Similar: mitogen, pyinfra.
  - Interesting but unsure.
- **Daemon mode** (Emacs daemon + eshell, tmux): one entish server holds every interactive session as goroutines; each terminal runs a thin client that relays keys and output (dtach).
  - Gains: real Go chans between terminals (`send`/`recv` with no serialization, so `1/3` and fns pass too), values shared across sessions, PATH scan, rc, completion caches and history loaded once and shared live.
  - Across processes a value channel is no better than `|> to_json >> f.ndjson` and `tail -f -n +1 f.ndjson | from_json`; only one process makes it worth a feature.
  - The server must own a pty per session (tmux). Passing the client's tty fd (`SCM_RIGHTS`) does not work: another session's tty cannot become a child's controlling tty without root, so `/dev/tty` readers (sudo, ssh prompts) and Ctrl+C break.
  - `lang` is nearly ready: cwd and env already live in `Interp` (read once in `lang/interp.go`). `lineedit` reads `$EDITOR`, `os.Stderr` and SIGWINCH globally.
  - Costs: a panic or OOM in one session kills every terminal, and Go can't stop a runaway pure computation (cancellation points in value ops); `umask` and rlimits are per process; upgrading the binary restarts the server and drops all sessions; scripts (cron, CI) must still run without it, so two modes.
  - If tried: an opt-in `entish --server` for interactive use only, `recover()` per session, scripts untouched. Weeks of work.

### Language

- **Shared command prefixes (syntax open).** Write `git -C $repo` once above a block with `status --short` and `diff --check` on separate lines. Each line forms a separate ordinary call with that prefix, in order. Nested blocks could extend the prefix. The sketch used `:: {` as a provisional delimiter.
- **Streaming `fold` (for design review).** Accumulate values from `|>` as they arrive. A possible call is `...[1 2 3] |> stream:fold 0 {|acc x| $acc + $x}`, yielding `6`, or the initial value for empty input. Review the name and callback signature alongside the list-based `fold` in `config.ent` and the external `fold` command.
- **`finally`** (PowerShell `try/catch/finally`, bash `trap EXIT`), for cleanup that must run: temp dirs, lock files, containers. It adds a keyword.

## Rejected

- **`$_` as the REPL's last value** (and numbered `$_1`, `$_2`): `$_` became the item of a `|%` stage (DESIGN.md, Pipes). Bind a result with `let` instead.

- **Live pipeline preview** (run the pure tail of a pipeline on every keystroke). The checker can prove it calls no external, but it is still too risky:
  - one big rational or string operation can't be interrupted (Go can't kill a goroutine), and memory can blow up;
  - the purity analysis must track every new effect path forever.
- **Capturing stderr into the error map.** The child's stderr stops being a tty, so tools change their output (colors, progress). It is also on screen already.
- **`trace` / `stages` keys in the error map.** Display only was chosen; the map stays `cmd code signal msg pos`.
- **Piping the previous stdout without rerunning:**
  - tee every command's stdout: children lose the tty (colors off, `ls` one per line, vim and less break);
  - proxy all output through our own pty: a terminal emulator's worth of work;
  - ask the terminal (`kitty @ get-text --extent last_cmd_output`): the shell would behave differently per terminal.

  Instead: `|` on an empty line for output.
- **Running a nix package without confirmation.** Silent behavior.
- **From PowerShell:**
  - binding pipeline input by property name (`ls | Remove-Item` finds `.Path`): implicit, and depends on the shape of the value;
  - splatting a map into `--key value` args: an implicit map→text conversion;
  - type-based default formatting of output: output changes by type, silently;
  - more value types and unit literals (`1GB`, `500ms`): conflicts with one num type.
