;; trailing newlines
;;--- |>> keeps everything
printf "a\n\n\n" |>> values
;;--- a string to a process: no newline added
|> values "a" | od -c
