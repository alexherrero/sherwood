# Log in to the broker on the NAS

This page logs sherwood in to your broker account and brings in your current holdings and cash for the first time. You do both from a shell on the NAS. Neither step places, changes or cancels an order: the login only saves a session, and the import only reads.

Sherwood must already be installed and running, as in [Install sherwood on the NAS](install-sherwood-on-the-nas). Every name, number and amount below is a placeholder.

## Before you start

You need:

- A shell on the NAS, as a user who can run `docker exec`.
- Your broker username and password, and your authenticator app if the account uses one.
- Your phone with the broker's own app, in case the broker asks you to approve the login there.

You type the password into the NAS's terminal, and only there. It is never saved and never written to a log, and no one else runs this step for you.

## 1. Log in

Run the login command inside the running container:

```sh
docker exec -it sherwood python3 -m brokerhelper login
```

The `-it` matters: the command asks questions, and it refuses to run without a terminal. It then:

1. Says where it will save the session, `/data/broker.yaml` inside the container.
2. Asks for your broker username, then your password. The password is not shown as you type.
3. Asks for a code from your authenticator app. Press Enter if you don't use one.
4. Logs in. If the broker asks you to approve the login in its app, or sends you a code by text or email, follow its prompts in the terminal. The broker's library also prints a few progress lines of its own, such as "Starting login process...".
5. Reads your account list. With one open account it says which one it is using, by the last four digits of its number only. With more than one it lists them that way and asks which one sherwood should read.

When it works, the last two lines look like this:

```text
Logged in. The session is saved in /data/broker.yaml, readable only by you.
The broker says it lasts until about 2026-01-02 21:00 UTC; log in again after that.
```

Write down when the session ends. After that time, anything that needs the broker answers `login_needed` until you run this command again. The broker decides how long a session lasts, and sherwood does not log in again on its own.

### Check what was saved

On the NAS, outside the container:

```sh
ls -l /mnt/cache/appdata/sherwood/broker.yaml
```

The file should be `-rw-------` and belong to the user the container runs as. It holds the session and which account to read. It does not hold your password or any second-factor secret. Never copy it into the repo, a chat or a note.

Scroll back through the terminal and run `docker logs sherwood`, and check that no password, code or token appears in either. The library's progress lines are outside sherwood's control; if one of them ever shows a secret, log in again to replace the session, and treat it as a bug to fix.

### If the login fails

Every failure starts `brokerhelper login:`, says what went wrong, and ends with "Nothing was written." Whatever was in `broker.yaml` before is left as it was.

| Message | What it means | What to do |
|---|---|---|
| `run this in a terminal, for example ...` | The command was run without `-it`. | Run it again with `docker exec -it`. |
| `the data folder /data does not exist` or `... is not a folder` | The application-data folder is not mounted where the helper expects it. | Check the `volumes` line in `compose.yaml`. |
| `/data/broker.yaml exists and is not a regular file; move it aside first` | Something other than a file is in the way. | Move it aside on the NAS and log in again. |
| `no username given` or `no password given` | An answer was left empty. | Run the command again. |
| `the broker did not accept the login, or it did not finish` | A wrong username, password or code, or the approval in the broker's app was declined or not given in time. | Check the details and try again. If it keeps failing, log in to the broker's own app first to check the account is fine. |
| `the broker refused the new session` | The login seemed to work, but the broker turned the session away straight after. | Wait a few minutes, then log in again. |
| `the login failed with ...`, followed by the name of an error | The broker's library stopped with an error during the login. Most often that is the network; it can also mean the broker changed its login. | Check the NAS's internet connection and try once more. If it fails the same way again, don't keep retrying: the helper's broker library may need an update. |
| `the broker's login answer was not in the expected shape` | The broker's login has changed in a way the helper does not understand. | Don't keep retrying. The helper's broker library may need an update. |
| `reading the account list: ...`, such as `the broker answered HTTP 503` or `the request failed with ...` | The login worked, but reading the account list failed, in the broker or on the network. | Wait a while, then log in again. |
| `the broker listed no open account` | The login worked, but the account list had no open account in it. | Check the account in the broker's app. |
| `that is not one of the listed numbers` | The account choice was not one of the numbers shown. | Run the command again and pick by number. |
| `could not write /data/broker.yaml: Permission denied` | The container's user cannot write the data folder. | Fix the folder's owner as in step 2 of the install page. |

