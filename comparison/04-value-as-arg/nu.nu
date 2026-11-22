# values as an external's arguments
#--- num, float, bool
^echo 0755 (1 + 2) (0.1 + 0.2) (1 / 3) true
#--- list: error
^echo [a b]
#--- spread
^echo ...[a b]
#--- record: error
let m = {k: v}
^echo $m
