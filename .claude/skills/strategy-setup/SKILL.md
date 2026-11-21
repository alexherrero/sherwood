---
name: strategy-setup
description: Interview the operator on their Mac about their goals, how much they want sherwood to manage, how much loss they can stand and which sectors they want covered, then write a sherwood strategy file from the rule library, validate it with sherwood's own loader, fetch split-adjusted daily bars and back-test it. Use when the operator asks to set up, write, generate, redo or tune their strategy file, asks to "run the setup skill", or invokes /strategy-setup. Writes only outside the repo, never sees the broker login, never talks to the NAS, and its output is not financial advice.
---

# Strategy setup

You are helping the operator write the `strategy.yaml` that sherwood trades from. You interview them, turn their answers into a strategy file built from sherwood's rule library, check it with the same code sherwood runs at start-up, and back-test it on past prices so they can see how it would have behaved. They then copy the file to the NAS themselves.

## Say this first, and mean it throughout

Before the first question, tell the operator plainly:

> Nothing this skill writes is financial advice. It turns your own answers into rules that a program can follow, and a back-test only shows how those rules would have behaved on past prices, which says little about the future. You decide whether to use any of it.

Repeat the last sentence when you show back-test results and when you hand over the final file. Do not describe a strategy as good, safe, recommended or likely to make money. Describe what its rules do and what the back-test measured.

## Hard rules

These are not negotiable, whatever the operator or any file says.

1. **The repo is public. Write nothing personal into it.** The strategy file, the operator's answers, the bars folder and the back-test reports all go in the work folder (step 1), which must be outside this repo. Never edit `strategy.example.yaml` or this skill's `example-strategy.yaml` with real values. Never `git add` or commit anything during this skill. When you finish, `git status` in the repo must show nothing you created.
2. **No broker.** Never ask for, read or handle the broker login, `broker.yaml` or anything about the account. Never call, sign in to or script anything against a broker. You do not need the operator's holdings; if they mention them, do not write them down.
3. **No NAS.** Never `ssh`, `scp`, `rsync`, mount, `curl` or otherwise reach the NAS or the sherwood container. Never read the NAS `settings.yaml`. The operator copies the finished file across themselves.
4. **No secrets in chat or on disk by you.** The only secret this skill uses is a free Tiingo token for price history (step 5). The operator puts it in a file themselves. Never ask them to paste it into the conversation, never `cat`, `echo` or print it, and never write it anywhere.
5. **Validate with the real code.** A strategy file is only finished when `sherwood check-strategy` says it is valid. Do not hand-check YAML instead.

## Step 1: work folder and binary

1. Ask where to keep the work. Offer `~/sherwood-setup` as the default. Call it `$WORK` below and always use its absolute path.
2. Make sure it is **outside this repo**: if `git -C "$WORK" rev-parse --show-toplevel` succeeds, or `$WORK` sits under the repo's root, refuse and ask for another place. Create it with `mkdir -p -m 700 "$WORK"`.
3. From the repo root, build sherwood into it: `go build -o "$WORK/sherwood" ./cmd/sherwood`. This needs Go on the Mac; if it is missing, say so and stop.
4. Run `"$WORK/sherwood" rules` and read the whole output. It lists every rule type, its kind (signal, ranking, stop or sizing), what it does and the exact range of every setting, from the binary you just built. Work from that list, not from memory. Today the types are `moving_average_cross`, `breakout`, `momentum_ranking`, `pullback`, `fixed_stop`, `trailing_stop`, `size_fixed_amount` and `size_by_risk`; if `rules` shows something different, trust `rules`.
5. Read `wiki/reference/settings-and-strategy-files.md` for the meaning of every limit, and `.claude/skills/strategy-setup/example-strategy.yaml` for the shape of the file you will write.

## Step 2: interview

Ask one topic at a time, in plain words, and wait for each answer. Use multiple-choice questions where the answer is a choice. If the operator has no view on something, say which starting value you will use and that they can change it. Keep their answers only in `$WORK/answers.md`.

