# a value on the left of |
#--- a list is rendered as a table, box-drawing characters included
[a b] | ^cat
#--- a record too
{k: v} | ^cat
#--- an int sends nothing, no error
1 | ^od -c
#--- a bool sends nothing, no error
true | ^od -c
#--- a string is sent without a newline
"a" | ^od -c
#--- explicit
[a b] | to text | ^od -c
