ys=()
for x in "${xs[@]}"; do
    ys+=( "$(( (x * 3 + 7) % 97 ))" )
done