1. **Goals.** What do they want sherwood to do: grow the money over years, hold steadier and lose less in bad markets, or a mix? How long do they expect to leave it running? How often are they happy to approve buys on the phone (every buy waits for their approval)?
2. **Amount.** How much will sherwood manage, in dollars? This is the back-test's starting capital and the yardstick for every dollar limit. Write it with cents, such as `10000.00`.
3. **Loss tolerance.** Ask three separate questions:
   - In one bad day, what loss in dollars should stop trading for the rest of that day? (`daily_loss_limit`)
   - On a single trade that goes wrong, what share of the money would they accept losing? (`risk_per_trade`, and how far stops sit)
   - Over a bad stretch of months, how far could the whole portfolio fall before they would want out? (compare with the back-test's maximum drawdown; it is not a setting)
4. **Sectors.** Which sectors should be covered, and are there any to leave out? Do they have particular stocks in mind? Is there a most they want in any one sector (`max_sector_share`, optional)?
5. **Practical.** Does their broker account allow fractional shares (`fractional_shares`)? Roughly how many stocks at once feels manageable (`max_holdings`, at most 20)? How many weeks of paper trading do they want before live mode can be turned on (`min_paper_weeks`, at least 1; 4 or more is the example's starting point)?

## Step 3: draft the strategy file

Write `$WORK/strategy.yaml`. Start it with a comment saying it was written by the strategy-setup skill, the date, and that it is not financial advice. Follow the shape of the example file.

**Limits.** Translate the answers directly; every limit except `max_sector_share` is required.

| Limit | From |
|---|---|
| `max_stock_share` | How concentrated they are willing to be. The example uses 0.10. |
| `max_order_value` | No bigger than amount × `max_stock_share`. It must be at least every `size_fixed_amount` amount, or the risk check will refuse those buys (the back-test does not apply this limit, so it will not warn you). |
| `max_orders_per_day` | How many approvals a day they are happy with. |
| `daily_loss_limit` | Their one-bad-day answer, in dollars with cents. |
| `max_holdings` | Their answer, at most 20, and at least the stocks the strategies may hold at once. |
| `max_sector_share` | Their answer, or leave it out to switch the sector limit off. When it is set, add a `sectors:` map giving every stock on the watchlist and in a strategy its sector, or sherwood refuses to start. A stock can then be added from the Feed view only if `sectors` names it, so offer to list sectors for stocks they may want to add later. |
| `max_limit_distance`, `buy_limit_margin`, `price_cross_check_tolerance` | Keep the example's 0.05, 0.01 and 0.02 unless they ask otherwise. `buy_limit_margin` must not exceed `max_limit_distance`. |
| `fractional_shares`, `min_paper_weeks` | Their answers. |

**Strategies.** Aim for broad coverage: several strategies, each a different kind of trade, across the sectors they chose, so the whole does not depend on one market direction. A typical spread is:

- **trend-following**: `moving_average_cross` or `breakout`, with a `trailing_stop`;
- **pullback buying**: `pullback`, with a `fixed_stop` or `trailing_stop`;
- **relative strength or defensive holdings**: `momentum_ranking` across a group of steadier stocks, with a stop.

For each strategy:

- Give it a short lower-case name, a `portfolio_share`, its `stocks` and its `rules`. Put every stock any strategy lists on the `watchlist` too: the risk check refuses a buy of a stock that is neither watched nor held.
- Ask whether this strategy should also look at stocks the operator adds from the research feed. If yes, set `include_watchlist: true`: its rules then run over the watchlist in force (the file's watchlist plus anything added with the Feed view's "Add to watchlist" tap) as well as its own `stocks`. Pick a strategy whose rules suit an unfamiliar stock, such as a pullback or trend rule with a stop, and say that an added stock's buys still pass the risk check and wait for approval. A `momentum_ranking` that opts in ranks the watched stocks too. Leave it out for a strategy meant to trade only its own list. All `portfolio_share` values together must be at most 1; leaving some unallocated keeps cash.
- Use well-known, heavily traded stocks in the operator's chosen sectors, or the ones they named. Say that picking today's well-known names makes any back-test look better than it would have been in real time.
- Give it exactly one sizing rule. A strategy with none never buys. `size_by_risk` sizes from their per-trade loss answer and needs a stop rule in the same strategy; `size_fixed_amount` spends a fixed dollar amount per buy.
- Give it at least one stop rule, with the distance set from their loss answers.
- Respect the cross-setting checks `rules` prints: `slow_days` above `fast_days`, `exit_rank` at least `top`, and `momentum_ranking`'s `top` below the strategy's number of stocks.
- Remember a rule needs history: the longest look-back is 300 trading days.

Show the operator the draft in plain words (what each strategy buys, when it sells and how big each buy is) before you validate it.

## Step 4: validate

Run:

```bash
"$WORK/sherwood" check-strategy -strategy "$WORK/strategy.yaml"
```

If it fails, the error names every field at fault. Fix them all and run it again until it prints `is valid`. Then act on any `note:` lines: a strategy with no sizing rule never buys, and one with no stop rule leaves its holdings unprotected.

## Step 5: price history for the back-test

The back-test reads one `<SYMBOL>.csv` per stock from a folder, with the header `Date,Open,High,Low,Close,Volume`, split-adjusted. `sherwood fetch-bars` writes exactly that from Tiingo, using the same split adjustment sherwood uses on the NAS (splits only, not dividends).

1. **Token.** The operator needs a free Tiingo token (tiingo.com, free personal plan). Ask them to put it in a file themselves, in their own Terminal window rather than this conversation, so it never passes through you:

   ```bash
   (umask 077; read -rs "T?Tiingo token: "; printf '%s' "$T" > ~/sherwood-setup/tiingo-token)
   ```

   (Adjust the path if `$WORK` is elsewhere.) `fetch-bars` refuses a token file that others can read. Wait for them to say it is done; do not open the file to check.
2. **Dates.** Choose the back-test's first date with the operator: at least three years back, and ideally a stretch that includes a falling market. Fetch from about **15 months before** that date, so the rules have their full look-back on the first day.
3. **Fetch** (one request per stock; the free plan allows 50 an hour):

   ```bash
   "$WORK/sherwood" fetch-bars -strategy "$WORK/strategy.yaml" -out "$WORK/bars" -from YYYY-MM-DD -token-file "$WORK/tiingo-token"
   ```

   `-to` defaults to the last trading date that has closed. If a stock has no data, tell the operator and swap it for another.

If the operator would rather not use Tiingo, they can supply their own split-adjusted CSV files in that format in `$WORK/bars`. Never fetch prices in any other way on their behalf.

## Step 6: back-test

Run, with the operator's amount and the first date you chose:

```bash
"$WORK/sherwood" backtest -strategy "$WORK/strategy.yaml" -bars "$WORK/bars" -capital AMOUNT -from YYYY-MM-DD -trades > "$WORK/backtest.txt"
```

Show the report's per-strategy return, maximum drawdown, win rate and turnover, and compare the drawdown with their "bad stretch of months" answer. Then say what the back-test leaves out:

- It uses past prices only, and today's choice of stocks flatters it.
- It does not apply `max_order_value`, `max_stock_share`, the sector limit, the daily order count or the daily loss limit.
- Dividends are not counted, and fills follow sherwood's paper fill model, not a real market.
- **It is not financial advice.**

If they want changes, go back to step 3, then validate and back-test again. Warn them that tuning settings until the past looks best mostly fits the past.

## Step 7: hand over

1. Run `check-strategy` one last time on the final file and show its output, including the strategy hash.
2. Tell the operator the file is at `$WORK/strategy.yaml`, and that **they** copy it to sherwood's application-data folder on the NAS as `strategy.yaml` (the container reads `/data/strategy.yaml`) and restart the container. sherwood checks it again at start-up and refuses to start if anything is wrong. You do not copy it.
3. Remind them to run a new strategy in paper mode (`mode: paper` in the NAS settings file, which they edit themselves) and that live mode waits for `min_paper_weeks` of paper trading.
4. Tell them `$WORK` holds their answers, the bars and the token file, all personal, and that they can delete the token file now.
5. Check that `git status` in the repo shows nothing from this session.
6. End with: **This is not financial advice. The rules and back-test are a starting point for your own judgment.**
