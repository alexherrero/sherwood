# Settings and strategy file reference

Sherwood reads two YAML files when it starts. The **settings file**, `settings.yaml`, says who may use the app and holds the app's own keys. The **strategy file**, `strategy.yaml`, holds your strategies, the risk check's limits and the watchlist. This page lists every field in both files: its type, whether it is required, the values it accepts, what it means, and the error you see when it is wrong.

The repo ships `settings.example.yaml` and `strategy.example.yaml` at its root. Every value in them, and on this page, is a placeholder that shows the shape of the file. None of it is a recommendation or anyone's real portfolio.

## Where the files live

Both real files live only in sherwood's application-data folder on the NAS, the same folder that holds the database. They are never committed: their names are in `.gitignore`, so a copy left in a checkout by mistake is not picked up by git.

The Compose file mounts that folder into the container at `/data`, and sherwood looks for the files there by default. You can point it elsewhere with a flag or an environment variable:

| File | Default path in the container | Flag | Environment variable |
|---|---|---|---|
| `settings.yaml` | `/data/settings.yaml` | `--settings` | `SHERWOOD_SETTINGS` |
| `strategy.yaml` | `/data/strategy.yaml` | `--strategy` | `SHERWOOD_STRATEGY` |

| File | Written by | Who can read it |
|---|---|---|
| `settings.yaml` | You, by hand, starting from `settings.example.yaml` | Its owner only. It holds the feed token and the web push private key. |
| `strategy.yaml` | The setup skill on your Mac, which you then copy to the NAS yourself | It holds no secrets, but keep it where only you can change it. |

The broker login is in neither file. The broker helper keeps it in its own owner-only file, which you fill in by running the helper's login command on the NAS. The Go application never reads that file.

## How sherwood reads both files

Sherwood checks both files every time it starts, settings file first, and **refuses to start if either one fails**. It does not fall back to defaults or skip a bad field. The container log then shows one error that lists every problem it found in that file, so you can fix them all before the next try. The strategy file is checked once the settings file passes. When both pass, the log records the strategy file's hash and the mode.

```text
refusing to start: strategy file is invalid: limits.max_order_value is missing; strategy "example-trend": rules[0] has unknown rule type "moon_shot" (known types: [moving_average_cross breakout momentum_ranking pullback fixed_stop trailing_stop size_fixed_amount size_by_risk])
```

Each problem names its field by its path in the file, such as `limits.max_order_value` or `notifications.web_push.contact`. A problem inside a strategy names the strategy, such as `strategy "example-trend": portfolio_share is missing`, or its position, such as `strategies[1].name is missing`, when it has no name yet.

A few rules apply to the whole of both files:

- **Unknown fields are refused.** A misspelled field name is an error, not something quietly ignored: `yaml: unmarshal errors: line 70: field extra_field not found in type config.strategyFile`.
- **A field may appear only once.** Writing it twice is refused: `mapping key "version" already defined at line 1`.
- **One document per file.** A second `---` document is refused: `the file holds more than one YAML document`. An empty file is refused: `the file is empty`.
- **Single values stay single.** A list or a mapping where one value belongs is refused: `line 3: expected a single value, not a list or a mapping`. YAML aliases (`*name`) are refused in the same places.
- **Secrets never appear in an error.** When the YAML library would quote the value it could not read, sherwood replaces it with `(value hidden)`, so a token pasted into the wrong field is never repeated in the log. Errors keep their line numbers.
- **These shape errors stop the check early.** A YAML mistake like the ones above is reported on its own; fix it, and the next start lists the field-by-field problems.
- **A missing file is a different error.** If the file cannot be read at all, the log shows the reason, for example `refusing to start: reading settings file: open /data/settings.yaml: no such file or directory`.

## Settings file

