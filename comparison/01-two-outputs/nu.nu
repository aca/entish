# f runs an external, then returns "val"
def f [] { ^echo out; "val" }
def g [] { ^echo out }
def h [] { ^echo out; ^echo out2 }
#--- "out" goes to the terminal, not the pipe; "val" is rendered to tr's stdin
f | ^tr a-z A-Z
#--- an external's stdout reaches the pipe only when it is the last statement
g | ^tr a-z A-Z
#--- so adding a line after it moves "out" from the pipe to the terminal
h | ^tr a-z A-Z
#--- let takes the value; "out" goes to the terminal
let v = f
print $"v: ($v)"
