# Sherwood

Sherwood is a small, self-hosted investment tool. It runs on a home NAS, keeps a portfolio of at most 20 stocks, trades by fixed written rules inside hard limits, and keeps an AI-written feed of possible next picks. You reach it from your phone over a private Tailscale network.

## Designs

- **[Sherwood Design](designs/sherwood)** — is the design for the rewrite. It explains why sherwood is built the way it is: the rules that make every trade, the risk check every order passes through, the approval step for buys, and why the AI never touches the broker. It is final and is being built in six parts, starting with the foundations.

## How-to guides

- **[Install sherwood on the NAS](how-to/install-sherwood-on-the-nas)** — takes you from a checkout of the repo to sherwood running in paper mode: the Compose file, the application-data folder and who owns it, the settings file, Tailscale Serve in front of the loopback port (and why it must stay on loopback), putting the app on your phone's home screen, and turning on notifications.
- **[Log in to the broker on the NAS](how-to/log-in-to-the-broker-on-the-nas)** — saves a broker session on the NAS and runs the first read-only import of your holdings and cash, with what to compare against the broker's app and what each failure message means.
- **[Set up and back-test a strategy](how-to/set-up-and-back-test-a-strategy)** — runs the `strategy-setup` skill on your Mac: the interview, the work folder kept outside the repo, validating the file, the Tiingo token file and price history, reading a back-test and what it leaves out, and copying the finished `strategy.yaml` to the NAS. Nothing it writes is financial advice.
- **[Run the first live session](how-to/run-the-first-live-session)** — turns live trading on with a small amount once the paper record is long enough: choosing the limits, setting `mode: live`, confirming on Settings from an approval device and restarting, what to watch at 09:00, at 09:35, every 15 minutes and after the close, how to stop, and a checklist of the checks only your real account can answer.
- **[Set up the research job](how-to/set-up-the-research-job)** — sets up the nightly research job on your Mac: building the `sherwood` helper, the owner-only config file with the feed token, a check-only run and a first run by hand, the launchd job and its log, what a stale feed means (trading carries on), what each failure message means, and the one risk that remains. The AI it runs never creates, approves or sends an order.

## Reference

- **[Settings and strategy file reference](reference/settings-and-strategy-files)** — lists every field of `settings.yaml` and `strategy.yaml`: its type, whether it is required, the values it accepts, what it means, and the error you see when it is wrong. It also covers where the files live, why the limits have no defaults, and how a limit is tightened or loosened.
- **[Rule library and back-test reference](reference/rule-library-and-back-tests)** — lists every rule type and every setting it takes, with the values each accepts, how a strategy's rules combine into one answer per stock, the `rules`, `check-strategy`, `fetch-bars` and `backtest` commands, the bars file format, how a back-test fills orders, and which limits it does not apply.

## Operational

- **[Runbook](operational/runbook)** — what to do when something needs you: the notifications sherwood sends, what each halt cause means in paper and live mode and what to check before you reset the stop switch, reconciliation differences against the real broker (lost stops, foreign orders and partial fills included), the live jobs and the 15-minute checks, logging in to the broker again, restoring the database from a nightly backup (also when orders rest at the broker), rolling back across a migration, going back to paper mode, and a stale research feed or a failed upload.

The original proof-of-concept is archived in [`v1/`](https://github.com/alexherrero/sherwood/tree/main/v1).
