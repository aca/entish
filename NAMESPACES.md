# 이름 공간

entish의 이름은 두 공간 중 하나에 속한다. 어느 공간인지는 위치와 `$`로 정해지고, 실행 전에 체커가 모두 확정한다.

| 공간 | 구성원 | 쓰는 자리 |
|---|---|---|
| 호출 | keyword, fn, builtin, external | 문장 머리, `\|>` 오른쪽 |
| 값 | 변수, fn, builtin, env var, 내장 값 | `$name` |

fn은 양쪽에 다 있다. `fn f`는 호출 이름 `f`와 값 `$f`를 함께 만들고, 둘은 같은 함수 객체다. 변수는 호출 공간에 들어가지 않으므로, 변수가 명령을 가리는 일은 없다.

```sh
fn f { 1 }
print $f     # <fn f>  — 같은 fn
let f = 2
print (f)    # 1  — 호출 공간: let은 명령을 가리지 않음
print $f     # 2  — 값 공간: let이 $f를 가림
```

## 호출 공간

### 해석 순서

| 머리 | 뜻 |
|---|---|
| keyword (`let fn if else try catch match`) | keyword 형식 |
| `^name` | PATH의 external. fn을 건너뜀 |
| `/`가 들어간 단어 (`./x`, `/bin/x`) | 경로로 지정한 external |
| fn 이름 | 사용자 fn |
| builtin 이름 | builtin |
| 그 밖의 단어 | PATH의 external |

- 우선순위: keyword > fn > builtin > PATH.
- 해석은 정적이다. 실행 중에 이름을 다시 찾지 않는다.
- fn 이름은 snake_case: `[a-z_][a-z0-9_]*`. `fn Foo` → 컴파일 에러.
- `-`가 들어간 external(`git-lfs`)은 fn이 가릴 수 없다.

### fn의 유효 범위

- fn 이름은 정의한 블록 **전체**에서 보인다(hoisting). 정의보다 앞줄에서도 호출할 수 있다.
- 블록 안에서 정의한 fn은 그 블록 밖에서 보이지 않는다.

  ```sh
  fn outer { fn inner { 7 }; inner }
  outer    # 7
  inner    # inner: command not found  (external로 해석됨)
  ```

- 끌어올려지는 것은 이름뿐이다. 본문은 **정의한 위치**에서 보이는 변수만 본다(REVIEW.md C-1).

  ```sh
  fn g { $y }; let y = 1    # 컴파일 에러: undefined variable $y
  ```

### 가리기 (shadowing)

```sh
fn ls ...args { ^ls --color=auto ...$args }   # fn이 external ls를 가림; ^ls로 원본 호출
fn map x { $x }                               # fn이 builtin map을 가림
```

- fn은 builtin과 external을 가릴 수 있다.
- 가려진 external은 `^name`으로 부른다. 가려진 builtin을 다시 부를 방법은 없다.
- fn은 entish 안에만 있다. `sudo ls`, `xargs`, `find -exec`에서 실행되는 것은 external이다.

## 값 공간

`$` 뒤 첫 글자로 종류가 정해진다. 조회 없이 정해지는 고정 규칙이다.

| 형태 | 뜻 | 없을 때 |
|---|---|---|
| `$name` (`[a-z_]`로 시작) | 언어 변수 | 컴파일 에러 |
| `$NAME` (`[A-Z]`로 시작) | 현재 scope frame의 env var | `$nil` |
| `$env:name` | env var. 소문자 이름용 (`$env:http_proxy`) | `$nil` |

### 언어 변수

- 만드는 방법: `let`, `fn` 정의, fn 파라미터, 람다 파라미터(`{|x| ...}`), `catch` 바인딩, `$it`.
- 렉시컬하고 불변이다. 같은 이름으로 `let`을 다시 하면 새 바인딩이 이전 것을 가린다.
- 클로저는 값을 캡처한다.

  ```sh
  let x = 1; fn g { $x }; let x = 2
  g    # 1
  ```

- `fn f`의 `$f`는 `let`처럼 **정의한 줄부터** 보인다. 호출 이름 `f`만 블록 전체로 끌어올려진다. fn 본문 안에서는 `$f`가 자기 자신이라 `$f`로도 재귀할 수 있다.

  ```sh
  fn fact n { if $n <= 1 { 1 } else { $n * ($fact ($n - 1)) } }
  fn a { $b }; fn b { 1 }    # 컴파일 에러: undefined variable $b  (b로 부르면 됨)
  ```

- builtin도 값이다: `$map`, `$lines`, `$len`. 같은 이름의 변수가 스코프에 있으면 변수가 이긴다(`let lines = ..` 뒤의 `$lines`는 그 변수). `call`은 체커가 처리하는 형식이라 값이 없다.
- 대문자로 시작하는 이름은 env var 자리다. `let MAX = 3` → 컴파일 에러: `export MAX=3`을 쓴다.
- 예약어: `$true $false $nil $args $it $_`. `let it = ..`, `let true = ..` → 컴파일 에러.
  - `$it`: `$it`을 쓰는 블록은 파라미터 하나짜리 람다다.
  - `$_`: REPL의 마지막 결과. 스크립트에서는 컴파일 에러.

### env var

- 쓰기는 `export NAME=value`로만 한다. `let`으로는 못 쓴다.
- 값은 항상 `str`이다.
- 모든 env var는 external에 전달된다.

