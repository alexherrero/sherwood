# Run the first live session

This page turns live trading on for the first time and takes you through the first trading day with real orders, with a small amount of money. Live trading means that orders you approve, and the protective stops sherwood keeps on your holdings, are placed with your broker account and use real money.

You run every step yourself. Nothing here can be tried out first: the checks at the end need your real account, and that's why they are yours. Every amount, symbol and name below is a placeholder. When something goes wrong during the day, the [runbook](../operational/runbook) says what to do.

## Before you start

You need:

- **A paper record long enough.** The strategy file's `limits.min_paper_weeks` sets the fewest weeks of paper trading before live mode can start. Settings shows the paper record's **Weeks so far**. Only weeks in paper mode count; time spent in live mode, even waiting for the confirmation, doesn't. The minimum is the floor, not a target: go live only once the paper record has held up as you hoped.
- **The broker login and a first import done**, as in [Log in to the broker on the NAS](log-in-to-the-broker-on-the-nas), with the holdings and cash compared against the broker's app.
- **Notifications on**, and your phone set up as an approval device (named in `approval_devices`).
- **A trading day you can watch**, from before 09:00 US Eastern until after the close. A full day is better than a half day. All times on this page are US Eastern.
- **The strategy file in its final form.** Any edit to it after you confirm ends the confirmation, so make your changes first.

## 1. Choose a small starting amount

Sherwood trades the account it is logged in to. The small starting amount is held by the strategy file's limits, which the risk check applies to every order:

- `max_order_value`, the most a single order may be worth;
- `max_stock_share`, the largest share of the portfolio one stock may hold;
- `max_orders_per_day`, the most orders sent in a day;
- `daily_loss_limit`, the day's loss at which trading halts;
- `max_holdings`, the most holdings sherwood may have.

Set them low for the first session. For example, in the strategy file on the NAS:

```yaml
limits:
  max_order_value: "<a small amount per order>"
  max_orders_per_day: 1
  daily_loss_limit: "<the most you are willing to lose in one day>"
```

