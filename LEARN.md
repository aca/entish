# Learn entish in Y minutes

entish is a shell where every command emits two streams: **output** (bytes, as in any shell) and **values** (numbers, strings, lists, maps). You pick which one the next command gets with the operator: `|` passes output, `|>` passes values. Nothing converts between them implicitly.

Run a file with `entish file.ent`, or one line with `entish -c '...'`.

```sh
# ---- commands: like any shell
echo hello                         # run an external
ls / | grep -c bin                 # | connects stdout to stdin
echo saved > /tmp/out.txt          # > >> < 2> 2>&1
let today = $(date +%F)            # $( ) captures output as a str

# ---- values
let n = 0.1 + 0.2                  # nums are exact: this is 0.3
let s = 'raw $n'                   # 'raw', no interpolation
let t = "n is $n"                  # "..." interpolates variables, never code
let xs = [1 2 3]                   # list; items are values, so quote words
let m = [name: 'ann'  age: 31]     # map
print $xs[0] $xs[1:] $m[name] (len $xs)    # => 1 [2 3] ann 3
print $HOME                        # $Upper is an env var, $lower a variable
export GREETING=hi                 # set one

# ---- fns
fn greet who { "hi $who" }         # the body's values are the fn's values
print (greet bob)                  # ( ) is a call's value, exactly one => hi bob
let double = { $it * 2 }           # a lambda; $it is its one param
let add = {|a b| $a + $b }
print ($double 21) ($add 1 2)      # => 42 3
...[1 2 3] |> each { echo hi }     # no |params| and no $it: args are ignored

# ---- two streams: every call emits output (bytes) and values
fn f { echo progress; 'result' }   # echo writes output, 'result' is a value
f | tr a-z A-Z                     # | takes the output, drops the value => PROGRESS
f |> each { print "got $it" }      # |> takes the values; output goes on => progress, got result
let r = f                          # let takes the value: 'result'
let o = $(f)                       # $( ) takes the output: 'progress'

# ---- value streams: values flow one at a time, like lines through a pipe
seq 1 5 | lines |> count |> print                    # bytes -> values => 5
seq 1 5 | lines |> each { (num $it) * 10 } |> print  # each maps... => 10 20 30 40 50
...$xs |> each { if $it != 2 { $it } } |> print      # ...and filters => 1 3
let ys = seq 1 3 | lines |> list                     # collect values into a list
echo ...$ys                                          # spread a list into args => 1 2 3
fn evens n { if $n > 0 { evens ($n - 2); $n } }      # every statement emits
print ...(evens 6 |> list)                           # => 2 4 6
echo '{"a": 1}' | from_json |> each { $it[a] } |> print   # => 1
[a: 1] |> to_json                                    # values -> JSON bytes

# ---- conditions: a command is tested by its status, ( ) by its value
if test -d / { print dir }
fn big x { $x > 9 }
if (big 10) && !test -f /nope { print big }          # `if big 10` is a compile error
false || print 'fallback'
let size = if (len $xs) > 2 { 'many' } else { 'few' }
match $m {
    [name: who] => print "named $who"
    _           => print other
}

# ---- errors: a failing command raises; try catches
let code = try { false } catch e { $e[code] }
print "false exited $code"                           # => false exited 1
match (status { grep -q x /etc/hostname }) {         # an exit code as a value
    0 => print found
    1 => print 'not found'
    c => fail "grep failed: $c"
}
```

Mistakes the compiler catches before anything runs:

```sh
let x = ls          # ls has no value: capture its output with $( ) or read it with | lines
1 | cat             # a value cannot be piped into |; print it: $x |> print | cat
ls |> count         # ls has no value; make its output values: ls | lines |> count
$xs |> len          # len takes its data as an arg, not values: len $x
print "now $(date)" # a string interpolates variables, never code; bind it first: let s = $( ... ), ...
```

More: STREAMS.md is the model, `example/` has longer programs, `comparison/` runs the same cases in elvish, nushell and rash.
