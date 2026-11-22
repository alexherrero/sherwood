# Runbook

This page is for the moments when something needs you: trading has halted, sherwood's records and the broker's disagree, the broker session has lapsed, the database has to come back from a backup, a new version has to be rolled back, live trading has to go back to paper, or the research feed has gone stale. Each section says what you will see, what to check, and what to do, in order. It covers paper and live mode alike, and says where they differ.

To turn live trading on for the first time, follow [Run the first live session](../how-to/run-the-first-live-session) instead. This page picks up once something goes wrong.

Every path, address, name and number on this page is a placeholder. Where a command names the data folder or the backup share on the NAS, use your own paths in their place. The container is called `sherwood`, as in the example Compose file.

## The notifications that lead here

Your phone only ever gets a few fixed words, never a stock, an amount or a reason. The details are in the app, over the tailnet.

| Notification | What it means | Where to look |
|---|---|---|
| Trading halted | The stop switch is on. | [Halts and their reset](#halts-and-their-reset) |
| A scheduled job failed | A job's run ended in an error. You get one for each job and date, however many times it is retried, and the after-close job and its two retries count as one. | Today, under the job's last run, which says what went wrong |
| Scheduled jobs ran late | Sherwood was not running when some jobs were due, so it is making them up now. You get one for each start that finds runs to make up, but not on the very first start of a new database. | Today, to see the made-up runs finish |
| The nightly backup failed | Last night's backup was not made, or the copy failed its check. You get one per night. | Today, under the backup job, then [Restoring a backup](#restoring-a-backup) if you need one |
| Broker login needed | The broker session has expired or the broker refused it. You get one for each stretch of refused calls, not one per call. | [Logging in again](#logging-in-again) |

In paper mode nothing calls the broker on a schedule, so the broker login notification arrives only in live mode. When live trading is on and the session lapses, expect three notifications close together: "Trading halted", "Broker login needed", and one "A scheduled job failed" for the day's checks.

## Halts and their reset

### What a halt is

A halt turns the stop switch on. While it is on, sherwood sends no buys and no ordinary sells to the broker. Protective stops are still placed and raised, because they only ever cut a loss. The rules still run after the close and record their signals, so nothing is lost while you look into it. Orders that were approved but not sent while the switch was on expire at that evening's after-close run; they are never sent late.

The switch comes on in one of two ways: you press **Stop all trading** on the Today view, or sherwood throws it itself when something is in doubt. Either way you get one "Trading halted" notification, and Today lists every active halt with its cause, what sherwood saw, and when.

When you press **Stop all trading**, sherwood also clears the way for nothing to buy:

- Every buy waiting for your approval is declined, and every approved buy not yet sent is called off. The Orders view shows each one as stopped by the stop switch.
- Every open buy at the broker is cancelled, with one call each. If the broker does not confirm a cancellation, that order becomes unknown and trading halts for it too (see `unknown_order` below); sherwood never tries the call again.
- Sells waiting for approval are left alone. Nothing is sent while the switch is on, and they expire with their trading day.
- Resting protective stops stay in place, so your holdings stay protected.

While the switch is on, Today shows a **Protective stops** card. Tick its box and press **Cancel protective stops too** only if you mean to leave every holding without a stop. This second, separate confirmation cancels each resting stop at the broker, sets aside any stop not yet sent, and adds a halt of its own (`stops_cancelled`), so the 09:35 send places no new stop until you reset the switch. When you reset, the next 09:35 send puts a stop back on every holding.

In paper mode the cancellations go to sherwood's own paper book. In live mode they go to the broker, but only once live trading is on. In live mode before then (see [Run the first live session](../how-to/run-the-first-live-session)), sherwood has no broker connection to cancel through: it declines the waiting buys and tells you to cancel any open orders in the broker's own app.

Automatic halts do not decline or cancel anything. They only stop new orders being sent, and the approval check refuses a buy while the switch is on.

The same failure seen again while the switch is already on is recorded only once. That matters for the broker helper, which is restarted every second while it keeps failing: you get one halt and one notification, not one a second. A different cause, or the same cause with different details, always adds a halt of its own. Two causes are folded more widely, by cause alone: a failed broker call (from the helper, the 09:35 send's account read, reconciliation or a 15-minute check) and the daily loss limit. While one of those halts is on, more of the same cause add nothing, so an expired session that fails every check all day still gives one halt.

### What each cause means and what to check

| Today says | Cause | What to check before you reset |
|---|---|---|
| You pressed Stop all trading | `operator` | Nothing more than why you pressed it. The halt names the device you pressed it from. |
| The broker failed | `broker_error` | Live mode only. Read the details on Today. If they start "The broker session has expired or was refused", follow [Logging in again](#logging-in-again). Otherwise run `docker logs sherwood` and look for `broker helper failed` or the failed call: the helper crashed or would not start, the network or the broker failed, or the broker answered in a shape the helper does not know. A broker answer listing two open orders under one of sherwood's references halts here too, because that order may have been sent twice: check the broker's app for a duplicate before anything else. An order the broker refused when it was sent halts with this cause as well, and the Orders view shows it as rejected with the broker's reason. Fix the helper or wait out the outage, then reset. Sherwood never retries a broker call. |
| The price sources disagree on a stock you hold | `price_disagreement` | The details name each stock and both sources' prices for that date. Look the close up somewhere you trust. The rules made no signal for that stock that day, and the next trading day's prices are checked again from scratch. |
| An order was asked to make a change that is not allowed | `illegal_transition` | This is a bug in sherwood, not something at the broker. The details name the order and the change it was asked to make. Leave the switch on, keep the log, and get the bug fixed before you reset. |
| An order's state at the broker is unknown | `unknown_order` | Sherwood sent an order, or a cancellation, and cannot tell what the broker did with it. That happens when the call failed or timed out, when the answer was about another order, when the broker answered with a status sherwood does not expect, or when it reported a fill sherwood cannot record (no price, more shares than ordered, or "filled" for only part of the order). A failed cancel of a resting stop, and a failed lookup of an unknown order during reconciliation, land here too. The order shows as unknown on the Orders view. Look it up in the broker's own app, and never place a replacement by hand until you know what happened to the first one. The next reconciliation, or the next 15-minute check in live mode, looks it up once and settles it. Reset once Orders shows it settled. |
| Sherwood's records and the broker's differ | `reconciliation` | See [Reconciliation differences](#reconciliation-differences), which lists the live-mode differences one by one, including lost stops and foreign orders. A partial fill that grew halts again at every check until you record it with `settle-order`, as in [A partial fill that grows](#a-partial-fill-that-grows); a reset alone does not clear it. |
| The daily loss limit was reached | `daily_loss` | Live mode only. The 15-minute check read the account's value from the broker, and its fall since the previous close reached the daily loss limit in force. The details give the value now, the previous close's value from sherwood's own records, the loss and the limit. Nothing more is sent until you reset, apart from protective stops, which stay at the broker. Look at what moved before you decide anything. If the previous close's value looks wrong, the records may lag the account: compare Portfolio with the broker's app. If you reset the same day and the loss still reaches the limit, the next check halts again. |
| You cancelled the protective stops | `stops_cancelled` | You confirmed, a second time, that the resting stops are cancelled. No stop is placed while it is active. Once you reset, the next 09:35 send places a stop on every holding again; reset only when you want them back. |

### Resetting the switch

Reset the switch only once you understand every active halt. Resetting clears all of them at once.

1. Open Today on one of your approval devices, the devices named in `approval_devices`. Any other device sees the page but gets "Only an approval device can do this" when it tries to reset.
2. Read every halt listed on the card.
3. Write why you are resetting it in the reason box, in 500 characters or fewer, and press **Reset**. The reason is kept with the reset, together with the device's address, and is never written to the log.
4. Today then says "The stop switch is reset." and shows your reason under the last reset.

A reset can be refused, and Today says which way:

- **No reason.** The reason box is required. Write one and try again.
- **Trading halted again after this page was loaded.** A new halt arrived while you were reading. Sherwood never clears a halt you have not seen, so it refused the reset. Read the newest halt, which is now on the page, and reset again if you are still sure.
- **The stop switch was already off.** Someone reset it first. Nothing changed.

After a reset, the same failure halts trading again if it happens again. Only exact repeats while the switch is on are folded into one halt.

## Reconciliation differences

Reconciliation compares sherwood's records with the broker's: holdings, cash and open orders. When they differ, sherwood throws the switch with the cause `reconciliation`, and the details say what differed. The broker is treated as the record, but only after you have looked at the difference. Sherwood never quietly copies the broker's numbers over its own.

Reconciliation runs at 09:00 US Eastern on every trading day, as the `reconcile` job, and again at the end of the after-close run that succeeds. Each run does two things, through the broker connection:

1. It settles every order whose answer was never recorded: each `unknown` order, each order left `submitted` by a send that never got its answer, and in live mode each order sherwood has open that the broker no longer lists (most often one that filled). It looks each one up at the broker, once, and records what the broker says. It never sends an order again. A fill the broker reports is recorded at the broker's own fill price; if the broker gives no price, sherwood does not guess one and reports a difference instead.
2. It compares the shares held in each stock, the cash and the open orders (which ones, their state and their filled shares) with the broker's.

Any difference throws the switch with this cause, and the details list every difference found. A failed broker call during reconciliation halts too, as a broker error, and the job's run fails, so you also get "A scheduled job failed". A made-up 09:00 run for a morning that has passed is skipped, because the next run compares the records as they are then.

The paper settlement in the after-close run also halts with this cause: if the day's paper fills cannot be settled (for example, a sell of more shares than are held, or holdings recorded after the close by a later import), nothing is written and trading halts.

In paper mode the broker is sherwood's own paper book, so a difference points at the records themselves, such as an import that landed after the close. In live mode the broker is your real account, and the [15-minute open-order check](#the-live-jobs-and-the-15-minute-checks) also compares open orders, and settles orders that have left the broker's book, all through market hours. It halts with this cause too.

### How fills reach sherwood's records in live mode

Live mode runs no paper settlement. A fill reaches sherwood in one of two ways:

- **When the order is placed.** If the broker answers the 09:35 send with a fill straight away, sherwood records it then, by the broker.
- **Later, by lookup.** An order that fills while it rests at the broker is found by the next 15-minute check or reconciliation, looked up, and recorded by reconciliation. The Orders view shows these fills "by reconciliation".

Either way the fill price is the broker's average price for the order, to the cent, and the holdings and cash move with the fill in the same step, as a new snapshot with the source `fills`. Sherwood never copies the broker's holdings over its own here; it applies the fill to its own records, so a real difference still shows.

### The differences you can see in live mode

| The details say | What happened | What to do |
|---|---|---|
| `<stock>: sherwood holds X shares, the broker Y.` | The holdings differ. Most often an order filled while sherwood was down, a transfer in or out, or a corporate action such as a split. | Find the cause in the broker's app. If the broker is right, re-import (step 3 below), then reset. |
| `Cash: sherwood has recorded X, the broker Y.` | The cash differs. A gap of a few cents after a sell is most likely the broker's regulatory fees. Dividends, interest, deposits and withdrawals move the cash too. Sherwood has no tolerance: any difference halts. | Check the broker's cash activity. Once it accounts for the gap, re-import, then reset. |
| `The broker has an open order (reference foreign-order:<id>) that sherwood does not have open.` | A foreign order: one sherwood did not place, such as an order you placed in the broker's own app, or one placed after the backup a restored database was made from. | If it is yours and you want it, know that it halts every check until it fills or is cancelled. Otherwise cancel it in the broker's app. Re-import if it filled, then reset. |
| `... the resting stop at $P is cancelled (or expired) at the broker, so <stock> has no stop there until the next 09:35 send places a new one, at no less than $P.` | A lost stop. The broker cancelled or expired a resting protective stop (a good-till-cancelled order does not rest for ever; the broker has said about 90 days, which is not verified), or someone cancelled it in the broker's app. | See [Lost stops](#lost-stops) below. |
| `... the broker reports N shares filled without a fill price sherwood can record.`, or, while the order is still open, `... the broker reports N shares filled, sherwood has recorded M.` | An order that had already partly filled has filled further, or filled with no price at all. The broker gives only one average price for the whole order, so sherwood will not guess the price of the extra shares. | Record the extra shares by hand with `settle-order`: see [A partial fill that grows](#a-partial-fill-that-grows) below. |
| `... the broker reports N shares filled, more than the order's M.` | The broker reports more shares filled than were ordered. | Check the order in the broker's app. This should never happen; keep the log. |
| `... shares sold at $P, which sherwood's holdings cannot take` | A sell filled for shares sherwood does not hold, or more than it holds. | Compare the holdings with the broker's app, re-import, then reset. |
| `... it is X in sherwood's records, Y at the broker.` or `... the broker does not list it as open.` | An order's state differs, and a lookup did not settle it. | Look the order up in the broker's app before you do anything else. |

### Lost stops

When a resting stop is gone from the broker without filling, the holding has no stop at the broker until the next 09:35 send. That send places a new one even while trading is halted, because protective stops always go out, and never at a lower price than the stop that was lost. If the stock already trades below that price by then, the new stop triggers at once and sells.

1. Look at the stock in the broker's app and decide whether you are happy for a new stop to go out at 09:35 at that price.
2. If you cancelled the stop on purpose and want the holding left without one, use the stop switch's second confirmation (**Cancel protective stops too**), which stops the 09:35 send placing any stop until you reset.
3. Otherwise reset the switch with a reason, and check at 09:35 that the new stop rests on the Orders view.

### A partial fill that grows

Sherwood records a partial fill when it sees one, at the broker's price. When the same order later fills further, the broker gives a new average price for the whole order, not a price for the extra shares, so sherwood halts rather than guess. This happens whether the first part filled when the order was placed or was found by a check. Until you record the extra shares, every 15-minute check and reconciliation finds the same difference, so a reset alone lasts only until the next one.

You record them by hand, with the prices the broker's app shows:

1. Leave the switch on while you look. Your holdings keep their protective stops, which still go out at 09:35.
2. Find the order number in the halt's details: they start `Order N (...)`.
3. In the broker's app, open that order and read its executions: how many shares have filled in all, and the price of each execution after the ones sherwood already has. The Orders view shows the fills sherwood has recorded.
4. On the NAS, record the new shares:

   ```bash
   docker exec -it sherwood sherwood settle-order N -filled TOTAL -price PRICE
   ```

   `TOTAL` is the order's filled shares in all, as the broker's app shows them, not just the new ones. `PRICE` is the new shares' price in dollars and cents. If the new shares filled at more than one price, run it once per execution, in order, each time with the running total.
5. Read what it prints. It records the new shares as a fill by the operator, moves the order to filled when every share has filled, and moves the holdings and cash with it, as a new snapshot with the source `fills`. It calls no broker and resets nothing.
6. Compare Portfolio with the broker's app: the shares, the average cost and the cash. Then reset the switch with a reason that names the order. The next check looks the order up, finds its fills match, and settles the rest of its state, such as cancelled when the broker ended it with part unfilled.

`settle-order` refuses, and writes nothing, when the order number or key is not an order's, when the order's state can't take a fill (it never reached the broker, or it has ended), when the total is no more than sherwood has recorded or more than the order's shares, when the price is zero or finer than a cent, when a limit buy's price is above its limit, or when a sell would take more shares than sherwood holds. It works for any order with a fill sherwood couldn't record, including an `unknown` order the broker filled with no price. It does not re-baseline anything else: if Portfolio and the broker's app still differ afterwards, use [When you see a reconciliation halt](#when-you-see-a-reconciliation-halt).

### When you see a reconciliation halt

1. Read the details on Today. They name the stock, the order or the amount that differs.
2. Compare them with the broker's own app (in live mode) or with the Orders view and the holdings sherwood shows (in paper mode). Check for an order that filled while sherwood was down, a fill the broker reported late, a transfer, a dividend paid as cash, fees on a sell, or a corporate action such as a split.
3. If the broker is right, record its holdings and cash with `docker exec -it sherwood sherwood import-holdings`, as in [Log in to the broker on the NAS](../how-to/log-in-to-the-broker-on-the-nas). That reads the broker, records a new snapshot of your holdings and cash, and changes no order. Re-import only once you understand the difference; an import hides it for good. In paper mode there is no broker to read, so look for the cause in the details instead: most often a holdings import that landed after the close.
4. If you cannot explain the difference, leave the switch on. Trading waits; your holdings stay protected by their resting stops.
5. Reset the switch with a reason that says what the difference was and what you did about it.

## The live jobs and the 15-minute checks

Once live trading is on, sherwood runs these jobs on every trading day, all in US Eastern time. Today shows each one's last run.

| Job | When | What it does |
|---|---|---|
| `reconcile` | 09:00 | The full reconciliation above: holdings, cash and open orders. |
| `send-orders` | 09:35 | Reads the account, sends the approved orders, and places or raises a protective stop on every holding, re-placing any lost stop. |
| `open-orders-check` | Every 15 minutes from 09:30, the last at 15:45 (12:45 on half days) | Lists the broker's open orders, settles by lookup every order that has left the broker's book or was never answered, and compares the open orders. Holdings and cash are left to the full reconciliation. In the log: `open orders agree with the broker`. |
| `daily-loss-check` | Same slots | Reads the account's value from the broker and compares the fall since the previous close with the daily loss limit in force. In the log: `daily loss under the limit`. |
| The after-close reconciliation | At the end of the after-close run that succeeds | The full reconciliation again. |

The two 15-minute checks never make up a missed slot: if sherwood was down, the next slot runs as usual. They share one "A scheduled job failed" notification a day, however many slots fail. A check fails when a broker call fails (which halts trading as a broker error) or when the day's loss cannot be measured, for example because a holding has no previous close or no daily loss limit is set. A loss that cannot be measured halts nothing, but the risk check refuses every order that needs the limit until it can be.

### When the broker is unwell

While the broker helper is down or the session has lapsed, sherwood treats the broker as unhealthy. Approvals' risk check then refuses buys and ordinary sells with "The broker is not answering, so only protective sells go through.", and the after-close proposal's preview refuses the same way. It recovers on its own once a broker call works again.

## Logging in again

The broker decides how long a session lasts, about a day, and sherwood never logs in on its own. When the session expires or the broker refuses it, every broker call fails the same way until you log in again. You get "Trading halted", one "Broker login needed", and one "A scheduled job failed" for the day's checks. Today's halt says "The broker session has expired or was refused: log in again with the broker helper's login command on the NAS, then reset the switch." Your resting stops stay at the broker meanwhile, but no new or raised stop goes out until the session works.

1. Log in again from a shell on the NAS, as in [Log in to the broker on the NAS](../how-to/log-in-to-the-broker-on-the-nas#1-log-in):

   ```sh
   docker exec -it sherwood python3 -m brokerhelper login
   ```

   Write down when the new session ends.

2. You don't need to restart sherwood. The next broker call uses the new session.
3. Reset the switch with a reason such as "Logged in again after the session expired".
4. Watch the next 15-minute slot on Today: both checks should succeed. If the halt came before 09:35 and the send failed, the day's approved buys expire that evening, as every unsent approval does.

If the session lapses every day, plan to log in each morning before 09:00.

## Restoring a backup

Every night at 02:00 US Eastern, sherwood copies its database into the backup folder as `sherwood-YYYY-MM-DD.db`, checks the copy by opening it and counting the rows of every table, and keeps the newest few (`backup.keep` in the settings file). A copy that fails its check is deleted straight away, so every copy in the folder has passed. The [settings reference](../reference/settings-and-strategy-files#backup) explains the settings.

Restore a backup when the database file is damaged (sherwood refuses to start and the log says the database is malformed, for example), or when you are [rolling back across a migration](#rolling-back-across-a-migration).

Whatever happened after the copy was made is not in it. When sherwood starts on the restored copy, it makes up the scheduled runs of the last few trading days, so prices, signals and the paper record catch up on their own, and you get one "Scheduled jobs ran late" notification. Anything you did by hand since the copy, such as approvals, resets and tightened limits, is gone and needs doing again. In live mode the broker has moved on without it: see [Restoring in live mode](#restoring-in-live-mode) before you start.

To restore:

1. Pick a time outside market hours if you can.
2. Stop the container:

   ```sh
   docker compose stop sherwood
   ```

3. Move the current database aside rather than deleting it, together with its `-wal` and `-shm` files if they are there:

   ```sh
   cd /path/to/your/appdata/sherwood
   mv sherwood.db sherwood.db.before-restore
   mv sherwood.db-wal sherwood.db-wal.before-restore
   mv sherwood.db-shm sherwood.db-shm.before-restore
   ```

   The last two may say there is no such file; that is fine. Never leave an old `-wal` file beside a restored database, because SQLite would try to apply it to the wrong file.

4. Copy the backup in as the database, and give it the owner and permissions the container expects (user 10001, or the `user:` set in your Compose file):

   ```sh
   cp /path/to/your/backup-share/sherwood/sherwood-2026-01-14.db /path/to/your/appdata/sherwood/sherwood.db
   chown 10001:10001 /path/to/your/appdata/sherwood/sherwood.db
   chmod 600 /path/to/your/appdata/sherwood/sherwood.db
   ```

   Choose the newest copy from before the trouble started.

5. Start the container and watch it come up:

   ```sh
   docker compose start sherwood
   docker logs --follow sherwood
   ```

   You should see `database ready`, then `sherwood started`. If the image is newer than the copy, `database ready` reports the migrations it applied to bring the copy up to date, which is expected.

6. If you are in live mode, press **Stop all trading** as soon as the app answers, then follow [Restoring in live mode](#restoring-in-live-mode).
7. On Today, check that the made-up job runs finish and the halts listed are the ones you expect. Once you are happy, you can delete the `.before-restore` files.

### Restoring in live mode

In live mode, the broker kept trading your account after the copy was made, and the restored database knows nothing of that. Three things can be out of step:

- **Orders the copy doesn't know.** An order sherwood placed after the copy, such as a new or raised protective stop or a buy sent at 09:35, still rests at the broker. Its reference is not in the restored database, so the next reconciliation or 15-minute check names it as `foreign-order:<id>` and halts.
- **Orders the copy thinks are still open.** An order that filled or was cancelled at the broker after the copy is still open in the restored records. The next check looks it up and settles it, recording any fill at the broker's price.
- **Stops the copy thinks are missing.** The 09:35 send places a protective stop on every holding it believes has none, even while halted. If the stop placed after the copy still rests at the broker, the holding would end up with two stops on the same shares.

So, after step 5 and with the switch on:

1. Open the broker's app and list its open orders. Compare them with the Orders view.
2. Cancel, in the broker's app and before the next 09:35, every open order the Orders view doesn't show as open. That includes protective stops: the 09:35 send puts a stop back on every holding.
3. Let the next check settle the orders the copy thinks are open. Outside market hours that is the 09:00 reconciliation, which runs while the switch is on.
4. Then, if the holdings or cash still differ, re-import them with `docker exec -it sherwood sherwood import-holdings`. Import only after step 3, so a fill isn't counted twice.
5. Reset once a reconciliation finds no difference, and check at 09:35 that each holding has one resting stop.

If the copy is older than your live-mode confirmation, sherwood starts with live trading off: it sends nothing and runs no reconciliation or checks, and Settings shows live mode as not confirmed. Do steps 1 and 2 in the broker's app first, then confirm on Settings, restart, and carry on from step 3.

## Rolling back across a migration

Sherwood's database changes are numbered migrations that only ever run forward. A new version may add one when it starts, and once a migration has run, an older version of sherwood refuses to start on that database, with a message like this:

```text
refusing to start: opening the database: store: migration failed: database records migration "0009_example.sql", which this build does not have; it was made by a newer build
```

That refusal is deliberate. An older version cannot know what the newer migration changed, so running it on that database could do the wrong thing. There is no "down" migration. Rolling back across a migration means going back to a database from before it, together with the version of sherwood that matches it.

**Before you upgrade to a version that adds a migration,** take a copy you know predates it. The night's backup works if it was made after your last change and before the upgrade. To be sure, stop the container and copy the file by hand:

```sh
docker compose stop sherwood
cp /path/to/your/appdata/sherwood/sherwood.db /path/to/your/backup-share/sherwood/before-upgrade.db
```

After a clean stop the `-wal` file is empty or gone, so the one file is the whole database. Then upgrade as usual.

**To roll back:**

1. Stop the container.
2. Check out the version you are going back to in your copy of the repo, so the next build makes the older image.
3. Restore the copy from before the upgrade, following steps 3 and 4 of [Restoring a backup](#restoring-a-backup).
4. Build and start the older version:

   ```sh
   docker compose up -d --build
   ```

5. Check the log for `database ready` and `sherwood started`, then follow steps 6 and 7 of [Restoring a backup](#restoring-a-backup).

Everything recorded since that copy is lost, as with any restore. Never edit a migration file that has already run, and never delete rows from `schema_migrations` to make an older version start: either one leaves a database that no version of sherwood describes correctly.

## Going back to paper mode

Go back to paper when live trading is not behaving as you expect and you want sherwood to keep running without real orders. If you only need to stop orders for now, the stop switch is enough and is quicker; switching mode is for longer.

Orders resting at the broker stay there when you switch. Paper mode builds no broker connection, so sherwood can neither see nor cancel them, and a protective stop left behind could still sell real shares without sherwood knowing. Cancel them while live trading is still on:

1. On Today, from an approval device, press **Stop all trading**. That declines every buy waiting for approval and cancels every open buy at the broker.
2. On the **Protective stops** card, tick the box and press **Cancel protective stops too**. That cancels every resting stop at the broker.
3. Check the broker's app: no order sherwood placed should still be open. Cancel any that is, and check Today for an `unknown_order` halt, which means a cancellation was not confirmed.
4. Set `mode: paper` in the settings file on the NAS, and restart:

   ```sh
   docker compose restart sherwood
   ```

5. Check the log for `sherwood started` with `"mode":"paper"`, and Today for the Paper banner. The live jobs and checks no longer run.
6. Reset the switch with a reason such as "Back to paper mode". In paper mode the next 09:35 send places paper stops in the paper book, not at the broker.

If sherwood can't start in live mode, or live trading was never on, the stop switch can't cancel at the broker: cancel every open order in the broker's app before you switch instead.

The change of mode ends the live-mode confirmation. To go live again later, set `mode: live`, restart, confirm again on Settings and restart once more, as in [Run the first live session](../how-to/run-the-first-live-session). Weeks spent in live mode don't count as paper trading.

## Stale feed or failed upload

The research feed comes from a job on your Mac, set up as in [Set up the research job](../how-to/set-up-the-research-job). A feed stays fresh for 36 hours, the threshold set by `feed.StaleAfter` in the code. After that, the Feed view shows a quiet note saying when the latest one arrived, and Portfolio and the approval cards show only the rules' own reasons, without the feed's notes and explanations. You get no notification, and nothing else changes: the rules still run, the risk check still applies, buys still wait for your approval, and protective stops still work.

To bring the feed back:

1. On the Mac, read the end of the job's log, `research-job.log` in the job's work folder. The last lines say which step stopped and why.
2. Fix what it shows. Most often the Mac was asleep or off the tailnet, the NAS was down, or the Claude login lapsed (run `claude`, then `/login`). The how-to's [troubleshooting table](../how-to/set-up-the-research-job#troubleshooting) covers every message.
3. Run the job again by hand with `launchctl kickstart -k gui/$(id -u)/com.example.sherwood-research`, and check the end of the log says `done`.

A failed upload stores nothing, so the Feed view keeps showing the previous feed, with the stale note once it is more than 36 hours old, until a good one arrives. An upload that worked but dropped some items still stored the rest; the log lists each dropped item and why.

## Known wording slips

- The comment on the `setting` column of `settings_changes`, in migration `0001`, gives `limits.max_daily_loss` as its example. The real key is `limits.daily_loss_limit`, and that is what sherwood records. Applied migrations are never edited, so the comment stays as it is.
