---
title: Sherwood Design
status: final
visibility: published
author: repository owner
contributors: []
created: 2026-11-15
updated: 2026-11-15
last_major_revision: 2026-11-15
prd:
project:
---

# Sherwood Design

## Context

### Objective

Sherwood is a small investment tool that runs on a home NAS and that you open from your phone. It keeps a portfolio of at most 20 stocks, keeps a feed of possible next picks written by an AI, and places trades through a broker by fixed, written rules. It exists so that one person can run a spread of rule-based trades safely, with hard limits that hold even when a rule or the AI is wrong. This design describes the rewrite that replaces the archived v1 code.

### Background

The first sherwood was a Go proof-of-concept, kept in [`v1/`](https://github.com/alexherrero/sherwood/tree/main/v1). An audit of it found mistakes that could not be repaired in place: money held as floating point, a risk check that was optional and unwired, and order handling with no protection against a retry buying twice. A second design, [Sherwood v2](https://github.com/alexherrero/sherwood/blob/fa1dfec036dc9610a7fc5766976c2d385d43d485/wiki/designs/sherwood-v2.md), answered that audit with a large platform: the whole S&P 500 on streaming data, crypto, Postgres and an AI chat agent. It was never built. This design starts again from scratch at a much smaller size, and it keeps the v2 design's foundations and the lessons of the [v1 audit](../../docs/retros/v1-tech-debt.md).

Sherwood runs in one Docker container on the home NAS. You reach it only over your Tailscale tailnet, through Tailscale Serve, the same way the NAS serves its other apps; nothing opens on the router. Trades go to Robinhood, which has no official API for stocks, so sherwood uses an unofficial route that can break without warning. That fact drives much of the design: the broker sits behind one small adapter that can be swapped, and any broker error stops trading. The nightly AI research runs on a Mac under your existing Claude subscription and sends its results to the NAS. The repo is public, so it holds the engine and the rule library; your strategies, limits, watchlist and holdings live only in the NAS's own configuration and database.

The running cost is meant to be zero. Prices are free end-of-day data and news is free (company filings and public news feeds). The rules trade a few times a week at most, so nothing needs minute-by-minute prices. The AI runs on a subscription you already pay for, and the NAS is already running. One person builds and runs it, so the design favours few moving parts over flexibility.

## Design

### Overview

Sherwood runs in one container on the NAS, and you use it through an app installed on your phone. Every trading day, after the market closes, it fetches that day's prices, runs your written rules over them, and turns what the rules say into signals: buy this, sell that, hold. Each signal becomes an order record. A buy waits on the phone app until you tap to approve it. A protective sell, such as a stop-loss, goes through on its own, so a fast drop is not held up waiting for you. Every order, whatever made it, passes through one risk check just before it is sent, and the risk check enforces hard limits on how much any stock, any order and any day may carry.

Alongside the trading, a research job on the Mac reads the news and writes a feed: short, sourced cases for candidate stocks, notes on each stock you hold, and a plain-words explanation of each signal the rules made. The feed is data and nothing else. **The AI never creates, approves or sends an order.** Only the rules create orders, only you approve buys, and only the execution layer talks to the broker. A candidate in the feed becomes tradable only when you add it to your watchlist with a tap, and even then the rules decide whether to buy it and you approve the buy.

Sherwood starts in paper mode. In paper mode it simulates its own fills against the real prices of each day as they arrive, because Robinhood has no practice account. When the paper record has held up for several weeks, you switch it to live mode with a small amount of money. A stop switch on the phone app halts everything at any time, and any broker error, unexplained difference with the broker, or breach of the daily loss limit throws the same switch automatically.

### Infrastructure

Sherwood is a Go application with a small Python helper for the broker calls, packaged together as one Docker container and run with Docker Compose on the primary NAS, which runs Unraid. The Go application does everything except talk to Robinhood: it serves the phone app, runs the scheduled jobs, the rules engine, the risk check and the execution layer. The Python helper is the one piece that talks to Robinhood, because the unofficial route is maintained as a Python library. Its database is a single SQLite file in the container's application-data folder on the NAS. The host's Tailscale gives the app its own HTTPS port on the NAS's Serve name; the container publishes its web port on the host's loopback address only, so Tailscale Serve is the one way in.

| Piece | Where it runs | What it does |
|---|---|---|
| Go application | NAS, in the `sherwood` container | Serves the phone app, runs the scheduled jobs, the rules engine, the risk check and the execution layer, and owns the database. |
| Broker helper (Python) | NAS, in the `sherwood` container | Makes the Robinhood calls for the execution layer over a local socket, and runs the one-time broker login. It is the only process that reads the broker secrets. |
| Phone app | Your phone, installed from the page | An installable web app on your home screen: approvals, the stop switch, the portfolio, the feed and notifications. |
| SQLite database | NAS application-data folder | Holds prices, signals, orders and their history, fills, holdings, the feed, job runs and settings changes. |
| Broker settings file | NAS application-data folder, owner-only | Holds the broker login, its second-factor setup and the session token, readable only by the broker helper. It is never in the repo, the vault, a log or a test fixture. |
| App settings file | NAS application-data folder, owner-only | Holds the allowed tailnet login, the approval devices, the feed token and the notification keys. |
| Strategy file | NAS application-data folder | Holds your strategies, limits and watchlist, written by the setup skill. |
| Tailscale Serve | NAS host | Gives the phone app HTTPS on the tailnet and tells sherwood which tailnet user is calling. |
| Research job | Mac (launchd) | Runs the AI research under your Claude login, then uploads the feed to the NAS. |
| Setup skill | Mac, run by you | Interviews you and writes a strategy file for this install. |

The jobs run on a fixed schedule in the exchange's own time zone (US Eastern), on trading days only, using an exchange calendar so holidays and half days are handled.

| When | Job | Needs the Mac? |
|---|---|---|
| 09:00 ET, trading days | Reconcile with the broker: compare holdings, cash and open orders; halt on any difference. | No |
| 09:35 ET, trading days | Send today's approved buys as day limit orders. | No |
| Every 15 minutes in market hours | Check open orders at the broker; in live mode, check the daily loss limit against the account value. | No |
| 16:30 ET, trading days | Fetch the day's prices, cross-check them, value the portfolio, run the rules, create signals, update protective stops, and publish the research export. | No |
| Nightly, while the Mac is awake | Research job: read the export and the news, write the feed, upload it. | Yes |
| Every 15 minutes | Take in any uploaded feed, validate it and store it. | No |
| 02:00 local, daily | Back up the database to the NAS backup share. | No |
| When you tap | Approve or decline a buy, add a candidate to the watchlist, throw or reset the stop switch, tighten a limit. | No |

The trading path depends only on the NAS, the broker and the price sources. When the Mac is asleep the feed goes stale, and trading carries on with the rules' own short reasons in place of the AI's explanations.

### Detailed Design

#### Money, time and determinism

All money, prices and share counts are exact decimal values, never floating point: a decimal type in the Go application and Python's `Decimal` in the broker helper. SQLite has no decimal type, so they are stored as text and read back as decimals; storing them as SQLite numbers would quietly turn them into floats. They travel as strings in JSON, including between the Go application and the broker helper. Rounding follows one written policy: prices to the cent, share counts to whole shares unless fractional shares are turned on in the strategy file.

Nothing in the rules, the risk check or the scheduler reads the wall clock directly. They are given a clock, and the tests give them a fake one. A rules run is a pure function of the day's prices, your holdings, the strategy file and the trading date, so running it twice on the same inputs gives the same signals and the same order keys. Each job run is recorded with its trading date and a hash of its inputs. If the container was down when a job should have run, it catches up in order on start, and a catch-up never sends a buy into a session other than the one it was approved for.

#### The rule library and strategies

The code holds a library of rule types with no settings of their own: for example a moving-average cross, a breakout above a recent high, a momentum ranking, a pullback within an uptrend, a fixed stop-loss, a trailing stop, and position-sizing rules by fixed amount or by risk. A strategy is a named set of these rules with their settings, the stocks it may trade, and its share of the portfolio. Strategies live in the strategy file on the NAS, never in the repo.

Each rule produces one of four outcomes for a stock on a day: buy, add, trim or sell, or no change. The same outcome drives the hold, add or trim suggestion shown beside each holding, so the suggestions are rule-made too. Every signal carries a short mechanical reason written by the rule itself (for example, "closed above its 50-day high"), so a signal is understandable even with no AI note.

Sherwood refuses to start if the strategy file fails validation. Validation checks the file against a schema, checks that every rule type exists, that every limit in the next section is present, and that the strategies' shares add up to no more than the whole portfolio. Each signal records the hash of the strategy file that made it, so you can always tell which version of your rules made which trade.

The back-tester runs the same rule functions over past prices, with the paper broker filling the orders, and reports return, drawdown, win rate and turnover for each strategy. A strategy should show a back-test before it trades on paper.

#### The setup skill

Strategies are generated per install by an AI setup skill that lives in the repo and runs on the Mac. It asks you about your goals, how much you want to put in, how much loss you can stand and which sectors you want covered. It then writes a strategy file from the rule library, runs the back-tester over it and shows you the results. You copy the file to the NAS yourself. The skill never sees the broker login and never talks to the NAS.

The skill aims for broad coverage: strategies spread across sectors and across kinds of trade (trend-following, pullback buying, defensive holdings), so the portfolio does not depend on one market direction. Its output is a starting point for your judgment, and nothing it writes is financial advice.

#### Orders and their states

Every order is a record that moves through clear states, and every change is appended to an order-history table, so the history of any order can be replayed. The current state is also kept on the order row for quick reads.

| State | Meaning |
|---|---|
| `proposed` | A rule made a signal and the order record exists. |
| `awaiting_approval` | A buy is waiting for your tap. |
| `approved` | You approved it; it waits for the next send window. |
| `declined` | You declined it, or it expired unapproved at the end of its trading day. |
| `blocked` | The risk check refused it; the reason is stored. |
| `submitted` | The execution layer sent it to the broker. |
| `accepted` | The broker acknowledged it. |
| `partially_filled` | Some shares have filled. |
| `filled` | All shares have filled. |
| `cancelled` | It was cancelled, by you, the stop switch, or the broker. |
| `rejected` | The broker refused it. |
| `expired` | A day order ended unfilled. |
| `unknown` | A send or a status check failed and the order's true state is unclear. |

Only the transitions in the state machine are allowed, and an illegal one is an error that halts trading. A protective sell is a sell made by a stop-loss or trailing-stop rule to limit the loss on a holding. Protective sells skip `awaiting_approval` and go from `proposed` straight to the risk check. Every other sell, such as a trim or a rule's ordinary exit, waits for your approval as a buy does.

**Each order has an idempotency key built from its strategy, stock, side, signal kind and trading date,** and the database holds a unique constraint on it. Re-running the signal job, restarting the container or replaying a day therefore finds the existing order instead of making a second one. The key is passed to the broker as the client reference wherever the route accepts one.

A failed send is never retried automatically. If the broker returns an error or a call times out, the order moves to `unknown` and trading halts. The next reconciliation looks the order up at the broker and settles its state, and you reset the stop switch once you have seen the result. This follows the rule that any broker error stops trading, and it means a retry can never buy twice.

Buys are sent as day limit orders, with the limit a small, configured margin above the previous close. Approvals expire at the end of the trading day they were made for. Protective stops rest at the broker as standing stop orders, placed when a holding is opened and moved after each close when a trailing rule raises them. Resting the stop at the broker means it protects the holding during the day and even while the NAS is down.

#### The risk check

**Every order passes through the risk check immediately before it is sent, and the execution layer cannot be built without one.** Buys are also checked when they are proposed, so you see on the approval card whether a buy would pass. The limits' values live in the strategy file on the NAS; the code ships no default values and refuses to start if any limit is missing.

| Check | Applies to |
|---|---|
| The largest share of the portfolio one stock may hold after the order fills | Buys |
| The largest value of a single order | All orders |
| The most orders sent in one trading day | All orders except protective sells |
| The daily loss limit: when the portfolio has lost this much since the previous close, all trading stops for the rest of the day | All orders except protective sells |
| At most 20 holdings after the order fills | Buys |
| The largest share of the portfolio in one sector, when set | Buys |
| Cash only: a buy may not cost more than available cash; no margin, no short selling, no options | Buys |
| The stock is on the watchlist or already held | Buys |
| The limit price is within a configured distance of the last close | Buys |
| No buying and selling the same stock on the same day | All orders |
| The stop switch is off and the broker is healthy | All orders except protective sells |
| The market is open, or the order is a resting stop | All orders |

Protective sells pass a shorter list on purpose. A sell that cuts a loss should go through on a bad day, so the daily loss limit, the order count and the stop switch do not block it. It still has to be a sell of shares you hold, at no more than the shares you hold.

You can tighten any limit from the phone app, and the change applies at once. Loosening a limit means editing the strategy file on the NAS and restarting, so a loosening is always a deliberate act at the NAS. Every limit change is recorded with its time and its source.

#### The stop switch and automatic halts

The stop switch is a large button on the phone app. Throwing it halts every strategy, declines every buy awaiting approval and cancels every open buy order at the broker. Resting protective stop orders stay in place, because cancelling them would leave your holdings unprotected; a second, separate confirmation cancels those as well. While the switch is on, rules still run and record their signals so you can see what they would have done, but nothing is sent.

Sherwood throws the switch itself when any broker call fails, when an order's state is `unknown`, when reconciliation finds a difference between its records and the broker's, when the daily loss limit is reached, when the price sources disagree about a held stock, or when a state-machine transition is illegal. Each halt is recorded with its cause and evidence, and you are notified. Only you can reset the switch, from the phone app, and the reset is recorded with a reason.

#### The broker adapter

The broker sits behind one small interface: place an order, cancel an order, look up an order, list open orders, list holdings and read cash and account value. There are three implementations.

| Adapter | Used for |
|---|---|
| Paper | Paper mode and back-tests. It fills orders against each day's real prices: a buy fills at the next open if the open is at or below its limit, otherwise at the limit if the day traded through it; a stop fills when the day's low reaches it, at the stop or at the open if the stock opened below it. |
| Robinhood | Live mode. A thin Go client that passes each call to the broker helper, which makes it through an unofficial Python client library pinned to one reviewed version. |
| Fake | Tests. It returns scripted results and errors and never touches a network. |

The paper and fake adapters are written in Go. The broker helper is a small Python process that the Go application starts and watches inside the container. They talk over a Unix socket in the container, one JSON request and one JSON reply per call, and the helper offers only the adapter's operations. The helper never retries a call; it reports every error back to the Go application, which halts trading. If the helper stops or stops answering, that counts as a broker error.

The mode is a setting on the NAS. The Go application starts the broker helper only when the mode is live, and switching to live mode needs both the setting and a confirmation on the phone app. Holdings are first imported through the adapter in a read-only step, before any strategy runs.

You enter the Robinhood login and its second-factor setup yourself, on the NAS, by running the broker helper's one-time login command inside the container. The command writes the credentials and the session token to the owner-only broker settings file, which only the helper reads. When the session expires the helper reports an error, trading halts, and the phone app tells you to log in again. No agent runs this command or reads the file.

Reconciliation runs before each open and after each close. It compares holdings, cash and open orders with the broker's, and any difference halts trading until you look at it.

#### Prices and news

Daily prices come from free end-of-day sources behind a small price-source interface, with one primary source and one cross-check source. If the two disagree on a held or watched stock by more than a configured amount, that stock gets no signals that day and a held stock halts trading for review. A missing day of prices means no signals that day; it never means trading on stale prices. Rules use split-adjusted prices, and a sudden change in a holding's share count at the broker halts trading until you confirm the split.

News is read only by the research job on the Mac, from company filings and public news feeds. The NAS fetches prices and nothing else.

#### The research feed

The research job runs nightly on the Mac under launchd, as other scheduled jobs on the Mac do. It works in three steps.

1. **Fetch the export.** A plain script downloads the research export from the NAS using the feed token. The export holds the symbols you hold and watch, their recent prices and today's signals with their rule reasons. It holds no balances, share counts or account details.
2. **Research.** Claude runs headless under your Claude login with a research skill from the repo. It is given only tools to read its inputs, fetch web pages and search the web, and to write files in its own output folder. It has no shell, and no tool that can send anything to the NAS. It writes the feed as a JSON file.
3. **Upload.** The same plain script validates the file against the feed schema and uploads it to the NAS's feed endpoint with the feed token.

The feed holds three kinds of item: a candidate case (a stock not held, with a short case for it and its sources), a holding note (what is happening to a stock you hold, with sources), and a signal explanation (a plain-words account of why a rule made a signal). Every item must cite at least one source link, and items without one are dropped. The phone app shows each link with its domain, so you can check where a claim came from.

The NAS's feed endpoint accepts only this schema and can only add feed rows. The feed module in the code has no path to the execution layer, and a test checks that import boundary. All feed text is shown as escaped plain text, because news pages can carry text written to manipulate an AI, and that text must never become active content on your page.

#### The phone app

You use sherwood through a web app installed on your phone's home screen. The Go application serves it as a progressive web app: server-rendered pages built for a phone screen first, a little JavaScript, an app manifest with an icon, and a service worker. Installed, it opens full-screen like a native app. Tailscale Serve's HTTPS is what lets the phone install it. The service worker caches only the app's shell (its layout, styles and icon); every view loads its data fresh from the NAS, and every action needs a live connection, so you never approve a buy from stale numbers. It has six views.

| View | What it shows |
|---|---|
| Today | Paper or live mode in a clear banner, the stop switch, any halt and its cause, and the last run of each job. |
| Approvals | Each buy waiting for you: the stock, amount, limit price, the rule's reason, the AI's explanation if there is one, and the risk check's verdict. Approve and Decline buttons, one order per tap. |
| Portfolio | Your holdings (at most 20), their value and gain or loss, each one's rule-made hold, add or trim suggestion, and its latest AI note. |
| Feed | Candidate cases with their sources, and an "Add to watchlist" button on each. |
| Orders | Every order and its full state history. |
| Settings | The limits (read-only, with a Tighten action), the mode, and the paper record. |

You are notified on your phone when buys are waiting and when trading halts. Notifications are web push messages to the installed app, which both iPhone and Android support for home-screen web apps. A push message travels through the phone maker's push service, encrypted, so it carries only a short alert ("2 buys waiting", "Trading halted") and never amounts, holdings or reasons; tapping it opens the app over the tailnet. The notification channel sits behind a small interface, so another can be added later.

#### Who may call the app

The container's port is published on the NAS host's loopback address only, so every request arrives through Tailscale Serve. Serve adds the calling tailnet user's login to each request, and sherwood accepts only the login set in its settings file. Every action that changes anything is a form POST with an anti-forgery token, and reading a page never changes anything. Approving a buy and resetting the stop switch can additionally be limited to named tailnet devices, so that by default only your phone can approve.

#### Storage and migrations

The database schema is managed by numbered SQL migrations from the first commit. They run forward-only at start, inside a transaction, and a failed migration stops the container from starting. SQLite runs in write-ahead mode with one writer, the app itself. A nightly online backup copies the database to the NAS's backup share, and the backup is checked by opening it and counting rows.

## Alternatives Considered

**Build the v2 design as written.** The v2 design aimed at the whole S&P 500 on streaming data, crypto, Postgres and an in-app AI chat. That is far more than a 20-stock portfolio needs, and you decided to rewrite from scratch at a smaller size. This design keeps the v2 parts that guard money (decimals, the order state machine, the mandatory risk check, deterministic scheduling, migrations from the first commit, the AI's distance from the broker) and leaves the rest.

**One language for everything.** An all-Python build would use the unofficial Robinhood library directly. An all-Go build would have to re-implement and maintain Robinhood's login and second-factor flow by hand, which is the most fragile part of the whole tool. Sherwood uses Go for the application, as v1 and the v2 design did, because Go makes a small, self-contained server with strong typing around money and orders. It uses Python only for the broker calls, where the maintained library lives. Keeping Python to a small helper behind a socket also keeps the broker secrets in one process the rest of the application cannot read.

**A native phone app.** A native iPhone or Android app would need an app-store build, signing and a separate codebase. An installed web app gives a home-screen icon, a full-screen view and push notifications from the same server that runs everything else.

**Postgres instead of SQLite.** Postgres adds a second container, a second password and a second thing to back up. One process writes a few hundred rows a day, which SQLite handles easily, and a single file is simple to back up and restore.

**A broker with an official API.** Alpaca, Schwab and Interactive Brokers all have official trading APIs, and Alpaca has a practice account. You chose to trade in your existing Robinhood account, knowing the risks. The swappable adapter is the answer to those risks: moving to an official broker later means writing one adapter and changing one setting.

**Watching stops in the app instead of resting them at the broker.** The app could watch prices and send a sell when a stop is reached. With free end-of-day prices it would act a day late, and it would not act at all while the NAS was down. A resting stop order at the broker acts during the day and needs nothing from sherwood.

**Running the AI in the container on the NAS.** The v2 design mounted the Claude login into the container. You chose to run the research on the Mac, as other AI jobs already do, which keeps the Claude login off the NAS and keeps the AI physically away from the broker secrets.

**Letting the AI post to the app directly.** The research session could call the NAS itself. Splitting it so that Claude only writes a file, and a plain script uploads it with a feed-only token, means a confused or manipulated AI session has no way to reach anything but the feed.

**Letting AI candidates enter the rules automatically.** The rules could evaluate every candidate in the feed. Requiring your tap to add a candidate to the watchlist keeps a human between what the AI suggests and what the rules may buy.

## Dependencies

| Dependency | What sherwood needs from it | Risk |
|---|---|---|
| Unofficial Robinhood client library (Python) | Login, second factor, orders, holdings, cash, used only by the broker helper | High. It can break without warning, and Robinhood may object to automated access. Pinned to one reviewed version behind the adapter. |
| Go toolchain and a few Go libraries | A decimal type, a pure-Go SQLite driver, HTTP routing and web push | Low. Mature libraries, locked by checksum. |
| Python runtime in the container | Running the broker helper | Low. Only the helper uses it. |
| Web push on the phone | Notifications to the installed app | Low. Supported for home-screen web apps on current iPhone and Android. |
| Tailscale and Tailscale Serve on the NAS host | HTTPS on the tailnet and the caller's identity | Low. Already in use for the NAS's other apps. |
| Docker on Unraid | Running the container | Low. Already in use. |
| Free end-of-day price sources (two) | Daily prices and a cross-check | Medium. Free sources change terms and have gaps; the cross-check and fail-closed rule contain the damage. |
| Company filings and public news feeds | Material for the research job | Low. Read only on the Mac. |
| Claude CLI under your subscription, on the Mac | The research job and the setup skill | Medium. When the login lapses, the feed goes stale until you log in again; trading carries on. |
| An exchange holiday calendar | Trading days, holidays and half days | Low. Kept as a table in the repo and checked each year against the exchange's published schedule. |
| A sector source (SEC industry codes) | The optional sector limit | Low. |

## Migrations

There is no data to migrate. v1 stays in `v1/` as reference, and the rewrite starts with an empty database. Your existing holdings enter through a read-only import from the broker, before any strategy runs.

The database is managed by numbered migrations from the first commit, as described in Detailed Design. Rollback of a migration is a restore of the nightly backup taken before it, together with the previous container image.

When this design lands, the unmerged branch that holds the v2 design is closed and the v2 design is marked replaced. The link to the v2 design points at a fixed commit, and the v1 audit has been copied to [`docs/retros/v1-tech-debt.md`](../../docs/retros/v1-tech-debt.md) on `main`.

The repo now has one developer, so the note in `AGENTS.md` that kept Codex in its list of tools for a second developer is dropped in the same change as this design.

## Technical Debt & Risks

- **The unofficial broker route can break or be blocked.** Robinhood can change its private API at any time, and it may restrict an account it sees being automated. Sherwood halts on any broker error, and resting stops at the broker keep protecting holdings while it is halted. *Re-audit when* the route breaks for the first time, when Robinhood changes its terms on automated access, or when a broker with an official API becomes acceptable to you.
- **Second-factor logins expire.** When the broker session lapses, trading halts until you log in again on the NAS. *Re-audit if* it lapses more than once a month.
- **Paper fills are approximate.** The paper broker fills against daily open, high, low and close, so it cannot see the order of moves within a day, and real fills will differ. *Re-audit after* the first month of live trading, by comparing live fills with what the paper broker would have done.
- **A stop order can fill below its stop.** When a stock opens far below its stop, the stop fills at the open. The per-stock limit bounds how much one gap can cost. *Re-audit after* any stop fills more than a few percent below its stop.
- **Free price data has errors and gaps.** Two sources and fail-closed handling contain them, but a shared upstream error would pass the cross-check. *Re-audit when* a price error reaches a signal.
- **The AI feed can be wrong.** It can misread news or cite weak sources. Its items need sources, and it never trades, but a convincing wrong case could still sway your approvals. *Re-audit after* three months of feed history, by checking how candidates did against their cases.
- **The feed depends on the Mac being awake.** When it sleeps, the feed goes stale. *Re-audit if* stale feeds become common.
- **One NAS, one database file.** A disk failure or a corrupted file loses recent history. The nightly checked backup bounds the loss to a day, and the broker remains the record for holdings and orders. *Re-audit when* the NAS backup design changes.
- **Approval fatigue.** Tapping approve on every buy can become a reflex. One tap per order and a visible risk verdict on each card help. *Re-audit if* buys are routinely approved within seconds of appearing.
- **The link to the v2 design points at a commit on a branch that will be deleted.** GitHub may eventually drop commits no branch holds, and the link would then break. The v1 audit is safe, because it is now kept in `docs/retros/` on `main`. *Re-audit when* the branch is retired, if the v2 design is still worth reading.

## Quality Attributes

### Security

The threat model has three parts: someone reaching the app who should not, a broker secret leaking, and the AI being turned against you through what it reads.

**Reaching the app.** The app exists only on the tailnet, through Tailscale Serve, with nothing open on the router and no Tailscale Funnel. The container's port is bound to the host's loopback address, so Serve is the only way in, and Serve's identity header can therefore be trusted. Sherwood accepts one tailnet login, every change is a POST with an anti-forgery token, and approvals and stop-switch resets can be limited to named devices.

**Broker secrets.** **The broker login, its second-factor setup and its session token live only in the owner-only broker settings file in the container's application-data folder on the NAS, and only the broker helper reads it.** They are never in the repo, the vault, a log or a test fixture. You enter them yourself through the helper's login command; no agent sees them. The Go application holds no broker secrets at all, so a bug in the web-facing code cannot reveal them. The logger writes only named fields, and the settings values are wrapped in a type whose text form is a mask, so printing one by mistake prints the mask. Test fixtures for the Robinhood adapter are written by hand from the library's documented shapes, never captured from a real account, and a CI check scans the repo for credential-shaped strings. The repo ships an example settings file with placeholders, and the real file's name is in `.gitignore`. The feed token is a separate secret with power only to read the research export and upload a feed.

**The AI.** The AI reads untrusted web pages, which can carry text written to steer it. Its session has no shell and no tool that can reach the NAS, its output is a file a plain script validates against a schema, and the NAS shows all feed text as escaped plain text. The worst a manipulated session can do is write a bad feed item, and a feed item cannot become an order without your watchlist tap, a rule's signal and your approval.

**Supply chain.** Go modules are locked by checksum, the broker helper's Python dependencies are locked with hashes, the unofficial broker library is pinned to one reviewed version and every upgrade of it is read before it is taken, the container's base image is pinned by digest, and GitHub Actions are pinned by commit.

### Reliability

**Sherwood fails closed: when anything is uncertain, it stops trading and keeps protecting.** A broker error, an order in `unknown`, a reconciliation difference, a price disagreement or missing prices all stop new trading, while resting protective stops at the broker keep working. A restart resumes from the database, and the deterministic jobs catch up without sending a buy into the wrong session. The Mac being asleep only makes the feed stale.

### Data Integrity

Money is decimal end to end and stored as text. The order-history table is append-only, and an order's state can be rebuilt from it. The idempotency key has a unique constraint. Holdings are derived from fills and reconciled against the broker twice a day, and the broker is treated as the record when they differ, after you review it. Migrations are numbered from the first commit and run in a transaction. The nightly backup is checked by opening it.

### Privacy

Your holdings, balances, orders, strategies and limits stay on the NAS. The research export sent to the Mac carries symbols, prices and signals, with no balances, share counts or account details, and the research job sends those symbols to Claude under your subscription. Nothing personal goes into the public repo: strategies are generated per install and live in the NAS's configuration, and the repo's examples use placeholder values.

### Abuse

The one realistic abuse path is prompt injection through news pages read by the research job, covered under Security. The app is reachable only by your tailnet login, so there is no public surface to rate-limit.

### Accessibility

The phone app is designed for one-handed phone use: large tap targets, high contrast in light and dark themes, readable text sizes, and clear words on every button (Approve, Decline, Stop all trading). Destructive actions ask for a second confirmation. The paper or live banner never relies on colour alone.

### Testability

The broker, the price sources, the clock and the notifier are interfaces, and each has a fake used in tests; no test touches the network or a real account. The rules are pure functions tested with tables of prices and expected signals. The risk check has a passing and a refusing test for every limit. The state machine has a test for every allowed transition and for refusal of an illegal one. One test runs the signal job twice for the same day and finds a single order; another makes the fake broker fail and finds trading halted and no retry sent. The import boundary between the feed and the execution layer is checked by a test. A contract test runs the Go client against the broker helper with the helper's library replaced by a fake, so the socket messages are checked on both sides without touching Robinhood. The back-tester gives the same result on every run of the same inputs.

### Compliance

Robinhood's customer agreement may not allow automated access through its private API, and you have accepted that risk. The tool does no day trading, which keeps it clear of the pattern-day-trader rule, and the risk check refuses a same-day buy and sell. Trades are recorded with their dates, prices and fees for tax reporting; wash sales are noted on the Orders view but not prevented. Nothing sherwood shows is financial advice; the rules are yours to approve, and the AI's cases are for your judgment.

## Project management

### Work estimates

| Stage | What it delivers | Size |
|---|---|---|
| Skeleton | The container with the Go application and the broker helper, the database with its first migrations, the installable phone app over Tailscale Serve, the settings and strategy files, and a read-only holdings import. | M |
| Prices and rules | The price sources and cross-check, the rule library, the setup skill, the deterministic scheduler and the back-tester. | L |
| Research feed | The research export, the research skill and job on the Mac, feed upload and the Feed view. | M |
| Paper trading | The order state machine, the risk check, the paper broker, approvals, the stop switch, automatic halts, notifications and reconciliation. | L |
| Live trading | The Robinhood adapter, the login command, resting stops, and the switch to live mode with a small amount. | M |

The build plan is written only after this design is approved.

### Documentation Plan

| Page | Kind |
|---|---|
| `wiki/designs/sherwood.md` (this design) | Design |
| `wiki/Home.md` | Landing page |
| Install sherwood on the NAS | How-to |
| Log in to the broker on the NAS | How-to |
| Generate your strategies with the setup skill | How-to |
| Set up the nightly research job on the Mac | How-to |
| Strategy file and settings file reference | Reference |
| Rule library reference | Reference |
| Order states and risk checks reference | Reference |
| Halts, resets, reconciliation and restoring a backup | Operational runbook |

### Launch Plans

Sherwood launches in paper mode. It moves to live mode only when the paper record has held up for a minimum number of weeks set in the strategy file, and only by your decision on the phone app. Live trading starts with a small amount, held by the per-order and per-stock limits, and grows only when you loosen those limits at the NAS.

## Operations

### Monitoring and Alerting

The Today view shows the last run of every job, the mode, and any halt with its cause. You get a phone notification when buys are waiting, when trading halts for any reason, when a scheduled job fails or does not run, when the broker session needs a new login, and when the nightly backup fails its check. A stale feed shows as a quiet note on the Feed view rather than an alert, because trading does not depend on it.

### Logging Plan

Sherwood writes structured JSON logs of named fields to the container's log and to a rotating file in its application-data folder, kept for 90 days. Every job run, signal, order state change, risk decision, halt, reset and limit change is logged with its order key and reason. Broker secrets, tokens and request headers are never logged. The database's order-history and halt tables are the audit record and are kept for good.

### Rollback Strategy

The container image is versioned, and rolling back means pinning the previous image and restarting. A rollback across a migration means restoring the backup taken before that migration. The fastest way out of any trading problem is the stop switch, which halts trading without a deploy. Orders already filled at the broker cannot be rolled back; they are undone, if at all, by new trades you approve.

## Document History

| Date | Change | Status |
|---|---|---|
| 2026-11-15 | Initial draft written from the 2026-11-15 rewrite brief, carrying over the v2 design's foundations and the v1 audit's constraints. The owner's first review the same day settled the open calls: Go for the application with Python only for the broker calls, an installable web app on the phone, SQLite, and the remaining calls (resting stops at the broker, the stop switch keeping them, non-protective sells waiting for approval, watchlist taps, approval devices) as written. The v1 audit was copied to `docs/retros/` on `main`. The owner then approved the design as final. It was translated into six parts (foundations, phone-app, prices-and-rules, paper-trading, research-feed, live-trading) in `wiki/designs/sherwood/parts/` and sequenced into plans: foundations is active at `.harness/PLAN.md`, and the other five are queued in `.harness/designs/sherwood/queued-plans/`. | final |