## 경계

두 공간 사이에 자동 변환은 없다. 건너가려면 문법이 따로 필요하다.

| 하고 싶은 것 | 쓰는 법 |
|---|---|
| 변수에 든 fn 값을 호출 | `$f x`, `x \|> $f`, `cmd \| $f` (stdin이 파이프), `call $f` (인자 없음). `$f` 자리에 `{ }`, `( )`도 같은 뜻 |
| fn 값 자체를 넘김 | `$f` (bare `$f`는 절대 호출이 아니다) |
| fn, builtin을 값으로 넘김 | `map $double`, `map $lines` |
| 파이프라인을 값으로 만듦 | 람다: `let count = {\|x\| $x \|> lines \|> len }` |

### 자주 막히는 곳

**`$f`는 정의한 줄 뒤에서만 쓸 수 있다.**

```sh
let g = $vifm                 # 에러: undefined variable $vifm
fn vifm d { cd $(^vifm $d --choose-dir -) }
let f = $vifm                 # OK
$f /tmp
```

**`$f`는 호출이 아니다.** `$f`는 fn 값이다. 인자 없이 부르려면 `call $f`.

```sh
{ $f; print done }            # 컴파일 에러: unused value: $f is not called; to call a fn value: call $f
```

**`let`은 명령을 만들지 않는다.**

```sh
let vifm = {|d| cd $(^vifm $d --choose-dir -) }
$vifm /tmp                    # 람다 호출
vifm /tmp                     # external vifm 실행
```

**fn을 담은 파라미터는 `$`를 붙여서 호출한다.**

```sh
fn apply g x { $g $x }        # OK
fn apply g x { g $x }         # g가 external로 해석됨 → 실행 중 'g: command not found'
```

**fn 파라미터는 named fn 문법으로 선언한다.** `{|d| ...}`는 람다 문법이다.

```sh
fn vifm d { ... }             # OK
fn vifm { |d| ... }           # 파라미터 없는 fn; 본문의 $d는 undefined variable
```

## 왜 나눴나

- xonsh에서는 `ls = 1` 하나로 `ls` 명령이 망가진다. entish에서는 변수가 명령을 가릴 수 없다.
- 문장 머리는 항상 호출 대상이고 변수에는 항상 `$`가 붙는다. 그래서 체커가 실행 전에 모든 호출을 fn, builtin, external 중 하나로 확정할 수 있다. 인자 개수 검사, fn Kind 계산, 순수성 추론이 모두 이 정적 해석에 기댄다.
- `$`가 붙은 것은 항상 값이고, fn은 값 공간에도 있으므로 함수를 데이터처럼 넘길 수 있다.
- bare `$f`가 항상 값이므로 `if $c { $a } else { $b }`는 `$a`에 무엇이 들어 있든 `$a`를 돌려준다.

## 미결 / 구현과 다른 점

DESIGN.md에는 적혀 있지만 현재 구현에 없는 것과, 아직 정하지 않은 것.

| 항목 | DESIGN.md | 현재 동작 |
|---|---|---|
| builtin/external을 가리는 fn | 경고 (본문이 `^name`을 부르면 경고 없음) | 경고 없음 |
| 자기 이름을 `^` 없이 부르는 fn | 경고: `did you mean ^ls?` | 경고 없음 |
| `explain` | 해석 결과를 보여줌 | 없음 (`explain: command not found`) |
| `command name args...` | fn을 건너뛰고 external 실행, 이름에 값 허용 | 없음 |
| 같은 블록에 같은 이름의 fn 두 번 | 정해지지 않음 | 나중 것이 조용히 이김 |
| fn 파라미터를 `$` 없이 머리에 씀 (`g $x`) | 정해지지 않음 | external로 해석, 실행 중 실패 |

## 결정: fn은 값이다

함수형 언어에서 이름 붙인 함수는 값이다(`map f`). 그래서 `fn f`는 `$f`도 만든다. 검토한 안:

1. **`$name`이 변수를 못 찾으면 fn을 찾는다.** 기각. 두 공간이 섞이고, 대상이 실행 순서가 아닌 조회 실패 여부로 정해진다.
2. **전용 문법 (`$proc:f`, elvish `$f~`).** 기각. 가장 흔한 경우(`map $f`)에 문법이 하나 더 붙는다.
3. **`fn f`가 정의한 줄에서 `$f`를 묶는다 (채택).** `let f = ...`과 같은 렉시컬 규칙이라 새 규칙이 없다. 뒤에 `let f`가 오면 그 줄부터 `$f`만 가려지고, 앞에 있던 `let f`는 `fn f`가 가린다. 호출 이름 `f`는 영향받지 않는다.

`fn`을 `proc`으로 바꾸는 안도 검토했다. "이름 붙은 것은 값이 아니다"라는 전제에서는 맞는 이름이지만, fn이 값이 되면서 `fn`이 정확한 이름이 됐다.

블록을 2급(호출 인자로만 쓰고 값이 아님)으로 만드는 안도 기각했다. `$`가 절대 호출되지 않는다는 단순함은 얻지만, 함수 리턴·합성·저장이 막혀 함수형 언어가 아니게 된다. 블록 파라미터를 받는 fn 안에서는 Kind를 여전히 정적으로 알 수 없어서 얻는 것도 기대보다 작다.
