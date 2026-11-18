# Rule library and back-test reference

Every trade sherwood suggests comes from a rule in its rule library. A strategy in `strategy.yaml` picks rules from the library by name and gives each one its settings; sherwood then runs those rules after each market close and turns what they say into signals. This page lists every rule type, every setting it takes and the values each setting accepts, explains how one strategy's rules are combined into a single answer per stock, and describes the commands that check and back-test a strategy file.

Every example on this page uses made-up symbols and placeholder numbers to show the shape of the file. None of it is a recommendation, and nothing here is financial advice. A rule is a mechanical test of past prices, not a judgement about a company.

## Writing a rule

Each entry under a strategy's `rules` is one rule type and its settings:

```yaml
strategies:
  - name: example-trend
    portfolio_share: 0.40
    stocks: [TECH1, TECH2, HLTH1]
    rules:
      - type: moving_average_cross
        settings:
          fast_days: 20
          slow_days: 50
      - type: trailing_stop
        settings:
          distance: 0.10
      - type: size_by_risk
        settings:
          risk_per_trade: 0.01
```

**Every setting is required.** A rule type has no defaults of its own, so nothing is ever set by accident. A missing setting, a setting the rule type does not take and a value out of range are all refused when sherwood starts, and the error names the strategy, the rule's position and the setting:

```text
refusing to start: strategy file is invalid: strategy "example-trend": rules[0] (moving_average_cross): slow_days is missing; it must be a whole number from 3 to 300
```

