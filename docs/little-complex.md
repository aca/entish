# 조금 복잡한 예제: 비트코인 급등일 찾기

업비트 공개 API(인증 불필요)로 비트코인 일봉을 받아, 하루에 10% 이상 오른 날을 최근 순으로 5개 찾는다.
각 블록은 그대로 파일로 저장해 `entish 파일.ent`로 실행할 수 있다. 1초 안에 끝난다.

## 1. 페이지를 넘기며 급등일 5개

업비트는 한 번에 200일치를 준다. 더 과거는 `to=<가장 오래된 날짜>`로 다시 요청한다.

```
fn days to {
    echo "GET to=$to"
    let cs = (curl -s "https://api.upbit.com/v1/candles/days?market=KRW-BTC&count=200&to=$to" | from_json)
    ...$cs
    if (len $cs) == 200 { days $cs[199][candle_date_time_utc] }
}

days '' |% {
    let pct = (round 1 ($it[change_rate] * 100))
    if $pct >= 10 { print $it[candle_date_time_kst] "+$pct%" }
} | head -n 5
```

```
GET to=
GET to=2026-04-28T00:00:00
GET to=2025-10-10T00:00:00
GET to=2025-03-24T00:00:00
GET to=2024-09-05T00:00:00
2026-02-06T09:00:00 +13%
2025-03-02T09:00:00 +11.1%
2024-11-11T09:00:00 +11.7%
2024-08-08T09:00:00 +10.2%
2024-02-28T09:00:00 +11.5%
GET to=2024-02-18T00:00:00
GET to=2023-08-02T00:00:00
```

(출력을 파이프로 받으면 `head`가 버퍼링해서 결과가 끝에 몰린다. 터미널에서는 페이지마다 바로 찍힌다.)

- **평범한 재귀가 스트림이다.** fn은 반환하지 않고 문장마다 값을 내보낸다. `...$cs`는 Python의 `yield from`이고, 재귀 호출은 다음 페이지를 이어서 내보낸다.
- **모든 stage가 동시에 돈다.** 다음 페이지를 받는 동안 앞 페이지는 이미 필터링되고 있다.
- **필요한 만큼만 요청한다.** `head`가 5줄을 받고 끝나면 나머지 stage가 종료된다. 2017년까지 17페이지가 있지만 7번만 요청한다. 동시에 돌기 때문에 `head`가 끝나기 전에 이미 나간 요청이 한두 개 있다.
- **로그는 데이터에 섞이지 않는다.** `echo` 로그는 바이트라서 `|%`로 가지 않고 터미널로 간다.

## 2. 로그가 결과에 섞이는가: elvish와 비교

최근 30일치를 받아 개수를 센다.

```
fn candles n {
    echo "GET $n days"
    ...(curl -s "https://api.upbit.com/v1/candles/days?market=KRW-BTC&count=$n" | from_json)
}
candles 30 |> count |> print
```

```
GET 30 days
30
```

elvish는 `|` 하나로 바이트와 값을 함께 넘기고, `count`는 둘 다 센다. 에러 없이 틀린 답이 나온다.

```elvish
fn candles {|n|
    echo "GET "$n" days"
    all (curl -s "https://api.upbit.com/v1/candles/days?market=KRW-BTC&count="$n | from-json)
}
candles 30 | count
```

```
▶ (num 31)
```

로그 한 줄을 추가하는 것만으로 이 fn을 쓰는 모든 `count`, `each`의 결과가 바뀐다.

## 3. 함수형 언어와 비교: Elixir

재귀로 쓰면 함수는 마지막에 한 번 반환하므로 모든 페이지를 받은 뒤에야 필터가 돈다. `Enum.take(5)`가 있어도 17페이지를 다 요청한다.

```elixir
def days(to) do
  IO.puts("GET to=#{to}")
  cs = Req.get!(url(to)).body
  if length(cs) == 200, do: cs ++ days(List.last(cs)["candle_date_time_utc"]), else: cs
end
```

필요한 만큼만 요청하려면 재귀를 버리고 `Stream.unfold`로 다시 써야 한다.

```elixir
Stream.unfold("", fn
  nil -> nil
  to ->
    IO.puts("GET to=#{to}")
    cs = Req.get!(url(to)).body
    {cs, if(length(cs) == 200, do: List.last(cs)["candle_date_time_utc"])}
end)
|> Stream.flat_map(& &1)
|> Stream.filter(&(&1["change_rate"] >= 0.1))
|> Enum.take(5)
```

| | 재귀로 스트림 | 방식 |
|---|---|---|
| Elixir | 불가, `Stream.unfold`로 다시 써야 함 | pull, 순차 |
| Clojure | `lazy-seq`로 감싸야 함 | pull, 순차 |
| Haskell | conduit 라이브러리, `yield`, `liftIO` | pull, 순차 |
| entish | 모든 fn이 기본으로 스트림 | push, 동시 실행 |

재귀로 lazy stream을 만드는 것 자체는 Clojure와 Haskell도 할 수 있다. entish의 차이는 감싸는 장치 없이 기본이라는 점과, 생산자와 소비자가 실제로 동시에 돈다는 점이다. 대가로 pull 방식보다 요청이 한두 번 더 나갈 수 있다.
