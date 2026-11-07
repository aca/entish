# external output as a value: count the lines of "a\nb\n"
#--- length on a byte stream counts bytes
^printf 'a\nb\n' | length | print
#--- str length converts to a string first, trailing newline trimmed: 3
^printf 'a\nb\n' | str length | print
#--- lines
^printf 'a\nb\n' | lines | length | print
#--- JSON bytes without from json: 5 bytes, no error
^printf '[1,2]' | length | print
#--- with from json
^printf '[1,2]' | from json | length | print
#--- each on a byte stream gets the whole chunk, not lines
^printf 'a\nb\n' | each {|x| $"<($x)>" } | to json -r | print
