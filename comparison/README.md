# Values and bytes: entish, elvish, nushell, rash

Runnable snippets for how each shell moves data between commands: stdout bytes vs. typed values.

```sh
./run.sh            # all cases
./run.sh 03         # one case
./run.sh 03 nu      # one case, one shell
```

Each case dir has `entish.ent elvish.elv nu.nu rash.rkt`. Snippets are separated by `#---` (`;;---` in rash); the text after it is a title. Each snippet runs in its own process, so one error does not stop the rest. Text before the first separator is a prelude prepended to every snippet. Edit and rerun.

Needs `elvish`, `nu`, `racket` with the `rash` package, and `go` (entish is built from `..`).
Observed with nu 0.112.2, elvish 0.19.0-dev, Racket 9.1 + rash from the catalog.

## Models

| | channels per command | what `\|` carries | conversion at the boundary |
|---|---|---|---|
| bash | stdout + exit status | bytes | none, everything is bytes |
| elvish | byte output + value output (a stream of 0..n values) | both at once; the reader picks | `( )` merges both; `count`/`each` read bytes as lines |
| nushell | one: a value or a byte stream, decided at run time | that one thing | implicit, by type |
| rash | one per stage: a process has bytes, a Racket function has its return value | `\|` and `\|>` pick the next stage's kind | process → fn: a port (`\|>>`: a string); fn → process: `display` of the value |
| entish | output (bytes) + one value + status (raise) | output only | none between values and bytes; `print`/`to_json` and `$( )`/converters |

## Findings

**01 two outputs**: a fn that writes "out" and returns "val"
- elvish: `f | each` gets both `out` and `val`. `[(f)]` gives `[val out]`, and the order between the two channels is not fixed. `var v = (f)` fails at runtime with 2 values.
- nu: a fn's external writes to the pipe only when it is the last statement. `def g [] { ^echo out }` pipes it; adding a line after it sends `out` to the terminal instead.
- rash: a Racket function's own stdout never reaches the pipe; only its return value does.

**02 bytes → value**
- elvish: `e:printf 'a\nb\n' | count` is 2, since byte input is read as lines.
- nu: `| length` on a byte stream counts bytes (4) and `| str length` gives 3. `^printf '[1,2]' | length` is 5 with no error. `each` on a byte stream gets one chunk, not lines.
- rash: `|> length` gets a port and fails at runtime.

**03 value → stdin**
- elvish: `put [a b] | cat` prints nothing; values are dropped silently.
- nu: `[a b] | ^cat` sends a box-drawing table. `1 | ^od -c` and `true | ^od -c` send **nothing**, with no error.
- rash: Racket `display`: `(a b)`, `#hash((k . v))`, `#t`, `1/3`.
- entish: `print` of a list writes source form `['a' 'b']`. That too is a value → text rendering, with one fixed format.

**04 value as an argument**
- elvish: every value becomes its repr: `$true [a b] [&k=v] 1/3`.
- nu: floats are written in full (`0.30000000000000004`). Lists and records are errors.
- rash: lists are spread, `0755` becomes `755`, `display` for the rest.
- entish: num → decimal (exact, `0.3`), `1/3` errors, bool/map are runtime errors, a list is a compile error.

**05 trailing newlines**
- `$( )` in entish and `( )` in nu trim one trailing newline. elvish splits into lines. rash `|>>` keeps everything.
- `print`/`echo` add a newline. A string sent to stdin in nu and rash has none.
