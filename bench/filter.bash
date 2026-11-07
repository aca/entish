ys=()
for x in "${xs[@]}"; do
    if (( x % 2 == 0 )); then
        ys+=( "$x" )
    fi
done
