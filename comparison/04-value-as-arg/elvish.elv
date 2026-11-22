# values as an external's arguments (e:echo, not the builtin)
#--- num, bool, list, map: all written as their repr
e:echo 0755 (+ 1 2) (+ 0.1 0.2) (num 1/3) $true [a b] [&k=v]
#--- spread
var xs = [a b]
e:echo $@xs
