# entish 설계 리뷰

0. [v2 리뷰: 설계 문제와 미결정 사항](#0-v2-리뷰-설계-문제와-미결정-사항) — 현재 기준
1. [Gotcha 리뷰](#1-gotcha-리뷰) — v1 기준 (기록)
2. [다른 셸과의 비교](#2-다른-셸과의-비교) — v1 기준 (기록)
3. [다음에 할 일](#3-다음에-할-일) — v1 기준 (기록)

---

## 0. v2 리뷰: 설계 문제와 미결정 사항

대상: fn 종류(value fn / command fn)를 없앤 뒤의 구현. 규칙은 DESIGN.md "Output and values", 버린 v1 설계와 그 이유는 같은 절의 "Why not kinds".

v2의 핵심 규칙:

- 모든 호출은 출력(stdout)과 값을 따로 냄. external, `print`, `each`의 값은 `$nil`.
- 연산자마다 채널 하나: `|` 출력→stdin, `|>` 값→마지막 인자, `$( )` 출력→str, `( )` 값.
- compile error는 소스만 보고 확실한 경우만. 런타임 raise는 추가하지 않음.

아래 예시는 모두 실제로 실행해서 확인한 것입니다.

### 0.1 설계 문제

| 등급 | 뜻 |
|---|---|
| **A** | 조용히 틀린 결과가 나옴 |
| **B** | 에러로 드러나지만 헷갈림 |
| **C** | 구현 한계 또는 빠진 기능 |

#### A-1. 조용한 `$nil` / `''`

```sh
fn v { 42 }; let s = $(v)                              # s = ''  ($( )는 출력만 받음)
let c = try { cat /etc/hostname } catch e { 'none' }   # 성공하면 c = $nil (cat의 값)
fn ll { ^ls }; let x = ll                              # x = $nil
if $x == '' { .. }                                     # $nil == '' → false, 조용히 else로
```

- `try { cmd }`로 출력을 받던 v1 습관이 그대로 틀림. `try { $(cmd) }`로 써야 함.
- 이후 `$nil`을 문자열, 산술, external 인자에 쓰면 에러가 나서 대부분 곧 드러남. `==` 비교만 조용히 통과함.
- 관련 미결정: 0.2의 D-1(void), D-3(lint).

#### A-2. 버려지는 값을 못 잡음

```sh
[1 2] |> map { $it * 2 } |> each { $it + 1 }   # 아무것도 안 함 (map을 써야 했음)
$xs |> myfn | cat                              # myfn의 값이 버려짐
add 1 2 | cat                                  # cat은 아무것도 못 받음
```

v1은 이 중 일부를 fn 종류로 잡았음.

#### A-3. 문자열 안의 `$( )`가 조용히 글자 그대로 나옴 (v1부터 있던 문제)

```sh
print "today: $(date +%F)"     # today: $(date +%F)
```

보간은 변수만 한다는 원칙은 맞음. 다만 bash 습관대로 쓰면 에러 없이 틀림. compile error에 hint를 다는 것을 제안: `let d = $(date +%F)` 후 `"$d"`.

#### A-4. 디버그 출력이 캡처에 섞임

```sh
fn f { print debug; 42 }
let s = $(g)          # g가 f를 부르면 'debug'가 s에 들어감
fn f { print debug >&2; 42 }   # 해결책: stderr로. 동작 확인함
```

bash와 같은 문제. `>&2`로 피할 수 있다는 점을 문서에 적어야 함.

#### B-1. wrapper fn은 실패하기 전에 부수효과가 먼저 일어남

```sh
^ls |> lines                   # compile error (external은 정적으로 앎)
fn ll { ^ls }; ll |> lines     # ls 출력이 다 찍힌 뒤 "lines needs a str, got nil"
```

에러에 `ll | lines`를 쓰라는 안내가 없음.

#### B-2. 조건에서 괄호가 의미를 바꿈

```sh
fn ok { ^true }
if ok { .. }       # 호출 → $nil은 성공 → true
if (ok) { .. }     # 식 → $nil은 bool이 아님 → 에러
```

문법으로만 판단한다는 원칙은 지킴. 하지만 `( )`를 단순한 묶음 괄호로 생각하면 함정. 조용히 틀리지는 않음.

#### B-3. builtin만 검사하는 비대칭

```sh
let y = print hi               # compile error: print has no value
fn p { print hi }; let x = p   # 통과, x = $nil
```

builtin에만 `Value` / `Void` 표시가 있음. builtin에 한정된 작은 종류 시스템이 남은 셈. wrapper로 감싸면 검사가 사라짐.

#### B-4. 섞인 파이프라인에서 출력이 어디로 가는지

```sh
cat f | grep x |> len     # grep의 출력은 터미널로, len은 $nil을 받음
a | b |> c | d            # b의 출력은 터미널로, d는 c의 출력을 받음
```

규칙은 일관됨(연산자마다 채널 하나). 다만 중간 stage의 출력이 터미널로 새어 출력이 섞일 수 있음.

#### B-5. `!`의 의미가 값에 따라 갈림

```sh
fn f { ^true }; !f      # 문장으로 쓰면 raise: '!f' failed: the command succeeded
fn g { $true }; !g      # false (그냥 부정)
```

`$nil`(명령 성공)이면 "실패해야 한다"는 단언으로, bool이면 부정으로 동작함. 규칙은 정해져 있지만 두 가지 의미가 섞여 있음.

#### B-6. 에러 메시지가 원인을 가리키지 못함

| 입력 | 메시지 | 실제 원인 |
|---|---|---|
| `[i: (len $x) + 1]` | `mixed list items and map entries` | map 값의 식에 괄호가 필요함: `[i: ((len $x) + 1)]` |
| `print (1/3)` | `1/3 has no value: capture its output with $( ) ...` | `/`가 있어서 경로(external)로 읽힘. `1 / 3`으로 써야 함 |
| `print [a: 1][a]` | `cannot use a map in a string` | 리터럴 바로 뒤에는 인덱스를 쓸 수 없음 |

#### B-7. `call`은 변수만 받음

```sh
fn twice f { { $f ($f $it) } }
call (twice $inc) 1        # error: call takes one fn variable
let g = twice $inc; $g 1   # 이렇게 써야 함
```

인자 없는 fn 값을 map에서 꺼내 바로 부를 방법도 없음. `let`으로 먼저 받아야 함.

#### C-1. 재귀 성능

- `$xs[1:]`이 매번 리스트를 복사함(`eval.go`의 `slice`). 그래서 재귀 `fold`의 메모리가 리스트 길이의 제곱으로 늘어남. 10,000개에 약 850MB. `l.Items[lo:hi:hi]`로 공유하면 해결됨.
- tail call 최적화가 없음(DESIGN은 보장한다고 적었지만 구현 안 됨). 1,000,000단계 재귀에서 Go 런타임 에러로 종료되는 것을 관찰함(원인은 확인 안 함).

#### C-2. 예제마다 같은 fn을 다시 만듦

`example/`의 lists, todo, wordfreq, functional이 모두 `fold`와 `join`을 직접 구현함. map에 key가 있는지 보는 방법이 없어서 `try { $m[$k] } catch e { default }`로 대신함. planned 목록의 `reduce`, `join_with`, `get`이 우선순위 후보.

### 0.2 미결정 사항

DESIGN.md "Open decisions"의 번호를 같이 적음.

| # | 질문 | 현재 | 대안과 비용 |
|---|---|---|---|
| D-1 (12) | "값 없음"을 `$nil`과 구분된 void로 둘지 | `$nil`로 합침 | void는 값으로 쓰는 순간 에러가 나서 A-1을 근본적으로 막음. 대신 런타임 검사와 개념이 하나 늘어남 |
| D-2 (13) | `eval` 소스를 `'literal'` / `$var`로 제한할지 | 제한함 | 제한하는 원래 이유(소스의 종류를 실행 전에 알기)는 사라졌음. 지금은 `eval "rm $f"` 같은 주입을 막는 역할만 남음 |
| D-3 (14) | "user's responsibility" 표의 실수에 lint를 붙일지 | 없음 | 추론을 최선 노력으로만 하고 동작은 절대 바꾸지 않는 경고. `explain`에 넣을 수 있음 |
| D-4 (15) | stdin을 읽는 규칙(인자를 빼면 stdin)의 범위 | 변환 builtin(`lines lines0 from_json`)만 | 모든 Data builtin으로 넓히면 `ls \| len`이 파일 개수가 아니라 글자 수를 셈 |
| D-5 | 조건에서 `( )` 안의 호출도 `$nil`을 성공으로 볼지 (B-2) | 식으로 취급 → 에러 | 성공으로 보면 괄호가 의미를 바꾸지 않지만, "식은 bool이어야 한다" 규칙에 예외가 생김 |
| D-6 | builtin의 `Value` / `Void` 검사를 wrapper fn까지 넓힐지 (B-3) | builtin만 | 넓히려면 추론이 필요함. 동작을 바꾸지 않는 lint로만 하는 것이 v2 원칙에 맞음 |
| D-7 | 에러 hint 보강 (B-1, B-6, A-3) | 없음 | 메시지만 바뀌어 동작 영향 없음. 제안: `$nil`이 값 builtin에 들어오면 출처를 알려주기, `try { external }`의 값을 쓰면 hint, `each` 본문이 식으로 끝나면 compile error |
| D-8 | `call`이 식을 받게 할지 (B-7) | `$name`만 | `call (expr)`을 허용하면 편함. `$f` 단독은 값이라는 규칙과는 충돌 없음 |
| D-9 | 슬라이스 공유, tail call (C-1) | 복사함, TCO 없음 | 슬라이스는 몇 줄 수정. TCO는 `execBlock` 구조 변경 필요 |

DESIGN.md Open decisions 1–11(숫자 표기, `ref`, `_` placeholder, 명령별 성공 코드, 터미널 처리, process substitution, `peach` 등)도 아직 미결정.

---

---

## 1. Gotcha 리뷰

> v1 설계 문서 단계의 리뷰 기록. 여기의 A-1(SIGPIPE), A-2(타입 혼합 비교), A-4, A-7 등은 v1에서 반영됐고, fn 종류에 관한 B-1, C-2는 v2에서 fn 종류를 없애면서 사라짐.

목표는 **놀랄 일을 최소화**하는 것입니다. 특히 에러 없이 조용히 틀린 결과가 나오는 경우를 가장 위험하게 봤습니다.

| 등급 | 뜻 |
|---|---|
| **A** | 조용히 틀리거나 자주 마주침. 고쳐야 함 |
| **B** | 헷갈리지만 에러로 드러남. 메시지와 lint로 해결 |
| **C** | 명세가 비어 있음. 정하기만 하면 됨 |
| **D** | 의도된 동작 또는 bash와 같음. 문서화만 |

### A. 고쳐야 할 것

#### A-1. `| head`가 raise됨

```sh
git log | head -5          # git이 SIGPIPE로 죽음 → pipefail → 에러
yes | head -1
git log --oneline | head -5 |> lines    # DESIGN.md 예제도 깨짐
```

- **원인:** pipefail이 켜져 있어서, 앞 명령이 SIGPIPE로 죽으면 실패로 처리됩니다.
- **해결:** 파이프라인 중간 명령이 SIGPIPE로 죽은 것은 성공으로 봅니다.

#### A-2. 타입이 다른 값의 비교가 조용히 false가 됨

```sh
let n = $(wc -l < f)       # "3" (str)
if $n == 3 { ... }         # str과 num 비교 → 항상 false
```

- **해결:** 타입이 다른 값끼리 `==`, `<` 등을 쓰면 에러입니다. `$nil`과의 비교만 허용합니다.

#### A-3. 숫자 리터럴의 원래 표기가 `let`을 거치면 사라짐

```sh
let ver = 1.10
git checkout v$ver         # v1.1 이 넘어감
```

- **해결:** 숫자 리터럴은 계산하기 전까지 원래 표기를 유지합니다. `let`에 담거나 list에 넣어도 유지됩니다.

#### A-4. `[ -f x ]`가 list가 되어 조용히 넘어감

```sh
[ -f x ] && print yes      # bash 습관. entish에서는 list [-f x]
```

- **증상:** REPL에서는 `[-f x]`를 출력하고, 스크립트에서는 아무 일도 하지 않습니다.
- **해결:**
  - 스크립트에서 쓰이지 않는 값 문장은 compile error입니다.
  - 첫 원소가 `-`로 시작하는 list라면 `test -f x`를 쓰라고 안내합니다.

#### A-5. list 안의 URL이 map으로 읽힘

```sh
[https://a.com https://b.com]   # key "https" ?
```

- **해결 (반영함):** list 항목과 map 값의 문자열은 반드시 quote합니다. bare word는 compile error입니다: `['https://a.com' 'https://b.com']`. map key는 bare 이름 뒤에 `:`와 공백이 올 때만 인정합니다.

#### A-6. 없는 환경변수가 정해지지 않았음

```sh
if $CI == 'true' { ... }   # 에러로 정하면 로컬 실행에서 터짐
rm -rf $DIR/               # 빈 문자열로 정하면 bash와 같은 사고
```

- **해결:** 없는 환경변수는 `$nil`입니다.
  - 비교는 그냥 동작합니다.
  - 외부 명령 인자로 넘기면 에러입니다: `DIR is not set`

#### A-7. `\\` 줄 사이의 주석이나 빈 줄이 문자열을 쪼갬

```sh
\\line a
# note                     # 여기서 문자열이 끝남
\\line b                   # 별개의 문자열 → 쓰이지 않고 버려짐
| cat > out.txt
```

- **해결:** A-4의 "쓰이지 않는 값 문장은 compile error" 규칙이 같이 잡아줍니다.

### B. 에러로 드러나는 것

| # | 함정 | 해결 |
|---|---|---|
| B-1 | 값 fn의 마지막 줄에 디버그용 `print`를 넣으면 명령 fn으로 바뀜. `let x = f`가 compile error가 됨 | 에러에 원인 줄을 표시: `f became a command fn because its last statement is print (line 5)`. `explain`도 분류를 보여줌 |
| B-2 | `print add 1 2`는 `add 1 2`라는 글자를 출력함 (괄호를 빠뜨림) | 인자로 쓴 bare word가 사용자 fn 이름이면 경고: `did you mean (add 1 2)?` |
| B-3 | `$dirs \|> each {\|d\| cd $d; make }`에서 `cd`가 누적됨. 람다도 fn이라 바깥 cwd를 바꾸기 때문 | 람다 안에서 `cd`를 쓰면 `in_dir`을 쓰라고 안내 |
| B-4 | `grep`이 못 찾으면(exit 1) raise됨. `let xs = grep foo f \|> lines`가 빈 list가 아니라 에러 | Open decision 6(명령별 성공 코드)의 우선순위를 올림 |
| B-5 | `else`를 다음 줄에 쓰면 새 문장으로 읽힘 | `} else {`를 같은 줄에 쓰도록 강제(Go와 같음). 에러에 hint 표시 |

### C. 명세가 비어 있는 것

| # | 빈칸 | 제안 |
|---|---|---|
| C-1 | 끌어올려진(hoisted) fn이 나중에 나오는 `let`을 보는가 | fn **이름**만 블록 전체에서 보임. 본문은 정의한 위치에서 보이는 변수만 봄 |
| C-2 | 마지막 문장이 `else` 없는 `if`이고, 그 분기가 명령인 fn | 명령 fn으로 분류. `else`가 없는 쪽은 출력 없음으로 봄 |
| C-3 | `{ \|x\| ...}`처럼 `{`와 `\|` 사이에 공백이 있을 때 | **반영함:** 공백이 있어도 람다로 인정. `\|`로 시작하는 문장은 없으므로 모호하지 않음 (Ruby와 같은 표기) |
| C-4 | `lines ""` | `[]` |
| C-5 | `print`에 list나 map을 주면 | entish 리터럴 문법으로 출력 |
| C-6 | map key의 타입 | 항상 `str`. `[1: a]`의 key는 `"1"`. **반영함:** `$m[$n]`에 num을 주면 raise (암묵 변환 없음) |
| C-7 | `\|` 왼쪽에 올 수 있는 값 | **반영함:** 값은 올 수 없음 (compile error). `print $x \| cmd`로 씀 |

### D. 의도된 동작

| 동작 | 비고 |
|---|---|
| `$name_backup`은 변수 이름이 `name_backup` | `${name}_backup` (bash와 같음) |
| `"$file[1]"`은 인덱스로 읽힘 | zsh와 같음 |
| glob이 매칭되지 않으면 에러 | zsh와 같음 |
| `ls [ab]*`는 list로 읽힘 | `./[ab]*`로 씀 |
| `find -exec rm '{}' ';'` | 또는 `\|> lines \|> each` |
| `len $xs + 1`은 인자 3개 | `(len $xs) + 1` |

---

## 2. 다른 셸과의 비교

> 아래 비교는 각 셸에 대해 제가 알고 있는 내용 기준입니다. 특히 nushell은 버전마다 바뀌는 게 많아서, 세부 동작은 최신 문서로 확인해야 합니다.

### 한눈에 보기

| 관점 | entish | elvish | nushell | rash |
|---|---|---|---|---|
| 언어 | 자체 언어, 키워드 6개 | 자체 언어 | 자체 언어 | Racket 전체 |
| fn인지 외부 명령인지 구분 | 실행 전 정적 해석 + `explain` | 이름 해석, `e:`로 외부 강제 | 사용자 명령 우선, `^`로 외부 강제 | line 모드 / s-식 모드 |
| 값과 바이트 | `\|>`와 `\|`로 **연산자에서 분리** | 한 `\|`에 값 채널과 바이트 채널이 함께 | 한 `\|`에 구조화 데이터와 문자열이 섞임 | `\|>`, `\|>>` |
| 산술 | 중위 `$a + 1` | **전위** `+ $a 1` | 중위 | 전위 (Racket) |
| 숫자 | 정확한 유리수 | 정확한 유리수 + 실수 | int, float | Racket 수치 전체 |
| 외부 명령 실패 | raise + 조건 위치 규칙 | 예외 | 버전마다 다름 | 예외 |
| 명령 사이 `&&` `\|\|` | 있음 | 없음 | 제한적 | 있음 |
| 가변성 | 없음 (불변) | `var` / `set` | `mut` | 있음 |
| 반복문 | 없음 (`each` / `map`) | `for`, `while` | `for`, `while`, `loop` | Racket |
| 스트리밍 | **없음** (`\|>`는 다 모은 뒤 넘김) | 있음 | 있음 | 있음 |
| 여러 줄 문자열 | Zig식 `\\`, escape 없음 | 일반 문자열 | raw string `r#'..'#` | Racket |
| 문자열 보간 | 변수만 | 없음 (이어붙이기) | `$"..(expr).."` (코드 허용) | Racket |
| fn 시그니처 / 플래그 | 없음 | 있음 (옵션) | **타입 + 플래그를 실행 전 검사** | Racket |
| 환경 변경 범위 | fn은 항상 호출한 쪽을 바꿈 | — | `def --env`일 때만 바꿈 | parameterize |
| job control | 없음 | 없음 | 제한적 | 없음 |
| 모듈 | 없음 | 있음 | 있음 | Racket |
| 배포 | Go 단일 바이너리 | Go 단일 바이너리 | Rust 단일 바이너리 | Racket 런타임 (약 160MB) |
| 성숙도 | **설계 문서뿐** | 수년간 사용됨 | 크고 활발함 | 연구용에 가까움 |

### entish가 더 나은 점

1. **호출 대상과 결과 종류를 실행 전에 확정합니다.**
   - 각 호출이 fn인지 외부 명령인지, 값을 내는지 출력을 내는지를 컴파일러가 압니다.
   - 그래서 다음 실수를 실행 전에 잡습니다.
     - `let x = ls` (값이 없는 명령을 값으로 씀)
     - list를 외부 명령에 넘김
     - 분기마다 값과 명령이 섞인 fn
2. **wrapper가 원래 명령과 똑같이 동작합니다.** 명령 fn 규칙 덕분에 `fn ls ...`를 만들어도 `ls |> lines`, `cat x | grep y`가 그대로 됩니다.
3. **값과 바이트의 경계가 연산자에 드러납니다.** `|`는 바이트, `|>`는 값입니다. 지금 무엇이 흐르는지 코드만 보고 알 수 있습니다.
4. **bash에서 익숙한 명령 문법은 그대로 두고, 위험한 부분만 고쳤습니다.**
   - `&&`, `||`, 리다이렉션, glob을 그대로 씁니다.
   - `set -e`의 함정과 값의 재해석(word splitting, glob)은 없앴습니다.
5. **quote 부담이 가장 적습니다.**
   - 여러 줄 문자열에 escape가 없습니다.
   - 보간은 변수만이라 따옴표가 중첩되지 않습니다.
   - lexer가 상태 몇 개짜리 단순한 구조로 끝나서 문법 하이라이팅도 쉽습니다.
6. **배울 것이 적습니다.**
   - 키워드 6개, 문법 하나입니다.
   - REPL과 스크립트가 같은 규칙입니다.
   - 줄 이어쓰기도 앞을 내다보지 않습니다.
7. **elvish 대비:** 산술이 중위식입니다.
8. **rash 대비:** Go 단일 바이너리이고, 줄 맨 앞의 `(`로 모드가 바뀌는 문제가 없습니다.

### entish의 약점

| # | 약점 | 더 나은 쪽 | 설계로 줄일 수 있나 |
|---|---|---|---|
| W-1 | 아무것도 구현되지 않음. 문서, 자동완성, 플러그인, 사용자층이 없음 | elvish, nushell | 구현으로만 해결 |
| W-2 | 스트리밍이 없음. `find / \|> lines \|> each`는 전부 받은 뒤 시작 | elvish, nushell | **가능** (Open decision 4) |
| W-3 | 구조화 데이터 기능이 빈약함 (`from_json` 정도) | nushell (표, csv/yaml/toml, 질의) | builtin 추가 |
| W-4 | 불변이고 반복문이 없어서 셸 사용자에게 진입장벽 | elvish, nushell | `ref` 탈출구 (Open decision 2) |
| W-5 | 값 fn과 명령 fn의 구분이 암묵적. 토큰 모양 규칙이 여럿 (`[`, `{\|`, `$xs [0]`) | — | 에러 메시지와 lint |
| W-6 | fn에 타입 시그니처와 플래그 파싱이 없음. 인자의 오타가 문자열로 들어감 | nushell | **가능** |
| W-7 | fn이 `cd`, `export`로 항상 호출한 쪽 상태를 바꿈 | nushell (`def --env`) | **가능** |
| W-8 | 모듈, regex, 실수, job control이 없음 | elvish, nushell | 나중에 추가 |
| W-9 | 기존 언어의 라이브러리와 매크로를 못 씀 | rash, xonsh | 구조상 불가 (새 언어를 만드는 비용) |
| W-10 | 미결 함정: SIGPIPE, grep exit 1, 타입이 다른 값의 비교 | — | **가능** (1장 A) |

### 정리

entish가 겨냥하는 자리는 이렇습니다.

> 명령 문법은 bash처럼 익숙하게, 언어 핵심은 작고 엄격한 함수형으로, 실수는 실행 전에 잡는다.

- 이 자리에서는 elvish, nushell, rash보다 일관되고 놀랄 일이 적습니다.
- 반대로 **대용량 스트리밍, 구조화 데이터, 생태계** 쪽에서는 nushell을 이기기 어렵습니다.

---

## 3. 다음에 할 일

| 순서 | 항목 | 이유 |
|---|---|---|
| 1 | 1장 A-1 ~ A-7 반영 | 조용히 틀리는 함정. 특히 A-1(SIGPIPE)과 A-2(타입 혼합 비교)는 매일 마주침 |
| 2 | 1장 C-1 ~ C-7 결정 | 정하기만 하면 되는 작은 결정들 |
| 3 | W-7: 환경 변경을 명시적으로 | 1장 B-3 함정도 함께 사라짐 |
| 4 | W-6: fn 시그니처와 플래그 | 오타가 문자열로 들어가는 문제를 막음 |
| 5 | W-2: `\|>` 스트리밍 | 대용량 데이터 처리 |
| 6 | 1장 B의 에러 메시지와 lint | 구현할 때 함께 |