Every field in the table below is required, and none has a default. The one exception is the `backup` section, which the file may leave out; [its own section](#backup) explains what happens then.

```yaml
tailnet_login: you@example.com
approval_devices:
  - "100.64.0.10"
feed_token: "replace-me-with-a-long-random-feed-token"
notifications:
  web_push:
    vapid_public_key: "replace-me-with-the-vapid-public-key"
    vapid_private_key: "replace-me-with-the-vapid-private-key"
    contact: "mailto:you@example.com"
mode: paper
data_dir: /data
database_path: /data/sherwood.db
backup:
  dir: /backup
  keep: 14
```

| Field | Type | Required | Accepted values |
|---|---|---|---|
| `tailnet_login` | text | yes | A login with no spaces |
| `approval_devices` | list of tailnet addresses | yes | At least one; each an IPv4 address in `100.64.0.0/10` or an IPv6 address in `fd7a:115c:a1e0::/48`; no repeats |
| `feed_token` | secret text | yes | At least 32 characters |
| `notifications.web_push.vapid_public_key` | text | yes | Any non-empty value; notifications work only with a real key (see below) |
| `notifications.web_push.vapid_private_key` | secret text | yes | Any non-empty value; notifications work only with a real key (see below) |
| `notifications.web_push.contact` | text | yes | Starts with `mailto:` or `https://` |
| `mode` | `paper` or `live` | yes | Exactly `paper` or `live`, in lower case |
| `data_dir` | path | yes | An absolute path |
| `database_path` | path | yes | An absolute path |
| `backup.dir` | path | no, but every nightly backup fails without it | An absolute path to a folder that already exists |
| `backup.keep` | whole number | no; 7 when left out | 1 to 366 |

### `tailnet_login`

The one tailnet login sherwood accepts, written exactly as Tailscale Serve reports it. Tailscale Serve adds the caller's login to every request, and sherwood turns away any request from a different login, or with no login at all. The match is exact, letter case included.

- `tailnet_login is missing`
- `tailnet_login must not contain spaces`

### `approval_devices`

The devices that may approve a buy and reset the stop switch, listed by their tailnet addresses. List at least one, usually your phone. Tailscale Serve tells sherwood the address of the device that is calling but not its name, so sherwood identifies a device by its address.

Find each device's address in your tailnet's machine list, or by running `tailscale ip` on the device. Tailscale gives every device an IPv4 address between `100.64.0.0` and `100.127.255.255`, and an IPv6 address starting `fd7a:115c:a1e0:`; either one works. Put quotes around an IPv6 address. Write the address on its own, with no port, range or interface name. Two spellings of the same address count as a repeat.

Every other action, and every page, is open to any of your devices signed in with your tailnet login.

- `approval_devices is missing or empty; name at least one device, such as your phone`
- `approval_devices[0] must be a device's tailnet address, an IPv4 address in 100.64.0.0/10 or an IPv6 address in fd7a:115c:a1e0::/48`
- `approval_devices lists "100.64.0.10" more than once`

The message for a wrong entry does not repeat what you wrote, in case it was a secret pasted into the wrong field.

### `feed_token`

The token the research job presents to read the research export and upload a new feed. It can do nothing else. Use at least 32 random characters, and replace the example's placeholder: a token copied from the example would be public. Sherwood keeps it in a type that prints as a mask, so it never reaches a log.

- `feed_token is missing`
- `feed_token is too short; use at least 32 random characters`

### `notifications.web_push`

The keys for web push notifications to the phone app.

- **`vapid_public_key`** is the public half of the VAPID key pair. The phone app uses it to subscribe to notifications.
- **`vapid_private_key`** is the private half. It signs each notification, is kept as a secret like the feed token, and never reaches a log.
- **`contact`** is the address push services may use to reach the sender: a `mailto:` address or an `https://` page.

Generate your own key pair; the example's values are not keys. Both halves are base64url text without padding, the form most web push tools print: the public key is 87 characters (a 65-byte P-256 point) and the private key 43 characters (a 32-byte P-256 key). On the NAS, `openssl` makes a pair without any other tool:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out vapid.pem
openssl ec -in vapid.pem -outform DER | tail -c 65 | base64 | tr '/+' '_-' | tr -d '=\n'; echo   # vapid_public_key
openssl ec -in vapid.pem -outform DER | head -c 39 | tail -c 32 | base64 | tr '/+' '_-' | tr -d '=\n'; echo   # vapid_private_key
rm vapid.pem
```

`npx web-push generate-vapid-keys` prints a pair in the same form.

The checks below only make sure each field is present. Sherwood checks the keys themselves when it starts: if the private key cannot be read, or the public key is not its other half, sherwood still starts but logs `notifications are off` with a reason naming the field, and the Settings view says notifications are not set up. Changing the key pair later means every device has to turn notifications on again.

- `notifications.web_push.vapid_public_key is missing`
- `notifications.web_push.vapid_private_key is missing`
- `notifications.web_push.contact is missing`
- `notifications.web_push.contact must start with mailto: or https://`

### `mode`

Whether orders go to the paper ledger or to the broker. Sherwood starts in `paper`. There is no default, so you choose the mode on purpose.

- `mode is missing; set it to "paper" or "live"`
- `mode is "Paper", but it must be "paper" or "live"`

### `data_dir` and `database_path`

`data_dir` is the application-data folder as the container sees it, and `database_path` is the SQLite database file. Both must be absolute paths inside the folder the Compose file mounts, which is `/data` in the example, so the database is kept on the NAS rather than inside the container.

- `data_dir is missing`
- `data_dir is "data", but it must be an absolute path`
- `database_path is missing`
- `database_path is "sherwood.db", but it must be an absolute path`

### `backup`

Every night at 02:00 US Eastern, on every day of the week including weekends and holidays, sherwood copies its database into the folder named by `backup.dir`. It makes the copy while it keeps running, then opens the copy and checks that every table holds the same number of rows as the live database did at the moment of the copy. Only a copy that passes is kept, under the name `sherwood-YYYY-MM-DD.db`, and only then is the oldest copy beyond `backup.keep` removed. A copy that fails is deleted, so the older good copies always stay.

`backup.dir` is the folder as the container sees it. Mount your NAS backup share into the container in the Compose file, ideally from a different disk or share than the data folder, and point `backup.dir` at the place you mounted it. For example, with this line under `volumes` in `compose.yaml`:

```yaml
      - /path/to/your/backup-share/sherwood:/backup
```

the settings file says `dir: /backup`. The container's user (10001, unless you set `user:` in the Compose file) must be able to write to that folder.

Sherwood never creates the backup folder. If the share is not mounted, the backup fails and tells you, rather than quietly writing copies onto the container's own disk.

If you leave `backup` out, or leave out `dir`, sherwood still starts. It logs a warning at start-up, and every nightly backup then fails, so the Today view shows the backup job as failed and your phone gets "The nightly backup failed" once a night until you set the folder. `keep` defaults to 7 copies.

When a backup fails for any reason, your phone gets one "The nightly backup failed" notification for that night, however many times it is retried, and the Today view shows what went wrong in the backup job's last run. To restore from a copy, follow the [runbook](../operational/runbook#restoring-a-backup).

- `backup.dir is "backup", but it must be an absolute path`
- `backup.keep is 400, but it must be from 1 to 366`

## Strategy file

The strategy file has five top-level fields. `sectors` is needed only when `limits.max_sector_share` is set:

```yaml
version: 1
limits:
  max_stock_share: 0.10
  max_order_value: "100.00"
  max_orders_per_day: 3
  daily_loss_limit: "50.00"
  max_holdings: 20
  max_sector_share: 0.30
  max_limit_distance: 0.05
  buy_limit_margin: 0.01
  fractional_shares: false
  price_cross_check_tolerance: 0.02
  min_paper_weeks: 4
watchlist:
  - AAPL
  - MSFT
  - KO
sectors:
  AAPL: technology
  MSFT: technology
  KO: consumer-staples
strategies:
  - name: example-trend
    portfolio_share: 0.40
    stocks: [AAPL, MSFT]
    rules:
      - type: moving_average_cross
        settings:
          fast_days: 20
          slow_days: 50
```

### The file's hash is its version

When sherwood loads the strategy file it takes a SHA-256 hash of its exact bytes, and every signal records that hash. You can therefore always tell which version of your rules made which trade. Because the hash covers every byte, **any change to the file is a new version**: a new limit, a reordered list, an extra space or an edited comment all give a new hash.

### Numbers

The strategy file uses three kinds of number.

| Kind | Written as | Meaning | Errors |
|---|---|---|---|
| **Share** | A plain decimal such as `0.10` | A fraction of the whole, so `0.10` means 10% | `… must be greater than 0`, `… must be at most 1 (it is a share, so 0.10 means 10%)` |
| **Amount** | A plain decimal such as `"100.00"`, at most two decimal places | US dollars | `… must be greater than 0`, `… an amount has at most 2 decimal places` |
| **Whole number** | Digits only, such as `3` | A count | `line 6: expected a whole number such as 3` |

Shares and amounts are read from the exact text you wrote and never pass through a floating-point number, so `0.10` stays exactly `0.10`. Quoting is optional; the example quotes amounts to make them easy to tell apart from shares. Only plain decimals are accepted: `1e-1`, `.5`, `+0.5`, `NaN` and `.inf` are refused with `line 3: expected a plain decimal number such as 0.05 or 250`. A whole number refuses a fraction rather than rounding it, so `3.5` is an error and never quietly becomes `3`.

### `version`

| Type | Required | Accepted values |
|---|---|---|
| whole number | yes | `1` |

The format version of the file. This build of sherwood reads version 1 only.

- `version is missing`
- `version is 2, but this build reads version 1`

### `limits`

The risk check's limits. **The code holds no default for any limit.** Every limit except `max_sector_share` must be written in the file, and sherwood refuses to start while any one is missing, so no limit is ever set by accident. If the whole `limits` section is missing, the error says `limits is missing` and then names each required limit.

| Field | Kind | Required | Accepted values | Example |
|---|---|---|---|---|
| `max_stock_share` | share | yes | Above 0, at most 1 | `0.10` |
| `max_order_value` | amount | yes | Above 0, at most 2 decimal places | `"100.00"` |
| `max_orders_per_day` | whole number | yes | 1 or more | `3` |
| `daily_loss_limit` | amount | yes | Above 0, at most 2 decimal places | `"50.00"` |
| `max_holdings` | whole number | yes | 1 to 20 | `20` |
| `max_sector_share` | share | no | Above 0, at most 1; leave it out for no sector limit | `0.30` |
| `max_limit_distance` | share | yes | Above 0, below 1 | `0.05` |
| `buy_limit_margin` | share | yes | 0 or above, below 1, and no larger than `max_limit_distance` | `0.01` |
| `fractional_shares` | `true` or `false` | yes | `true` or `false` | `false` |
| `price_cross_check_tolerance` | share | yes | Above 0, below 1 | `0.02` |
| `min_paper_weeks` | whole number | yes | 1 or more | `4` |

A missing limit gives `limits.<name> is missing`, for example `limits.max_order_value is missing`. A value out of range names the field, its value and the rule, for example `limits.max_stock_share is 10, but it must be at most 1 (it is a share, so 0.10 means 10%)`.

- **`max_stock_share`** is the largest share of the portfolio one stock may hold after a buy fills. The risk check applies it to buys.
- **`max_order_value`** is the largest value of a single order, in dollars. It applies to buys and ordinary sells. Protective sells (stop-loss and trailing-stop sells) skip it, so a holding that has grown past it still keeps a stop for every share.
- **`max_orders_per_day`** is the most orders sherwood sends in one trading day. Protective sells, which cut a loss, do not count against it and are never blocked by it. Error when below 1: `limits.max_orders_per_day is 0, but it must be at least 1`.
- **`daily_loss_limit`** is the loss since the previous close, in dollars, at which all trading stops for the rest of the day. Protective sells still go through.
- **`max_holdings`** is the most holdings sherwood may have after a buy fills. The design's hard ceiling is 20, so the file may set a lower number but never a higher one: `limits.max_holdings is 25, but it must be from 1 to 20`.
- **`max_sector_share`** is the largest share of the portfolio in one sector, counting what is held, today's buys not yet filled, and the buy being checked. It is the one optional limit; leave the line out to turn the sector check off. When it is set, the [`sectors`](#sectors) map must give every stock on the watchlist and in a strategy a sector.
- **`max_limit_distance`** is how far a buy's limit price may sit from the last close, as a share of that close. A buy whose limit price is further away is refused.
- **`buy_limit_margin`** is how far above the previous close a buy's limit price is set, as a share of that close. It may be 0. It may not be larger than `max_limit_distance`, because then every buy would be refused: `limits.buy_limit_margin (0.06) is larger than limits.max_limit_distance (0.05), so every buy would be refused`.
- **`fractional_shares`** turns fractional share counts on. When it is `false`, share counts are rounded to whole shares.
- **`price_cross_check_tolerance`** is how far the two price sources may disagree on a stock, as a share of its price, before that stock gets no signals for the day.
- **`min_paper_weeks`** is the fewest weeks of paper trading before you can turn on live mode from the phone app.

### Changing a limit

Limits can move in only one direction from the phone, and that is on purpose.

- **Tightening from the phone.** The phone app's Settings screen shows each risk check limit with the strategy file's value and the value in force, and a form to tighten one: `max_stock_share`, `max_order_value`, `max_orders_per_day`, `daily_loss_limit`, `max_holdings`, `max_sector_share` (which may be set from the phone even when the file leaves it out) and `max_limit_distance` (never below `buy_limit_margin`). Only an approval device can tighten a limit. A tightened limit applies at once, to the proposal, the approval check and the 09:35 send alike, and is recorded in the database with its time, its old and new values, and the source `phone`. A value that is not tighter than the one in force is refused. The strategy file itself is not changed. `buy_limit_margin`, `fractional_shares`, `price_cross_check_tolerance` and `min_paper_weeks` are shown but change only in the file.
- **Loosening at the NAS.** To loosen a limit, edit the strategy file on the NAS and restart sherwood. A loosening is therefore always a deliberate act at the NAS, never a tap on the phone. The edit gives the file a new hash, so later signals show the new version. At start-up, sherwood records the new hash and every limit the edit changed, with the source `nas`, and the tightenings made from the phone under the earlier file stop applying: the limits in force are the new file's until you tighten one again.

### `watchlist`

| Type | Required | Accepted values |
|---|---|---|
| list of stock symbols | no | Well-formed symbols, no repeats; may be empty |

The stocks the rules may buy besides the ones you already hold. Adding a candidate from the research feed puts it here. A buy of a stock that is neither on the watchlist nor already held is refused by the risk check.

A stock symbol is written in capital letters and digits, starts with a letter, may have one `.` or `-` followed by a share-class suffix (as in `BRK.B`), and is at most 10 characters long.

- `watchlist[0] is "aapl", which is not a stock symbol (capital letters, digits, and one . or - for a share class)`
- `watchlist lists AAPL more than once`

### `sectors`

| Type | Required | Accepted values |
|---|---|---|
| mapping of stock symbol to sector name | only when `limits.max_sector_share` is set | Well-formed symbols; each sector a name of at most 40 characters on one line, with no space at either end |

Each stock's sector, for the sector limit. The risk check adds up what you hold in a buy's sector and refuses the buy if the sector would then be more than `max_sector_share` of the portfolio. Sherwood has no sector feed, so you give the sectors here. Name them however you like, but spell each one the same way every time: `technology` and `Technology` are two different sectors.

When `max_sector_share` is set, every stock on the watchlist and in every strategy's `stocks` needs a sector, and sherwood refuses to start until each has one. A stock you hold that is in neither list needs no sector to start, but the risk check refuses a buy of any stock without one. When `max_sector_share` is left out, `sectors` may be left out too; any entries you do write are still checked.

```yaml
sectors:
  AAPL: technology
  MSFT: technology
  KO: consumer-staples
```

- `sectors is missing, but limits.max_sector_share is set, so every stock on the watchlist and in a strategy needs a sector`
- `sectors has no sector for KO, but limits.max_sector_share is set`
- `sectors.KO is empty; give the stock's sector, such as technology`
- `sectors.KO is " consumer-staples", which starts or ends with a space`
- `sectors.KO is not a sector name (at most 40 characters, on one line)`
- `sectors has "ko", which is not a stock symbol (capital letters, digits, and one . or - for a share class)`

### `strategies`

| Type | Required | Accepted values |
|---|---|---|
| list of strategies | yes | At least one strategy; shares add up to at most 1 |

Each strategy is a named set of rules, the stocks it may trade, and its share of the portfolio. The `portfolio_share` values of all strategies together may not add up to more than the whole portfolio.

- `strategies is missing or empty`
- `the strategies' portfolio_share values add up to 1.10, which is more than the whole portfolio (1)`

Each strategy has these fields:

| Field | Type | Required | Accepted values |
|---|---|---|---|
| `name` | text | yes | Unique across the file |
| `portfolio_share` | share | yes | Above 0, at most 1 |
| `stocks` | list of stock symbols | yes | At least one well-formed symbol, no repeats |
| `rules` | list of rules | yes | At least one rule |

- **`name`** identifies the strategy in signals, orders and errors. Errors: `strategies[1].name is missing`, `strategy name "example-trend" is used more than once`.
- **`portfolio_share`** is the strategy's share of the portfolio. Errors: `strategy "example-trend": portfolio_share is missing`, `strategy "example-trend": portfolio_share is 0, but it must be greater than 0`.
- **`stocks`** are the symbols this strategy may trade, written like the watchlist's. Errors: `strategy "example-trend": stocks is missing or empty`, `strategy "example-trend": stocks[0] is "aapl", which is not a stock symbol (…)`, `strategy "example-trend": stocks lists AAPL more than once`.
- **`rules`** are the rules the strategy runs, described below. Error: `strategy "example-trend": rules is missing or empty`.

### `rules`

Each rule is one entry from sherwood's rule library together with its settings.

| Field | Type | Required | Accepted values |
|---|---|---|---|
| `type` | text | yes | One of the rule types below |
| `settings` | mapping of single values | no | Single values only; no lists or mappings |

The rule types are:

| `type` | What it does |
|---|---|
| `moving_average_cross` | Signals when a faster moving average crosses a slower one |
| `breakout` | Signals on a breakout above a recent high |
| `momentum_ranking` | Ranks stocks by momentum |
| `pullback` | Signals on a pullback within an uptrend |
| `fixed_stop` | A fixed stop-loss |
| `trailing_stop` | A trailing stop |
| `size_fixed_amount` | Sizes a position by a fixed amount |
| `size_by_risk` | Sizes a position by risk |

`settings` holds the rule's own settings, such as `fast_days: 20`. Each value is kept as the exact text you wrote, so a number never becomes a floating-point value. Which settings each rule type takes, the values each accepts and what each one means are in the [rule library reference](rule-library-and-back-tests). The loader confirms here that each setting is a single value, and sherwood then checks every rule's settings against the library before it starts.

- `strategy "example-trend": rules[0].type is missing`
- `strategy "example-trend": rules[0] has unknown rule type "moon_shot" (known types: [moving_average_cross breakout momentum_ranking pullback fixed_stop trailing_stop size_fixed_amount size_by_risk])`
- `line 24: expected a single value, not a list or a mapping`

## Related

- [Rule library and back-test reference](rule-library-and-back-tests) lists every rule type's settings and describes the back-tester.
- [Sherwood Design](../designs/sherwood) explains why the limits exist, how the risk check uses them, and why the broker login is kept apart.
- `settings.example.yaml`, `strategy.example.yaml` and `compose.example.yaml` at the repo root are commented examples with placeholder values.
