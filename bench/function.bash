# Return through a variable: command substitution would create a subshell.
bench_calc() { reply=$(( ($1 * 3 + 7) % 97 )); }
ys=()
for x in "${xs[@]}"; do
    bench_calc "$x"
    ys+=( "$reply" )
done