## 2. Run the first import

With a fresh session saved, import your holdings and cash:

```sh
docker exec -it sherwood sherwood import-holdings
```

The command reads your holdings and cash from the broker, once each and with no retries. It records them in sherwood's database together, or not at all, and then prints what it recorded. It works the same in paper and live mode, and it never places an order. The output looks like this:

```text
Imported 2 holdings and the cash balance from the broker, as of 2026-01-02T21:05:00Z.

Symbol  Shares  Average cost  Opened
AAAA    10      12.34         2025-03-04
BBBB    0.5     100.005       2025-06-07

Cash: 1000.00
Nothing was ordered. Compare these figures with the broker's app before any strategy runs.
```

If you hold more than 20 stocks, a warning line says so. Every holding is still recorded, flagged as over sherwood's limit.

You can run the import again whenever you like. Each run records a new snapshot, and the newest one is what sherwood uses.

### Compare with the broker's app

Open the broker's app and check, holding by holding:

- **Every symbol** is there, and nothing extra is.
- **Shares** match, fractional shares included.
- **Average cost** per share matches. Sherwood keeps the broker's figure exactly, so it may show more decimal places than the app, which rounds to the cent.
- **Opened** is the date the broker created the position, in New York time. If you sold out of a stock and bought it back, it may be an earlier date than you expect; make a note of any you see.
- **Cash** matches what the app calls your cash. Brokers show several cash figures, such as cash, buying power and withdrawable cash. If `Cash:` matches a different one, or none of them, note which. Sherwood may be reading the wrong one of the broker's cash fields, and that has to be fixed before live trading.

Then open the Portfolio view on your phone. It should show the same holdings and cash. Values stay "Not available" until sherwood's after-close price job has stored a checked closing price for each stock.

Finally, check that nothing was ordered: the broker's app should show no new order.

### If the import fails

A failed import prints one line that starts `sherwood: import-holdings: nothing was written:` and exits with status 1. Nothing is recorded, so the previous import, if any, stays current. After the prefix comes the reason:

| Reason | What it means | What to do |
|---|---|---|
| `broker helper: holdings: login_needed: nobody has logged in to the broker yet` | There is no `broker.yaml`. | Do step 1. |
| `... login_needed: the broker session has expired` | The saved session has passed its end time. | Log in again (step 1), then import again. |
| `... login_needed: holdings: the broker refused the saved session` | The broker no longer accepts the session, even if it hasn't reached its end time. | Log in again, then import again. |
| `... broker_settings: ...` | `broker.yaml` can't be used: for example it `belongs to another user`, `can be read or changed by other users; run chmod 600 on it`, `cannot be opened`, or has a line the helper can't read. | Fix its owner or mode on the NAS, or move it aside and log in again. |
| `... broker_error: ...` | The broker or the network failed, for example `the broker answered HTTP 503`. | Wait and try once more later. Don't run it in a loop. |
| `... bad_broker_reply: ...` | The broker answered, but not in the shape the helper knows. | Stop. The broker may have changed, and the helper needs a look before you import again. |
| `broker helper sent a bad reply: ...` | A figure from the broker could not be read exactly, such as a share count with more than 6 decimal places, or a field sherwood does not know. | Stop, and keep the message: sherwood needs a change before this account can be imported. |
| `holdings: invalid: ...` | A holding sherwood refuses, such as `"AB CD" is not a stock symbol` or a symbol `listed twice`. | Stop, and keep the message. Check that holding in the broker's app. |
| `holdings: snapshot older than the current one` | The broker's time for this read is earlier than the last import's. | Check the NAS's clock, then try again. |
| `broker helper did not start` or `the broker helper stopped: ...` | The helper process could not run inside the container. | Rebuild the image with `docker compose up -d --build` and try again. |
| `reading settings file: ...` or `settings file is invalid: ...` | The import reads the same `settings.yaml` as sherwood itself. | Fix the file as the [settings reference](../reference/settings-and-strategy-files) describes. |

None of these is fixed by running the import again straight away, apart from a short broker outage. When in doubt, stop and read the reason.

## What's next

Sherwood now reads your real holdings and cash, and still trades on paper. Once the paper record is long enough, [Run the first live session](run-the-first-live-session) turns live trading on. When the session expires later, the [runbook](../operational/runbook#logging-in-again) says how to log in again and reset the stop switch.
