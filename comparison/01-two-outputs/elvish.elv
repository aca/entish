# f writes "out" to the byte output and "val" to the value output
fn f { echo out; put val }
#--- | carries both; tr reads bytes, "val" is dropped silently
f | tr a-z A-Z
#--- each reads values AND byte lines
f | each {|x| echo got $x }
#--- ( ) merges both into one list; the order between them is not fixed
put [(f)]
#--- so a single-value capture fails at runtime: 2 values
var v = (f)
