# trailing newlines
#--- ( ) splits into lines; the final newline ends the last line
put [(e:printf 'a\n\n\n')]
#--- slurp keeps everything
e:printf 'a\n\n\n' | slurp
#--- echo adds a newline
echo a | e:od -c
