#!/usr/bin/env python3
"""Compare complete entish and Bash scripts; measured scripts use no externals."""

import argparse
import datetime
import json
from pathlib import Path
import platform
import shutil
import statistics
import subprocess
import tempfile
import time


HERE = Path(__file__).resolve().parent
CASES = ("startup", "list", "arithmetic", "function", "filter")


def positive(value):
    value = int(value)
    if value < 1:
        raise argparse.ArgumentTypeError("must be at least 1")
    return value


def script(case, shell, values):
    if case == "startup":
        return "print ready\n" if shell == "entish" else "printf 'ready\\n'\n"
    body = (HERE / f"{case}.{'ent' if shell == 'entish' else shell}").read_text()
    if shell == "entish":
        return f"let xs = [{values}]\n" + body + """
let last = (len $ys) - 1
if $BENCH_VERIFY == '1' {
    print ...$ys
} else {
    print (len $ys) $ys[0] $ys[$last]
}
"""
    return f"xs=({values})\n" + body + """
if [[ $BENCH_VERIFY == 1 ]]; then
    printf '%s\\n' "${ys[@]}"
else
    printf '%s %s %s\\n' "${#ys[@]}" "${ys[0]}" "${ys[${#ys[@]}-1]}"
fi
"""


def expected(case, size, verify):
    if case == "startup":
        return ["ready"]
    values = list(range(size))
    if case in ("arithmetic", "function"):
        values = [(x * 3 + 7) % 97 for x in values]
    elif case == "filter":
        values = values[::2]
    if not verify:
        values = [len(values), values[0], values[-1]]
    return [str(x) for x in values]


def run(command, env, want):
    start = time.perf_counter_ns()
    result = subprocess.run(
        command, env=env, stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120,
    )
    elapsed = (time.perf_counter_ns() - start) / 1_000_000
    if result.returncode or result.stderr:
        raise RuntimeError(
            f"{command}: exit {result.returncode}\n{result.stderr.decode(errors='replace')}"
        )
    if result.stdout.decode().split() != want:
        raise RuntimeError(f"incorrect result from {command}: {result.stdout[:200]!r}")
    return elapsed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--size", type=positive, default=10_000, help="input items (default: 10000)")
    parser.add_argument("--runs", type=positive, default=9, help="timed runs per case (default: 9)")
    parser.add_argument("--warmups", type=positive, default=2, help="warmup runs (default: 2)")
    parser.add_argument("--entish", type=Path, help="existing binary; otherwise build in a temporary directory")
    parser.add_argument("--json", type=Path, help="save metadata and all timings")
    args = parser.parse_args()
    bash = shutil.which("bash")
    if not bash:
        parser.error("bash is required")
    bash = str(Path(bash).resolve())

    with tempfile.TemporaryDirectory(prefix="entish-bench-") as tmp:
        work = Path(tmp)
        entish = args.entish.resolve() if args.entish else work / "entish"
        go_version = None
        if not args.entish:
            print("Building entish (not timed)...", flush=True)
            subprocess.run(["go", "build", "-o", str(entish), "."], cwd=HERE.parent, check=True)
            go_version = subprocess.check_output(["go", "version"], text=True).strip()

        # No inherited BASH_ENV, exported functions, shell options, or user rc.
        # An empty PATH also catches accidental bare external command calls.
        for name in ("home", "config", "data", "empty-path"):
            (work / name).mkdir()
        env = {
            "HOME": str(work / "home"), "XDG_CONFIG_HOME": str(work / "config"),
            "XDG_DATA_HOME": str(work / "data"), "PATH": str(work / "empty-path"),
            "LC_ALL": "C", "BENCH_VERIFY": "0",
        }
        bash_version = subprocess.check_output(
            [bash, "--noprofile", "--norc", "--version"], env=env, text=True,
        ).splitlines()[0]
        report = {
            "date": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "platform": platform.platform(), "bash": bash_version, "go": go_version,
            "entish": str(args.entish.resolve()) if args.entish else "go build -o <temp>/entish .",
            "size": args.size, "runs": args.runs, "warmups": args.warmups, "results": {},
        }
        values = " ".join(str(x) for x in range(args.size))
        print(f"{bash_version}\n{platform.platform()}")
        print(f"{args.size:,} items; {args.warmups} warmups; {args.runs} timed runs per shell")
        print("Wall time includes startup, parsing, checking, execution and summary output.")
        print("Ratio = entish / Bash; below 1 means entish is faster. Times are milliseconds.\n")
        print(f"{'case':<12} {'entish median':>12} {'bash median':>12} {'ratio':>8}  {'entish min..max / bash min..max'}", flush=True)

        for case in CASES:
            commands = {}
            for shell in ("entish", "bash"):
                path = work / f"{case}.{shell}"
                path.write_text(script(case, shell, values))
                commands[shell] = ([str(entish), "-x"] if shell == "entish" else [bash, "--noprofile", "--norc"]) + [str(path)]
                # Full result validation is outside the timed samples. entish -x
                # traces externals; any stderr (including a trace) fails the run.
                run(commands[shell], {**env, "BENCH_VERIFY": "1"}, expected(case, args.size, True))

            want = expected(case, args.size, False)
            samples = {"entish": [], "bash": []}
            for iteration in range(args.warmups + args.runs):
                # Alternate order to reduce systematic first/second-run bias.
                order = ("entish", "bash") if iteration % 2 == 0 else ("bash", "entish")
                for shell in order:
                    elapsed = run(commands[shell], env, want)
                    if iteration >= args.warmups:
                        samples[shell].append(elapsed)
            medians = {shell: statistics.median(times) for shell, times in samples.items()}
            ratio = medians["entish"] / medians["bash"]
            report["results"][case] = {"samples_ms": samples, "median_ms": medians, "entish_over_bash": ratio}
            ranges = " / ".join(f"{min(samples[s]):.2f}..{max(samples[s]):.2f}" for s in ("entish", "bash"))
            print(f"{case:<12} {medians['entish']:12.3f} {medians['bash']:12.3f} {ratio:7.2f}x  {ranges}", flush=True)

        if args.json:
            args.json.write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
