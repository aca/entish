(require racket/port)
;; f writes "out" to stdout and returns "val"
(define (f) (displayln "out") "val")
;;--- "out" goes to stdout, not the pipe; "val" is displayed to tr's stdin
|> f | tr a-z A-Z
;;--- |> passes the return value
|> f |> string-upcase
;;--- a process stage passes a port; |>> turns it into a string
echo out |>> string-upcase
