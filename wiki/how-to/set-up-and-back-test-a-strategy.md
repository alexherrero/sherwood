# Set up and back-test a strategy

This page takes you from your own goals to a `strategy.yaml` that sherwood can trade from. On your Mac, the `strategy-setup` skill interviews you, writes the file from sherwood's rule library, checks it with the same code sherwood runs at start-up, and back-tests it on past prices. You then copy the file to the NAS yourself.

**Nothing on this page, and nothing the skill writes, is financial advice.** The skill turns your own answers into rules a program can follow, and a back-test only shows how those rules would have behaved on past prices, which says little about the future. You decide whether to use any of it.

Every path, symbol and amount below is a placeholder; put your own in its place.

## Before you start

You need:

- A Mac with Go installed and a checkout of the repo.
- Claude Code, opened at the root of that checkout.
- A free Tiingo token for price history. Sign up at tiingo.com; the free personal plan is enough. It can be the same token you put in the NAS settings file.
- A rough idea of how much you want sherwood to manage, and how much loss you could live with in a day, on one trade and over a bad stretch of months.

The skill never asks for your broker login or your holdings, and never reaches the NAS. Keep it that way: if you mention them, it will not write them down.

## 1. Start the skill

In Claude Code at the repo root, run `/strategy-setup`. It begins by saying that nothing it writes is financial advice, then asks where to keep its work.

Choose a folder **outside the repo**. The default is `~/sherwood-setup`. Everything personal lives there: your answers, the strategy file, the price history, the back-test reports and the token file. The skill refuses a folder inside a git checkout, because the repo is public and nothing personal belongs in it.

The skill builds sherwood into the work folder and runs `sherwood rules` to read the rule library from the code itself. The [rule library reference](../reference/rule-library-and-back-tests) describes the same rule types and settings.

## 2. Answer the interview

The skill asks one topic at a time:

- **Goals:** growth over years, a steadier ride, or a mix, and how often you are happy to approve buys on your phone.
- **Amount:** how much sherwood will manage, in dollars with cents, such as `10000.00`. This becomes the back-test's starting capital.
- **Loss tolerance**, as three questions:
  - the loss in one day that should stop trading for the rest of that day, which becomes `daily_loss_limit`;
  - the share of the money you would accept losing on one trade that goes wrong, which sets `risk_per_trade` and how far the stops sit;
  - how far the whole portfolio could fall over a bad stretch before you would want out, which you will compare with the back-test's maximum drawdown.
- **Sectors:** which to cover, which to leave out, any stocks you have in mind, and whether to cap any one sector.
- **Practical limits:** whether your account allows fractional shares, how many stocks at once feels manageable (at most 20), and how many weeks of paper trading you want before live mode can be turned on.

If you have no view on something, the skill says which starting value it will use, and you can change it later.

## 3. Review the draft and validate it

The skill writes `~/sherwood-setup/strategy.yaml` and explains it in plain words: what each strategy buys, when it sells and how big each buy is. It aims for a few strategies of different kinds, each with one sizing rule and a stop, so the whole does not depend on one market direction.

It then checks the file:

```sh
~/sherwood-setup/sherwood check-strategy -strategy ~/sherwood-setup/strategy.yaml
```

This is the same check sherwood runs at start-up. If anything is wrong, the error names every field at fault, and the skill fixes them and runs it again until it prints `is valid. sherwood would start with it.` with the file's hash. A `note:` line points out a strategy that never buys, has no stop or has nothing telling it when to buy.

## 4. Put your Tiingo token in a file

The back-test needs past prices, and `fetch-bars` gets them from Tiingo with your token. Put the token in a file yourself, in your own Terminal window rather than in the conversation, so it never passes through Claude:

```sh
(umask 077; read -rs "T?Tiingo token: "; printf '%s' "$T" > ~/sherwood-setup/tiingo-token)
```

Paste the token at the prompt and press Return; nothing is shown as you type. The file is readable only by you, and `fetch-bars` refuses a token file anyone else can read. Tell the skill when it is done. It will not open the file to check.

## 5. Fetch the price history