The [settings reference](../reference/settings-and-strategy-files#limits) lists every limit and the values it takes. Edit the file before you confirm live mode, because an edit after it ends the confirmation. After you confirm, you can still tighten a limit from Settings on the phone, which doesn't change the file and keeps the confirmation. Raise the amount later only by loosening the limits at the NAS, which needs a new confirmation and a restart.

Two things the limits don't cover:

- **Protective stops on what you already hold.** Protective stops skip `max_order_value`, so every holding keeps a stop for all its shares. At the first 09:35 send, sherwood places a resting stop at the broker on each holding its stop rules give a stop to, including holdings you had before sherwood. A stop that triggers sells real shares. Before you go live, look at each holding on Portfolio, where the rules' suggestion says "Stop at" a price, and be sure you want each stop.
- **Orders you placed yourself.** Any order open in the broker's own app, such as a stop of your own, has a reference sherwood doesn't know. Reconciliation names it as a foreign order and halts trading. A stop of your own would also sit beside the one sherwood places. Cancel your own open orders in the broker's app before the first live day.

## 2. Set live mode in the settings file

Do this outside market hours, the evening or weekend before the first live day.

1. Set the mode in `settings.yaml` on the NAS:

   ```yaml
   mode: live
   ```

2. Restart sherwood so it reads the file:

   ```sh
   docker compose restart sherwood
   ```

3. Check the log with `docker logs sherwood`. You should see `sherwood started` with `"mode":"live"`, then a warning, `orders are not sent`, with the reasons. Live trading is still off, so nothing is sent yet.
4. Open Today. The banner says "Live trading is off" and why. Open Settings and find the **Live mode** card. It lists the three conditions:
   - **The settings file names live mode:** Yes.
   - **Confirmed in the app:** No.
   - **Paper record long enough:** Yes, with the weeks so far.

If the paper record isn't long enough yet, the card says so and shows no confirm button. Go back to paper mode and wait; weeks in live mode don't count.

## 3. Confirm live mode on Settings

1. On your phone (an approval device), open Settings and scroll to the **Live mode** card.
2. Read the text above the button, then tap **Confirm live mode**.
3. Settings says "Live mode is confirmed." and asks you to restart sherwood on the NAS.

Any other device sees the card but is refused with "Only an approval device can do this." Recent changes on Settings now lists the mode's change from paper to live and the live-mode confirmation.

## 4. Restart to turn live trading on

1. Restart again:

   ```sh
   docker compose restart sherwood
   ```

2. The log now says `live trading is on`.
3. Today's banner says "Live trading: orders you approve are placed with the broker and use real money." The Live mode card on Settings shows **On: real orders**.

Sherwood reads the three conditions only when it starts, which is why the confirmation takes this second restart. From now on, these jobs run on every trading day: the reconciliation at 09:00, the send at 09:35, the two checks every 15 minutes during market hours, and a reconciliation at the end of the after-close run. The [runbook](../operational/runbook#the-live-jobs-and-the-15-minute-checks) describes each one.

## 5. Watch the first live day

### The evening before

- The after-close run proposes the next day's orders. Approve no more than you mean to try: one small buy is enough for a first day. Approvals' risk check now also checks that the broker is answering.
- At the end of the after-close run, a reconciliation compares sherwood with the broker. On Today it should have succeeded, and the log says `reconciled with the broker`.
- Check when the broker session ends. It lasts about a day. If it ends before tomorrow's close, log in again in the morning before 09:00, as in [Log in to the broker on the NAS](log-in-to-the-broker-on-the-nas#1-log-in).

### At 09:00

The `reconcile` job compares holdings, cash and open orders with the broker. On Today it should succeed, with `reconciled with the broker` in the log. If it halts, the details name the difference: see [Reconciliation differences](../operational/runbook#reconciliation-differences) before you reset anything.

### At 09:35

The `send-orders` job sends the approved orders and places the protective stops.

- On the Orders view, each approved order is accepted, or filled at once with the broker's price.
- Each holding with a stop rule has one resting stop.
- The broker's app shows the same orders, and no others.
- No "Trading halted" notification arrived. If one did, read the halt on Today and turn to the [runbook](../operational/runbook#halts-and-their-reset).

### Every 15 minutes

From 09:30 until 15:45, the `open-orders-check` and `daily-loss-check` jobs run in each slot.

- Today shows both succeeding, with the start times moving on every 15 minutes.
- The log says `open orders agree with the broker` and `daily loss under the limit`.
- When a resting order fills during the day, the next check finds it, and Orders shows the fill "by reconciliation". Portfolio's holdings and cash move with it.

You don't need to watch every slot. A notification reaches you if a check halts trading or fails.

### After the close

- The after-close run fetches prices, runs the rules and proposes the next day's orders, then reconciles with the broker. On Today it should succeed.
- Compare Portfolio with the broker's app: every holding, the shares, the cash, and each fill's price on Orders.
- Read the checklist below and note what you found.

## 6. How to stop

- **To stop sending orders at once,** press **Stop all trading** on Today. Waiting buys are declined, open buys are cancelled at the broker, and nothing more is sent until you reset the switch. Resting stops stay, so your holdings stay protected.
- **To take the stops off as well,** tick the box on the **Protective stops** card and press **Cancel protective stops too**. Every resting stop is cancelled at the broker, and no new one goes out until you reset.
- **To go back to paper mode,** follow [Going back to paper mode](../operational/runbook#going-back-to-paper-mode) in the runbook. Cancel the orders resting at the broker *before* you switch: paper mode has no broker connection, so it can neither see nor cancel them.

## 7. Checklist for the first live session

These checks need the real account, so they are yours. Note what you find for each; some answers decide later changes to sherwood. Don't try to make anything go wrong on purpose, such as losing money or forcing the session to expire. The tests cover those cases.

### Turning it on

- [ ] With `mode: live` and no confirmation, the log says `orders are not sent` and Today says why.
- [ ] Confirming from the phone and restarting gives `live trading is on` in the log.
- [ ] Confirming from a device that is not an approval device is refused.

### The first orders

- [ ] **The answers to a new order.** For the first buy, compare the order in the broker's app with Orders. Did it come back filled straight away, or accepted first? Either works; note which. Did anything come back in a state sherwood doesn't know, which would halt with `unknown_order`?
- [ ] **The reference on each order type.** The broker's app shows the order sherwood placed for a limit buy, a stop sell and a sell at the open, and reconciliation reports no difference for them. That shows each one carried sherwood's reference.
- [ ] **A cancel.** The first time sherwood cancels an order (for example when it raises a stop), the broker's app shows the old order cancelled and the next check shows no difference.
- [ ] **Stop sizes.** The broker took a stop on every holding. Note any stop it refused, especially on a fractional share count or a large holding.
- [ ] **A foreign order, if you choose to check it.** With sherwood's stops resting and no difference, place one tiny order by hand in the broker's app, well away from the market. The next check names it as a foreign order and halts. Cancel it by hand, then reset.
- [ ] **A second send with the same reference: skip this** unless you are willing to risk a duplicate tiny order. Sherwood never resends on its own.

### Fills, cash and the account value

- [ ] **Fill prices.** After the first fill, the price on Orders matches the broker's app, to the cent.
- [ ] **A fill during the day.** The next 15-minute check settles it by lookup, Orders shows "by reconciliation", and trading doesn't halt.
- [ ] **Which cash.** After the first fill, compare the cash on Portfolio with the app's cash, buying power and withdrawable cash. Sherwood reads the broker's cash, not buying power. Does it include unsettled sale proceeds?
- [ ] **Fees and other cash movements.** After the first sell, did the cash differ by a few cents, from regulatory fees? Dividends, interest and deposits also move cash, and reconciliation halts on any difference. Note whether a small tolerance would be worth adding later.
- [ ] **The account value.** During market hours and outside them, compare the value the daily loss check uses with the total in the broker's app. Is the "down X from Y" figure right, when the check halts or in the log?

### The broker connection

- [ ] **Session expiry.** When the session lapses on its own, you get one "Trading halted", one "Broker login needed" and one "A scheduled job failed" that day, not one per call, and Approvals refuses buys with "The broker is not answering" until a call works. After you log in again and reset, the next slot succeeds. Note how long the session really lasts, to decide whether a daily login is needed.
- [ ] **Call times.** The order calls get 60 seconds and holdings 120. If the checks or lookups are slow or fail with "ran past 50 pages", the account's order history is long and sherwood needs a change.
- [ ] **A quote-less order.** Sherwood's orders leave out the quote fields the broker's own library sends. If the broker refuses an order for that reason, it halts with `broker_error` and nothing is sent; note the reason.

### Later, not on the first day

- [ ] **Stop expiry.** Note when the broker expires a resting stop (it has said about 90 days, which is unverified). Check that trading halts with a lost stop, and that the next 09:35 send places a new one at no lower price.

## What's next

Once the first session has gone as you expected, leave sherwood running and keep the [runbook](../operational/runbook) at hand. To trade a larger amount, loosen the limits a step at a time at the NAS. Each edit to the strategy file needs a new confirmation on Settings and a restart.
