(require racket/port)
;; external output as a value: count the lines of "a\nb\n"
;;--- |>> gives the whole output as a string: 4
printf "a\nb\n" |>> string-length
;;--- |> gives the port
printf "a\nb\n" |> port->lines |> length
;;--- a function that expects a list gets a port: runtime error
printf "a\nb\n" |> length
