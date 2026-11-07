# entish vs Bash

From the repository root:

```sh
python3 bench/run.py
python3 bench/run.py --size 100000 --runs 15 --json /tmp/entish-bench.json
python3 bench/run.py --entish ./entish
```

Requires Python 3, Bash, and Go (unless `--entish` supplies an existing binary).
The runner builds entish in a temporary directory before timing and cleans up
generated scripts afterward. It does not modify your config or history.

| Case | Work |
| --- | --- |
| startup | Print one constant, with no input list. |
| list | Construct the input list/array and bind the result. Bash copies the array; entish shares its immutable list. |
| arithmetic | Compute `(x * 3 + 7) % 97` for every input. |
| function | The same arithmetic through a named function for every input. |
| filter | Retain even inputs using `%` and a comparison. |

The `.ent` and `.bash` files are workload bodies. The runner prepends identical
integer inputs `0..size-1`, then appends result reporting. Both shells materialize
their output collection. entish uses `map`/`filter`; Bash uses array loops. Bash
functions return through a variable, avoiding command substitution and subshells.
There are no external commands, byte pipelines, or subprocesses inside the
measured scripts. Python launches each shell and measures it from outside.

Every full output is checked against Python's expected result before measurement.
Timed runs print only the length, first item, and last item, which are also checked.
The runner clears inherited shell configuration, uses an empty PATH, and enables
entish's external-command trace; errors or stderr output fail the benchmark.

Results are median wall-clock milliseconds, with min/max and the entish/Bash ratio.
Samples alternate shell order after warmups. `--json` saves every sample and
environment metadata. Startup, file reading, parsing, entish's static checking,
input construction, execution, collection allocation, summary output, and process
teardown are all included. Build time, script generation, and full-result
validation are excluded. The startup and list cases provide context; their
times are not subtracted from other cases.

This compares equivalent small scripts, not isolated arithmetic instructions.
entish uses arbitrary-precision exact rationals; Bash uses fixed-width integers.
The default inputs keep results equivalent without overflow, fractions, or
negative remainder differences. Function and iteration mechanisms also differ.
Run on an otherwise idle machine and compare multiple input sizes before drawing
conclusions; short cases are particularly sensitive to process startup and noise.

## Example measurement

Measured on 2026-10-30 UTC, interpreter revision `63b9ef2`, AMD Ryzen 7 8845HS,
Linux x86-64, Go 1.27.3, Bash 5.3.9. Command:

```sh
python3 bench/run.py --size 100000
```

Two warmups and nine measured runs per shell; medians in milliseconds:

| Case | entish | Bash | entish / Bash |
| --- | ---: | ---: | ---: |
| startup | 1.647 | 1.046 | 1.57 |
| list | 68.759 | 116.951 | 0.59 |
| arithmetic | 172.958 | 292.855 | 0.59 |
| function | 203.518 | 459.976 | 0.44 |
| filter | 129.151 | 213.822 | 0.60 |

These are observations from one machine, not performance guarantees. In a
separate 10,000-item run, arithmetic took 20.262 ms in entish and 30.617 ms in Bash.
