;; values as an external's arguments: displayed
;;--- num literal 0755 is read as 755; exact rationals stay 1/3
echo 0755 (+ 1 2) (+ 0.1 0.2) (/ 1 3) #t
;;--- a list variable is spread
(define xs (list "a" "b"))
echo $xs
;;--- hash
echo (hash 'k "v")
