# Runbook

This page is for the moments when something needs you: trading has halted, sherwood's records and the broker's disagree, the database has to come back from a backup, a new version has to be rolled back, or the research feed has gone stale. Each section says what you will see, what to check, and what to do, in order. It grows as sherwood does; the live-trading part will add to it.

Every path, address, name and number on this page is a placeholder. Where a command names the data folder or the backup share on the NAS, use your own paths in their place. The container is called `sherwood`, as in the example Compose file.

## The notifications that lead here

Your phone only ever gets a few fixed words, never a stock, an amount or a reason. The details are in the app, over the tailnet.

| Notification | What it means | Where to look |
|---|---|---|
| Trading halted | The stop switch is on. | [Halts and their reset](#halts-and-their-reset) |
| A scheduled job failed | A job's run ended in an error. You get one for each job and date, however many times it is retried, and the after-close job and its two retries count as one. | Today, under the job's last run, which says what went wrong |
| Scheduled jobs ran late | Sherwood was not running when some jobs were due, so it is making them up now. You get one for each start that finds runs to make up, but not on the very first start of a new database. | Today, to see the made-up runs finish |
| The nightly backup failed | Last night's backup was not made, or the copy failed its check. You get one per night. | Today, under the backup job, then [Restoring a backup](#restoring-a-backup) if you need one |
| Broker login needed | The broker refused the saved session. | [Log in to the broker on the NAS](../how-to/log-in-to-the-broker-on-the-nas) |

Nothing calls the broker on a schedule yet, so the broker login notification starts arriving once live trading is built.

## Halts and their reset

### What a halt is

A halt turns the stop switch on. While it is on, sherwood sends no buys and no ordinary sells to the broker. Protective stops are still placed and raised, because they only ever cut a loss. The rules still run after the close and record their signals, so nothing is lost while you look into it. Orders that were approved but not sent while the switch was on expire at that evening's after-close run; they are never sent late.

The switch comes on in one of two ways: you press **Stop all trading** on the Today view, or sherwood throws it itself when something is in doubt. Either way you get one "Trading halted" notification, and Today lists every active halt with its cause, what sherwood saw, and when.

When you press **Stop all trading**, sherwood also clears the way for nothing to buy:

- Every buy waiting for your approval is declined, and every approved buy not yet sent is called off. The Orders view shows each one as stopped by the stop switch.
- Every open buy at the broker is cancelled, with one call each. If the broker does not confirm a cancellation, that order becomes unknown and trading halts for it too (see `unknown_order` below); sherwood never tries the call again.
- Sells waiting for approval are left alone. Nothing is sent while the switch is on, and they expire with their trading day.
- Resting protective stops stay in place, so your holdings stay protected.

While the switch is on, Today shows a **Protective stops** card. Tick its box and press **Cancel protective stops too** only if you mean to leave every holding without a stop. This second, separate confirmation cancels each resting stop at the broker, sets aside any stop not yet sent, and adds a halt of its own (`stops_cancelled`), so the 09:35 send places no new stop until you reset the switch. When you reset, the next 09:35 send puts a stop back on every holding. In live mode, until live trading is built, sherwood has no broker connection to cancel through: it declines the waiting buys and tells you to cancel open orders in the broker's own app.

Automatic halts do not decline or cancel anything. They only stop new orders being sent, and the approval check refuses a buy while the switch is on.

The same failure seen again while the switch is already on is recorded only once. That matters for the broker helper, which is restarted every second while it keeps failing: you get one halt and one notification, not one a second. A different cause, or the same cause with different details, always adds a halt of its own.

### What each cause means and what to check

| Today says | Cause | What to check before you reset |
|---|---|---|
| You pressed Stop all trading | `operator` | Nothing more than why you pressed it. The halt names the device you pressed it from. |
| The broker failed | `broker_error` | Read the details on Today. In live mode, run `docker logs sherwood` and look for `broker helper failed`; if it says the session expired or was refused, [log in again](../how-to/log-in-to-the-broker-on-the-nas). An order the broker rejected also halts with this cause, and the Orders view shows it as rejected with the broker's reason. |
| The price sources disagree on a stock you hold | `price_disagreement` | The details name each stock and both sources' prices for that date. Look the close up somewhere you trust. The rules made no signal for that stock that day, and the next trading day's prices are checked again from scratch. |
| An order was asked to make a change that is not allowed | `illegal_transition` | This is a bug in sherwood, not something at the broker. The details name the order and the change it was asked to make. Leave the switch on, keep the log, and get the bug fixed before you reset. |
| An order's state at the broker is unknown | `unknown_order` | A call to the broker failed or timed out after the order was sent, so sherwood cannot tell whether the broker has it. Look the order up in the broker's own app. Never place a replacement by hand until you know what happened to the first one. Reconciliation (below) settles these orders by looking them up. |
| Sherwood's records and the broker's differ | `reconciliation` | See [Reconciliation differences](#reconciliation-differences). |
| The daily loss limit was reached | `daily_loss` | The account lost more today than `limits.daily_loss_limit` allows. Look at what moved before you decide anything; this check runs in live mode only. |
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

1. It settles every order whose answer was never recorded: each `unknown` order, and each order left `submitted` by a send that never got its answer. It looks each one up at the broker, once, and records what the broker says. It never sends an order again. A fill the broker reports is recorded at the broker's own fill price; if the broker gives no price, sherwood does not guess one and reports a difference instead.
2. It compares the shares held in each stock, the cash and the open orders (which ones, their state and their filled shares) with the broker's.

Any difference throws the switch with this cause, and the details list every difference found. A failed broker call during reconciliation halts too, as every broker error does, and the job's run fails, so you also get "A scheduled job failed". A made-up 09:00 run for a morning that has passed is skipped, because the next run compares the records as they are then.

The paper settlement in the after-close run also halts with this cause: if the day's paper fills cannot be settled (for example, a sell of more shares than are held, or holdings recorded after the close by a later import), nothing is written and trading halts.

In paper mode the broker is sherwood's own paper book, so a difference points at the records themselves, such as an import that landed after the close. Live mode does not run reconciliation until live trading is built.

When you see a reconciliation halt:

1. Read the details on Today. They name the stock, the order or the amount that differs.
2. Compare them with the broker's own app (in live mode) or with the Orders view and the holdings sherwood shows (in paper mode). Check for an order that filled while sherwood was down, a fill the broker reported late, a transfer, a dividend paid as cash, or a corporate action such as a split.
3. If the broker is right, record its holdings and cash with `docker exec -it sherwood sherwood import-holdings`, as in [Log in to the broker on the NAS](../how-to/log-in-to-the-broker-on-the-nas). That reads the broker, records a new snapshot of your holdings and cash, and changes no order. In paper mode there is no broker to read, so look for the cause in the details instead: most often a holdings import that landed after the close.
4. If you cannot explain the difference, leave the switch on. Trading waits; your holdings stay protected by their resting stops.
5. Reset the switch with a reason that says what the difference was and what you did about it.

## Restoring a backup

Every night at 02:00 US Eastern, sherwood copies its database into the backup folder as `sherwood-YYYY-MM-DD.db`, checks the copy by opening it and counting the rows of every table, and keeps the newest few (`backup.keep` in the settings file). A copy that fails its check is deleted straight away, so every copy in the folder has passed. The [settings reference](../reference/settings-and-strategy-files#backup) explains the settings.

Restore a backup when the database file is damaged (sherwood refuses to start and the log says the database is malformed, for example), or when you are [rolling back across a migration](#rolling-back-across-a-migration).

Whatever happened after the copy was made is not in it. When sherwood starts on the restored copy, it makes up the scheduled runs of the last few trading days, so prices, signals and the paper record catch up on their own, and you get one "Scheduled jobs ran late" notification. Anything you did by hand since the copy, such as approvals, resets and tightened limits, is gone and needs doing again. In live mode, orders the broker filled since the copy are still at the broker, and reconciliation will show them as differences for you to review.

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

6. If you are in live mode, press **Stop all trading** as soon as the app answers, and reset it only once reconciliation shows sherwood and the broker agree.
7. On Today, check that the made-up job runs finish and the halts listed are the ones you expect. Once you are happy, you can delete the `.before-restore` files.

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

## Stale feed or failed upload

The research feed comes from a job on your Mac, set up as in [Set up the research job](../how-to/set-up-the-research-job). When no new feed has arrived for a while, the Feed view shows a quiet note, and approval cards show the rules' own reasons in place of Claude's explanations. You get no notification, and nothing else changes: the rules still run, the risk check still applies, buys still wait for your approval, and protective stops still work.

To bring the feed back:

1. On the Mac, read the end of the job's log, `research-job.log` in the job's work folder. The last lines say which step stopped and why.
2. Fix what it shows. Most often the Mac was asleep or off the tailnet, the NAS was down, or the Claude login lapsed (run `claude`, then `/login`). The how-to's [troubleshooting table](../how-to/set-up-the-research-job#troubleshooting) covers every message.
3. Run the job again by hand with `launchctl kickstart -k gui/$(id -u)/com.example.sherwood-research`, and check the end of the log says `done`.

A failed upload stores nothing, so the previous feed stays on the phone until a good one arrives. An upload that worked but dropped some items still stored the rest; the log lists each dropped item and why.

## Known wording slips

- The comment on the `setting` column of `settings_changes`, in migration `0001`, gives `limits.max_daily_loss` as its example. The real key is `limits.daily_loss_limit`, and that is what sherwood records. Applied migrations are never edited, so the comment stays as it is.
