# trailing newlines
#--- capture trims one trailing newline: "a\n\n" is 3
let s = (^printf 'a\n\n\n')
$s | str length | print
#--- lines
^printf 'a\n\n\n' | lines | to json -r | print
#--- a string to an external: no newline
"a" | ^od -c
