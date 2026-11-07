# external output as a value: count the lines of "a\nb\n"
#--- count reads byte input as lines, implicitly
e:printf 'a\nb\n' | count
#--- the same with an explicit from-lines
e:printf 'a\nb\n' | from-lines | count
#--- ( ) splits bytes into lines
put [(e:printf 'a\nb\n')]
#--- slurp gives one string: 4 codepoints
count (e:printf 'a\nb\n' | slurp)
#--- JSON is one value: count of values is 1, not 2
e:printf '[1,2]' | from-json | count
