;; a value on the left of |: displayed (Racket's display) to the process's stdin
;;--- list
|> list "a" "b" | cat
;;--- hash
|> hash 'k "v" | cat
;;--- bool
|> values #t | cat
;;--- exact rational
|> / 1 3 | cat
;;--- string: no newline added
|> values "a" | od -c