Choose the back-test's first date with the skill. At least three years back is a good start, ideally a stretch that includes a falling market. Then fetch from about **15 months before** that date. The rules can look back up to 300 trading days, and the extra months give them their full history on the back-test's first day.

```sh
~/sherwood-setup/sherwood fetch-bars -strategy ~/sherwood-setup/strategy.yaml -out ~/sherwood-setup/bars -from 2021-10-01 -token-file ~/sherwood-setup/tiingo-token
```

It writes one `<SYMBOL>.csv` per stock, adjusted for splits but not dividends, up to the last trading day that has closed. Each stock costs one Tiingo request, and the free plan allows 50 an hour, so a long list may need a pause between runs. If a stock has no data, swap it for another.

If you would rather not use Tiingo, put your own split-adjusted files in the `bars` folder in the [bars file format](../reference/rule-library-and-back-tests#bars-files).

## 6. Run the back-test and read it

```sh
~/sherwood-setup/sherwood backtest -strategy ~/sherwood-setup/strategy.yaml -bars ~/sherwood-setup/bars -capital 10000.00 -from 2023-01-03 -trades > ~/sherwood-setup/backtest.txt
```

Use your own amount and the first date you chose. The report gives each strategy's return, maximum drawdown, win rate and turnover, and with `-trades` every filled order with the rule that made it. Compare the maximum drawdown with your answer about a bad stretch of months.

Read the results knowing what a back-test leaves out:

- It replays past prices only, and stocks chosen today because they are well known today make the past look better than it would have looked at the time.
- It does not apply `max_order_value`, `max_stock_share`, the sector limit, the daily order count or the daily loss limit. The risk check on the NAS applies all of them, so it may refuse orders the back-test filled.
- It does not count dividends, and its fills follow sherwood's paper fill model, not a real market.

The [reference](../reference/rule-library-and-back-tests#how-a-back-test-works) explains exactly how orders fill and how each figure is worked out.

If you want changes, ask for them. The skill edits the file, validates it and back-tests it again. Tuning settings until the past looks best mostly fits the past, so change things because they match your goals, not because a number went up.

## 7. Copy the file to the NAS

The skill runs `check-strategy` once more and shows the strategy hash. Note it. Then copy the file to sherwood's application-data folder on the NAS yourself, for example:

```sh
scp ~/sherwood-setup/strategy.yaml you@nas.example:/mnt/user/appdata/sherwood/strategy.yaml
```

On the NAS, give the file to the user the container runs as, and restart sherwood so it reads the new file:

```sh
chown 10001:10001 /mnt/user/appdata/sherwood/strategy.yaml
docker compose restart
docker logs sherwood
```

Use the user you chose in [Install sherwood on the NAS](install-sherwood-on-the-nas) if it is not 10001. The log should show `strategy file loaded` with the same `strategy_hash` that `check-strategy` printed, then `sherwood started`. If sherwood refuses to start instead, the last line lists every problem in the file.

Run a new strategy in paper mode first, with `mode: paper` in the NAS settings file. Live mode can only be turned on after `min_paper_weeks` of paper trading.

## 8. Tidy up

The work folder holds your answers, the price history, the reports and the token file, all of it personal. Keep it outside the repo, and delete the token file once you no longer need it:

```sh
rm ~/sherwood-setup/tiingo-token
```

In the repo, `git status` should show nothing from the session.

## Doing it without the skill

Every step above is an ordinary command, so you can write `strategy.yaml` by hand and run them yourself. Start from `.claude/skills/strategy-setup/example-strategy.yaml` for the shape of the file, look up each rule's settings in the [rule library reference](../reference/rule-library-and-back-tests) or with `sherwood rules`, and then run `check-strategy`, `fetch-bars` and `backtest` as in steps 3 to 6.

## Related

- [Rule library and back-test reference](../reference/rule-library-and-back-tests) lists every rule type and setting, the command flags, the bars file format and the fill rules.
- [Settings and strategy file reference](../reference/settings-and-strategy-files) explains every limit in the strategy file.
- [Install sherwood on the NAS](install-sherwood-on-the-nas) covers the application-data folder and restarting the container.
