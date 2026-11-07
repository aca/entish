#!/usr/bin/env bash
# Runs each snippet of each case with its shell and prints the source and its output.
#
#   ./run.sh                  all cases, all shells
#   ./run.sh 03               cases whose dir starts with 03
#   ./run.sh 03 nu            only nu
#
# A case is a dir with one file per shell: entish.ent elvish.elv nu.nu rash.rkt.
# Snippets are separated by a line starting with `#---` (`;;---` in rash) and run
# in separate processes, so a compile error stops only its own snippet. The part
# before the first separator is a prelude prepended to every snippet.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

(cd "$here/.." && go build -o "$tmp/entish" .)

run() { # shell file
	case $1 in
	entish) "$tmp/entish" "$2" ;;
	elvish) elvish -norc "$2" ;;
	nu) nu -n "$2" ;;
	rash)
		# rash needs the file to start with #lang; unbuffered so racket's own
		# output and its subprocesses' output appear in order.
		{
			echo '#lang rash'
			echo "(file-stream-buffer-mode (current-output-port) 'none)"
			cat "$2"
		} >"$tmp/rash/s.rkt"
		racket "$tmp/rash/s.rkt"
		;;
	esac
}

mkdir "$tmp/rash"
for dir in "$here"/${1:-}*/; do
	echo "######## $(basename "$dir")"
	for f in "$dir"*; do
		case $f in
		*.ent) sh=entish ;; *.elv) sh=elvish ;; *.nu) sh=nu ;; *.rkt) sh=rash ;; *) continue ;;
		esac
		[[ -n ${2:-} && $2 != "$sh" ]] && continue
		echo "==== $sh"
		awk -v out="$tmp/snip" '
			/^(#|;;)---/ { n++; next }
			n == 0 { pre = pre $0 "\n"; next }
			{ s[n] = s[n] $0 "\n" }
			END { for (i = 1; i <= n; i++) { f = out "." i; printf "%s", pre s[i] > f; close(f) } }
		' "$f"
		i=1
		while [[ -f $tmp/snip.$i ]]; do
			# show the separator's comment and the snippet, not the prelude
			awk -v i="$i" '
				/^(#|;;)---/ { if (++n == i) { sub(/^(#|;;)--- ?/, ""); print "  -- " $0 }; next }
				n == i && $0 != "" { print "  | " $0 }
			' "$f"
			run "$sh" "$tmp/snip.$i" 2>&1 | awk '{ print "  > " $0 }' || true
			rm "$tmp/snip.$i"
			i=$((i + 1))
		done
	done
done
