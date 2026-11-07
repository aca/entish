# Hack / JS 파이프 예시

entish의 `$_`가 가져온 방식: `|>` 오른쪽에 "앞에서 온 값" 자리를 기호로 직접 적는다.

- Hack (Meta, HHVM): 기호 `$$`
- JS (TC39 pipeline 제안, Stage 2): 기호 `%` (최종안은 아님, 아직 미정)

출처: [Hack 문서](https://docs.hhvm.com/hack/expressions-and-operators/pipe), [TC39 제안 README](https://github.com/tc39/proposal-pipeline-operator). JS 예시는 README에 있는 그대로이고, "중첩 호출" 예시만 설명용으로 만든 것.

## 왜 쓰나: 안쪽부터 읽는 중첩 호출

```js
// 안쪽에서 바깥으로 읽어야 함
Math.round(Math.sqrt(Math.abs(x)))

// 왼쪽에서 오른쪽으로 읽음
x |> Math.abs(%) |> Math.sqrt(%) |> Math.round(%)
```

## JS: `%`는 어디든 들어감

```js
value |> foo(%)          // 인자
value |> foo(1, %)       // 첫 인자가 아니어도 됨
value |> %.foo()         // 메서드
value |> % + 1           // 연산자
value |> [%, 0]          // 배열
value |> {foo: %}        // 객체
value |> `${%}`          // 템플릿 문자열
value |> new Foo(%)
value |> await %         // await도 됨
value |> (yield %)       // yield는 괄호 필요
```

실제 코드 예 (README):

```js
const json = pkgs[0] |> npa(%).escapedName |> await npmFetch.json(%, opts);
```

## Hack: 같은 방식, 기호만 `$$`

```hack
$x = vec[2,1,3] |> Vec\map($$, $a ==> $a * $a) |> Vec\sort($$);
// 같은 뜻: Vec\sort(Vec\map(vec[2,1,3], $a ==> $a * $a))  → vec[1,4,9]
```

## 규칙

| 규칙 | 예 |
|---|---|
| `\|>` 오른쪽에는 토픽이 반드시 있어야 함. 없으면 실행 전에 문법 에러 | `value \|> foo + 1` ✗ |
| 파이프 밖에서 토픽을 쓰면 문법 에러 | |
| 클로저 안에서도 씀. 바깥 값을 가리킴 | `value \|> Promise.resolve().then(() => console.log(%))` |
| 우선순위가 비슷한 것(`?:`, `=>`, `yield`, 대입)은 괄호로 감쌈 | `a \|> b ? % : c \|> %.d` ✗ → `a \|> (b ? % : c) \|> %.d` |
| `eval` 안의 토픽은 바깥 파이프와 무관 (런타임 에러) | `v \|> eval('% + 1')` ✗ |
| Hack: 오른쪽에 `await` 불가 | |

## F# 스타일과 비교

F# 스타일(`x |> f`가 곧 `f(x)`)은 TC39에서 두 번 올라갔다가 채택되지 않음.

```js
// F# 스타일: 함수 하나면 짧음
value |> foo
// 그 외에는 화살표 함수로 감싸야 함
value |> x => x + 1
value |> x => foo(1, x)

// Hack 스타일: 함수 하나에도 (%)를 붙이지만, 나머지는 그대로 씀
value |> foo(%)
value |> % + 1
value |> foo(1, %)
```

Hack 스타일을 고른 이유 (README): 함수 호출 말고 다른 식(메서드, 연산자, 템플릿, `await`)이 더 흔하고, `value |> foo + 1` 같은 실수가 런타임이 아니라 문법 에러로 잡힘.

## entish와 다른 점

| | JS / Hack | entish |
|---|---|---|
| 기호 | `%` / `$$` | `$_` |
| `\|>`의 뜻 | 값 하나를 오른쪽 식에 넣음 | 값 스트림을 연결. `$_`를 쓰는 stage는 값마다 한 번 실행 |
| 토픽이 없으면 | 문법 에러 | stage가 값 입력을 읽음 (`\|> each {..}`, `\|> count`) |
| 클로저 안 | 바깥 토픽을 볼 수 있음 | `{ }` 블록은 바깥 `$_`를 못 봄 (컴파일 에러). 블록의 항목은 `$it` |
| 값이 하나일 때 | | 같음: `[1 2] \|> len $_` = `len [1 2]` |

```
x |> Math.abs(%) |> Math.sqrt(%)       # JS
ls | lines |> mv $_ $_.bak             # entish: 줄마다 mv
...[1 2 3] |> $_ * 2 |> list           # entish: [2 4 6]
```

entish 규칙 전체는 DESIGN.md "Pipes"의 `$_` 절.
