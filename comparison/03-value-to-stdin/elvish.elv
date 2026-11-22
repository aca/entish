# a value on the left of |
#--- values sent to an external are dropped: nothing printed
put [a b] | cat
#--- also strings
put a b | cat
#--- echo writes bytes
echo [a b] | cat
#--- to-lines
put a b | to-lines | cat