The [settings and strategy file reference](settings-and-strategy-files#rules) covers the rest of a strategy: its name, its share of the portfolio and its stocks.

### Kinds of setting value

| Kind | Written as | Accepted |
|---|---|---|
| Whole number | Digits only, such as `20` | Within the range the setting gives. No sign, decimal point or exponent. |
| Fraction | A plain decimal such as `0.05`, which means 5% | Above 0 and below 1. `.05`, `5e-2`, `5%` and a sign are refused. |
| Dollar amount | A plain decimal such as `"250.00"` | Above 0, with at most two decimal places. |

Like the limits, these are read from the exact text you wrote and never pass through a floating-point number, so `0.05` stays exactly `0.05`.

### The four kinds of rule

- **Signal rules** look at one stock's prices and say buy, add, sell or no change. A buy is for a stock the strategy does not hold, and an add is for one it does.
- **The ranking rule** looks at all of a strategy's stocks together, because a rank only means something among the others. It can also say trim.
- **Stop rules** work out a holding's stop price, and say sell when the stock has closed at or below it. They run only for stocks the strategy holds.
- **Sizing rules** decide how many shares a buy is for. They never decide whether to buy.

A working strategy usually has at least one signal or ranking rule to say when to buy, one stop rule to protect what it holds, and exactly one sizing rule. A strategy with no sizing rule never buys.

### When a rule speaks

Signal and ranking rules signal on the day their condition **starts** to hold, not on every day it goes on holding. A run of new highs is one breakout, and a cross is one cross, so a buy you decline on your phone is not proposed again the next day.

Stop rules are different: a stop is a level. A holding that has closed at or below its stop gets a sell on every day it is still held.

A rule needs enough history to answer. When it has too few days of prices it says no change, with a reason such as `needs 51 days of prices and has 40`. The longest look-back any setting allows is 300 trading days, and a rule can need up to 302 days of prices.

### How a strategy's rules combine

Each stock gets one answer a day from all of its strategy's rules together. The first of these that any rule gives wins:

1. A stop rule's sell.
2. Any other rule's sell.
3. A trim.
4. A buy or add.
5. No change.

So a stop always comes first, any sell beats a buy, and a trim beats a buy. The reason recorded with the signal is that of the first rule, in the order the rules are written in the file, that gave the winning answer. It is written as the rule type and the rule's own reason, such as `breakout: closed at 10.50, above its 50-day high of 10.00`. When a strategy has more than one stop rule, a holding's stop is the highest of them.

The daily run and the back-tester both use this same combining code, so a back-test and a live day decide alike.

### Checks across a strategy's rules

Besides each setting's own range, sherwood checks these within each strategy when it starts:

- At most one sizing rule.
- `size_by_risk` needs a `fixed_stop` or a `trailing_stop` in the same strategy, because it sizes from the distance to the stop.
- `moving_average_cross`'s `slow_days` must be more than its `fast_days`.
- `momentum_ranking`'s `exit_rank` must be at least its `top`, and its `top` must be fewer than the strategy's number of stocks, or it could never signal.

## Rule types

Every rule type sherwood knows is below. `sherwood rules` prints the same list from the code you are running, and a test fails if this page and the code ever disagree.

### `moving_average_cross`

**Kind:** signal rule.

Buys when the fast average of closes crosses above the slow one, and sells a holding when it crosses below.

| Setting | Accepted values | Meaning |
|---|---|---|
| `fast_days` | a whole number from 2 to 299 | days in the fast average |
| `slow_days` | a whole number from 3 to 300 | days in the slow average; more than `fast_days` |

It needs `slow_days` + 1 days of prices. It says buy (or add) on the day the fast average moves above the slow, and sell on the day it moves below. Every other day is no change, with a reason such as `its 20-day average is above its 50-day average`.

### `breakout`

**Kind:** signal rule.

Buys when a close clears the highest high of the previous days, and sells a holding when a close falls through the lowest low of the previous exit days.

| Setting | Accepted values | Meaning |
|---|---|---|
| `days` | a whole number from 2 to 300 | days of highs a close must clear to buy |
| `exit_days` | a whole number from 2 to 300 | days of lows a close must fall through to sell |

It needs the larger of `days` and `exit_days`, plus 2, days of prices. It says buy on the first close above the highest high of the previous `days` days, such as `closed at 10.50, above its 50-day high of 10.00`, and sell a holding on the first close below the lowest low of the previous `exit_days` days.

### `momentum_ranking`

**Kind:** ranking rule.

Ranks the strategy's stocks by their change in close; buys those that move into the top ranks, trims holdings that fall out of them and sells holdings that fall below the exit rank.

| Setting | Accepted values | Meaning |
|---|---|---|
| `days` | a whole number from 2 to 300 | days the change in close is measured over |
| `top` | a whole number from 1 to 20 | ranks that buy; fewer than the strategy's stocks |
| `exit_rank` | a whole number from 1 to 100 | the lowest rank a holding may fall to before it is sold; at least `top` |

It needs `days` + 2 days of prices. Each stock's score is its latest close divided by its close `days` trading days earlier, ranked best first, with ties broken by symbol. A stock is ranked only if it has enough prices and a price for the latest date; any other stock gets no change with a reason. On the day a stock moves into the top `top` ranks it is bought (or added to). On the day a holding falls out of the top `top` it is trimmed, and on the day it falls below `exit_rank` it is sold; when both happen on the same day, it is a sell. The reason gives the rank, such as `rose to rank 2 of 6 by its 120-day change, into the top 3`.

### `pullback`

**Kind:** signal rule.

Buys a close some way below its recent high close while it is still above its trend average, and sells a holding when a close falls below that average.

| Setting | Accepted values | Meaning |
|---|---|---|
| `trend_days` | a whole number from 2 to 300 | days in the average a close must stay above |
| `high_days` | a whole number from 2 to 300 | days of closes the pullback is measured from |
| `pullback` | a fraction above 0 and below 1, such as 0.05 for 5% | how far below that high close a close must be to buy |

It needs the larger of `trend_days` and `high_days`, plus 1, days of prices. It says buy on the first day the close is at least `pullback` below its highest close of the last `high_days` days (today included) **and** still above its `trend_days` average. It sells a holding on the first close below that average.

### `fixed_stop`

**Kind:** stop rule.

Sets a holding's stop a fixed share below its average cost, and sells it when it closes at or below the stop.

| Setting | Accepted values | Meaning |
|---|---|---|
| `distance` | a fraction above 0 and below 1, such as 0.05 for 5% | how far below the average cost the stop sits |

The stop is the average cost times (1 − `distance`), rounded to the cent. A holding that closes at or below it gets a sell, such as `closed at 90.00, at or below its stop of 90.00`. Otherwise it is no change, such as `its stop is 90.00, 10% under its cost of 100.00`. The stop is set either way.

### `trailing_stop`

**Kind:** stop rule.

Sets a holding's stop a fixed share below its highest close since it was bought, never lowering it, and sells it when it closes at or below the stop.

| Setting | Accepted values | Meaning |
|---|---|---|
| `distance` | a fraction above 0 and below 1, such as 0.05 for 5% | how far below the highest close since buying the stop sits |

The stop is the highest close since the holding was bought times (1 − `distance`), rounded to the cent, and it never moves down. It sells as `fixed_stop` does. When `size_by_risk` sizes a new buy, it uses the stop this rule would give on the day of the signal: that day's close times (1 − `distance`).

### `size_fixed_amount`

**Kind:** sizing rule.

Sizes each buy to spend at most a fixed dollar amount, rounded down to the share policy.

| Setting | Accepted values | Meaning |
|---|---|---|
| `amount` | a dollar amount above 0 with at most two decimal places | the dollar amount each buy spends at most |

The share count is `amount` divided by the buy's limit price, rounded down to whole shares, or to six decimal places of a share when `fractional_shares` is on. Keep `amount` no larger than the `max_order_value` limit, or the risk check refuses those buys.

### `size_by_risk`

**Kind:** sizing rule.

Sizes each buy so that a fall from its price to its stop loses at most a share of the strategy's capital, rounded down to the share policy.

| Setting | Accepted values | Meaning |
|---|---|---|
| `risk_per_trade` | a fraction above 0 and below 1, such as 0.05 for 5% | the share of the strategy's capital a buy may lose if it falls to its stop |

The strategy's capital is the portfolio's value times its `portfolio_share`. The share count is that capital times `risk_per_trade`, divided by the gap between the buy's limit price and the stop its stop rules would give it, rounded down. For example, with a strategy capital of 4,000.00, a `risk_per_trade` of `0.01`, a limit of 50.00 and a stop of 45.00, the buy risks 40.00 and is for 8 shares. A stop at or above the price gives no shares.

## Commands that check and test a strategy

These run on any computer with Go and a checkout of the repo, such as your Mac. Build the binary once with `go build -o ~/sherwood-setup/sherwood ./cmd/sherwood` from the repo root, or use `go run ./cmd/sherwood` in its place. None of them opens sherwood's database, reads the settings file or reaches a broker. [Set up and back-test a strategy](../how-to/set-up-and-back-test-a-strategy) walks through them in order.

### `sherwood rules`

Prints every rule type, its kind, what it does and every setting with its accepted values, followed by the checks across a strategy's rules. It takes no flags. It is the same list as this page, read from the code you built.

### `sherwood check-strategy`

| Flag | Meaning |
|---|---|
| `-strategy FILE` | The strategy file to check. Defaults to `SHERWOOD_STRATEGY`, or `/data/strategy.yaml` when that is unset. |

Checks the file exactly as sherwood does when it starts: the file's shape, the limits, every rule's settings and the checks across rules. On success it prints `<file> is valid. sherwood would start with it.`, the file's hash and one line per strategy. It adds a `note:` line for a strategy with no sizing rule (it never buys), no stop rule (its holdings get no protective stop) or no signal or ranking rule (nothing tells it when to buy). On failure it lists every problem and exits with a non-zero status.

### `sherwood fetch-bars`

| Flag | Meaning |
|---|---|
| `-out DIR` | The folder to write the bars files into. It is created if missing. Required. |
| `-from DATE` | The first date to fetch, as `YYYY-MM-DD`. Required. |
| `-to DATE` | The last date to fetch. Defaults to the last trading date whose session has closed. |
| `-strategy FILE` | The strategy file whose stocks to fetch. Defaults as for `check-strategy`. |
| `-token-file FILE` | A file holding your Tiingo token. Defaults to the `SHERWOOD_TIINGO_TOKEN` environment variable. |

Writes one bars file for every stock in every strategy, from Tiingo, with the same split adjustment sherwood uses on the NAS. Prices are adjusted for splits but not for dividends. The token is never taken as a flag value, and a token file that anyone but you can read is refused. Each stock costs one Tiingo request; the free plan allows 50 an hour and 1,000 a day. The first error stops the run and leaves no half-written file.

### `sherwood backtest`

| Flag | Meaning |
|---|---|
| `-bars DIR` | The folder of bars files. Required. |
| `-capital AMOUNT` | The starting capital in dollars, above zero with at most two places, such as `10000.00`. Required. |
| `-strategy FILE` | The strategy file to test. Defaults as for `check-strategy`. |
| `-from DATE` | The first trading date to replay. Defaults to the first bar. Bars before it are used only as history. |
| `-to DATE` | The last trading date to replay. Defaults to the last bar. |
| `-trades` | Also lists every filled order under its strategy, with the rule's reason and the fill's reason. |

Replays the bars through the strategy's rules and the paper fill model, one strategy at a time, and prints a report. The same inputs always print the same report, byte for byte.

## Bars files

The back-tester reads one file per stock, named `<SYMBOL>.csv`, such as `TECH1.csv`, from the bars folder. `fetch-bars` writes them, or you can write your own.

```text
Date,Open,High,Low,Close,Volume
2024-01-02,101.20,103.45,100.80,102.90,1250000
2024-01-03,102.80,104.10,102.10,103.70,980000
```

- The header is exactly `Date,Open,High,Low,Close`, with an optional `,Volume` at the end.
- One row per trading date, oldest first, with dates as `YYYY-MM-DD`.
- Prices are plain decimals. More than two decimal places are rounded to the cent, halves away from zero.
- Each row must make sense on its own: the high is the highest price of the day and the low the lowest.
- The prices must already be adjusted for splits, or a split looks like a crash.

## How a back-test works

The back-test steps through every date on which any of a strategy's stocks has a bar. On each date, in this order:

1. The orders made after the previous close are worked against the day's prices: sells at the open first, then resting stops, then buys.
2. Every holding is valued at its latest close.
3. After the close, the rules run on the prices up to and including that day, and what they say becomes the next day's orders.

A signal from one day's close can fill on the next trading day at the earliest, so the back-test never sees a price before it would have happened.

### Orders and fills

- **A buy** is a day limit order at the last close times one plus the strategy file's `buy_limit_margin`, rounded to the cent, sized by the strategy's sizing rule at that price. It fills at the open if the open is at or below the limit. Otherwise it fills at the limit only if the day's low went **below** the limit. A low that only touches the limit is not a fill, because a touch does not prove a seller met the order. A buy that does not fill expires.
- **A stop** rests from the day after the holding is bought. It fills at the open if the stock opens at or below the stop, and otherwise at the stop if the day's low **reaches** it.
- **A rule's sell or trim** fills at the next open. A trim sells half the holding, rounded down; a one-share holding under whole shares cannot be trimmed.

Fills are all or nothing.

### Limits a back-test applies, and those it does not

Each strategy trades only its own capital, the starting capital times its `portfolio_share`, with no margin. That capital does not grow with profits during the run. A buy that would cost more than the cash not yet promised to earlier buys is blocked, and so is a buy of a new stock that would take the strategy past `max_holdings`.

**The back-test does not apply** `max_order_value`, `max_stock_share`, `max_sector_share`, `max_orders_per_day` or `daily_loss_limit`. On the NAS the risk check applies every one of them, so it may refuse orders the back-test filled. In particular, check that every `size_fixed_amount` amount is within `max_order_value` yourself, because the back-test will not warn you.

### The report

For each strategy the report gives:

- **Return:** the final value, cash plus holdings at their last close, against the strategy's capital.
- **Max drawdown:** the largest fall from an earlier high in the daily value, as a share of that high.
- **Win rate:** the share of sells, trims and stops included, that sold above the average cost of the shares sold. Holdings still open at the end are not counted.
- **Turnover:** the value bought plus the value sold, halved, over the average daily value. 1.00 means the strategy traded its whole value once.
- The orders filled and unfilled, the buys blocked for cash, for `max_holdings` or for want of a sizing rule, and the stocks held at the end.

Every figure is worked out in exact decimals and rounded to two places in the report. The report itself says it is a replay of past prices, not a forecast and not financial advice.

### What a back-test leaves out

A back-test only shows how the rules would have behaved on past prices. It does not count dividends, its fills follow sherwood's paper fill model rather than a real market, and a list of stocks chosen today, because they are well known today, makes the past look better than it would have looked at the time. Tuning settings until the past looks best mostly fits the past. Treat the results as a description of the rules, not a promise about the future.

## Related

- [Set up and back-test a strategy](../how-to/set-up-and-back-test-a-strategy) runs the setup skill and these commands step by step.
- [Settings and strategy file reference](settings-and-strategy-files) explains the limits, the watchlist and the rest of a strategy.
- [Sherwood Design](../designs/sherwood) explains why trades come only from written rules and why every order passes the risk check.
