# Set up the research job

This page sets up the nightly research job on your Mac. Each night it downloads a short export from sherwood, has Claude read the public news about your stocks, and uploads what Claude wrote as the next morning's feed. You set it up once, run it by hand twice to see it work, and then hand it to launchd.

**The AI never creates, approves or sends an order.** The feed is reading material and nothing else. Only sherwood's rules create orders, and only you approve buys. A candidate in the feed reaches the rules only when you tap "Add to watchlist", and only the strategies that set `include_watchlist: true` look at it. Even then a rule has to signal, the risk check has to pass the buy, and you have to approve it. Nothing the job writes is financial advice.

Sherwood must already be running on the NAS, as in [Install sherwood on the NAS](install-sherwood-on-the-nas). Every path, address, symbol and token below is a placeholder; put your own in its place.

## What the job does each night

The job is one script, `deploy/mac/research-job.sh`, and it runs three steps in order. It stops at the first one that fails.

1. **Fetch the export.** `sherwood research-export` asks the NAS for the research export with the feed token. The export lists the stocks you hold and watch, their last 30 closing prices, and the latest signals with the rules' own reasons. It holds no share counts, costs, cash, balances or account details.
2. **Research.** Claude runs headless under your own Claude login, with the research skill from the repo. It can read its inputs, search the web, fetch public web pages, and write files in its own output folder, and nothing else. It has no shell, never sees the feed token, and has no way to send anything to the NAS. It writes one file, `out/feed.json`.
3. **Check and upload.** `sherwood research-upload` checks the file with the same code the NAS runs, then uploads it with the feed token. The NAS checks it again as it arrives, keeps the items that pass, and answers with what it kept and dropped.

The [Sherwood design](../designs/sherwood#the-research-feed) explains why the work is split this way: a confused or manipulated Claude session can only ever write a bad feed item.

## Before you start

You need:

- **A Mac that is signed in to your tailnet as the same login as `tailnet_login`** in the NAS settings file. The feed routes are reached through Tailscale Serve like the app, and the Serve login check comes first.
- **Go**, to build the `sherwood` helper. Check with `go version`.
- **A clone of the repo** on the Mac, kept on `main`. The job reads the research skill from it each night.
- **Claude Code 2.1.248 or later**, because the job needs its `--restricted` flag. Check with `claude --version`, and run `claude update` if it is older.
- **Claude Code signed in.** Run `claude` in a Terminal window, then `/login` if it asks. The job uses that login, under your own subscription.
- **The feed token** from the NAS settings file, the `feed_token` value described in the [settings reference](../reference/settings-and-strategy-files#feed_token). You will type it into your own Terminal in step 2, and only there. Never paste it into a chat, a note or the repo.

## 1. Build the helper and make the work folder

The job keeps everything it needs in one folder outside the repo. The default is `~/sherwood-research`, and the rest of this page uses it. Make it readable only by you:

```sh
mkdir -m 700 ~/sherwood-research
```

Then, from the root of your clone, build the `sherwood` helper into it:

```sh
go build -o ~/sherwood-research/sherwood ./cmd/sherwood
```

The helper is the same program that runs on the NAS. The Mac uses only its two research commands, which open no database and read no NAS settings.

The folder will end up holding the helper, the config file, the log, and a `run` folder where Claude works. Everything in it is personal, because the export and the log name the stocks you hold. Keep it outside the repo, and don't put a space in its path: the script refuses a work folder with a space in it.

## 2. Write the config file

The helper reads one small config file, `research.yaml`, with exactly two keys:

- `nas_url` is sherwood's Tailscale Serve address, such as `https://sherwood.example-tailnet.ts.net`. It must use `https`.
- `feed_token` is the token from the NAS settings file.

The repo's `deploy/mac/research.example.yaml` shows the shape. Write the real file yourself, in your own Terminal window, so the token never passes through Claude or your shell history:

```sh
(umask 077; read -rs "T?Feed token: "; printf 'nas_url: "https://sherwood.example-tailnet.ts.net"\nfeed_token: "%s"\n' "$T" > ~/sherwood-research/research.yaml)
```

Put your own Serve address in place of the example one before you run it. Paste the token at the prompt and press Return; nothing is shown as you type. Then make sure the file is readable only by you, and check it:

```sh
chmod 600 ~/sherwood-research/research.yaml
ls -l ~/sherwood-research/research.yaml
```

The file should show as `-rw-------`. The helper refuses a config file that anyone else can read, a file with any key other than these two, the example's placeholder token, a token shorter than the NAS accepts, and an address that is not `https`.

Keep the config file where it is, in the work folder and not in the `run` folder inside it. Claude's file tools are kept inside `run`, and the script refuses a config file placed there.

## 3. Do a check-only run

This step proves the Mac can reach the NAS with the token, and that the checker works, without running Claude or uploading anything.

First fetch tonight's export by hand:

```sh
~/sherwood-research/sherwood research-export -config ~/sherwood-research/research.yaml -out ~/sherwood-research/export.json
```

It prints one line with the number of stocks (held and watched), the signal date and the number of signals, then `wrote` and the file name. Open the file and check that it holds only symbols, closing prices and signals. If the rules have not run yet, the signal date is empty and there are no signals.

Then check the repo's example feed against your real export, from the root of your clone:

```sh
~/sherwood-research/sherwood research-upload -export ~/sherwood-research/export.json -feed .claude/skills/research/example-feed.json -check-only
```

With `-check-only`, nothing is sent, and no config file is needed. The example feed was written for a made-up export, so expect this to say that 2 items pass and 3 would be dropped:

- the two candidates pass, because their made-up symbols are not among your holdings;
- the two holding notes are dropped, because you don't hold those made-up stocks;
- the signal explanation is dropped, because its signal key is not in your export.

Each dropped item has a line saying why, and the run ends with `check only: nothing was sent`. That is the checker doing its job.

When you are done, delete the export, since it lists your stocks:

```sh
rm ~/sherwood-research/export.json
```

## 4. Do a first real run by hand

Now run the whole job once, from the root of your clone:

```sh
CLAUDE="$(command -v claude)" bash deploy/mac/research-job.sh
```

The script prints a dated line as each step starts. The research step takes a few minutes, while Claude reads the news, and ends with a line or two from Claude saying how many items it wrote of each kind. Then the upload prints how many items passed the check, any it dropped and why, and `the NAS answered 200 OK` with the NAS's answer:

```text
{
  "upload": 1,
  "accepted": 9,
  "dropped": []
}
```

The script ends with `done`. Open the Feed view on your phone: it should say when the feed arrived and how many items were kept, and show the new candidates with their sources. Portfolio should show a note under each holding, and an approval card for a signal should show the feed's explanation beside the rule's reason.

Read a few items against their sources. If anything tells you what to do, such as "buy", "sell" or a target price, the session broke the skill's one rule. Note it and look into it before you leave the job running.

## 5. Install the launchd job

launchd runs the job each night at 21:30. If the Mac is asleep then, launchd runs it when the Mac next wakes.

1. Copy the example job into your LaunchAgents folder:

   ```sh
   cp deploy/mac/com.example.sherwood-research.plist ~/Library/LaunchAgents/
   ```

2. Open the copy and replace every `/PATH/TO/...` with a real absolute path. launchd does not expand `~` or `$HOME`, so write each path in full:
   - the script, `/PATH/TO/sherwood-repo/deploy/mac/research-job.sh`, becomes the path to your clone's copy;
   - `RESEARCH_DIR` becomes your work folder, such as `/Users/you/sherwood-research`;
   - `CLAUDE` becomes the full path that `command -v claude` prints, because launchd's `PATH` is short;
   - both log paths become `research-job.log` in your work folder.

   To run at a different time, change `Hour` and `Minute`.

3. Check the file, then load it:

   ```sh
   plutil -lint ~/Library/LaunchAgents/com.example.sherwood-research.plist
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.sherwood-research.plist
   ```

4. Run it once now through launchd, to be sure it works there too:

   ```sh
   launchctl kickstart -k gui/$(id -u)/com.example.sherwood-research
   ```

If you change the file later, unload it and load it again, because launchd does not reread it on its own:

```sh
launchctl bootout gui/$(id -u)/com.example.sherwood-research
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.sherwood-research.plist
```

`launchctl print gui/$(id -u)/com.example.sherwood-research` shows whether the job is loaded, and `last exit code` says how its last run ended: `0` means it finished.

## Reading the log

Everything the job prints goes to `research-job.log` in the work folder. Each run adds to the end of it:

```sh
tail -n 40 ~/sherwood-research/research-job.log
```

A good night looks like this: `step 1: fetching the export`, the export's summary line, `step 2: research`, Claude's count of items, `step 3: checking and uploading the feed`, the check's result, the NAS's answer, and `done`. A run that stops early ends with the error that stopped it, and the [troubleshooting](#troubleshooting) table below says what each one means.

The log names your stocks, so keep it in the work folder, out of the repo. It grows by a few lines a night; delete it whenever you like.

## Keeping it up to date

The job reads the research skill from your clone each night, so keep the clone on `main` and pull now and then. When you pull a change to `cmd/sherwood` or `internal/feed`, build the helper again, as in step 1. The Mac and the NAS should check feeds with the same code; if the NAS is newer than the Mac, it may refuse a file the Mac passed.

## What a stale feed means

A feed stays fresh for 36 hours (the threshold is `feed.StaleAfter` in the code), so one missed night is enough to make it stale. The Feed view then shows a quiet note saying when the latest feed arrived, and Portfolio and the approval cards show only the rules' own reasons, without the feed's notes and explanations, until a new feed arrives. You get no notification, because nothing about trading depends on the feed. The rules still run, the risk check still applies, buys still wait for your approval, and protective stops still work.

A stale feed usually means one of these:

- the Mac was off, or asleep past the run and has not woken since;
- the Mac was not on the tailnet, or the NAS was down when the job ran;
- the Claude login lapsed;
- the job ran and failed, which the log says.

Read the log, fix what it shows, and run the job by hand with `launchctl kickstart -k gui/$(id -u)/com.example.sherwood-research`. The runbook has the same steps under [Stale feed or failed upload](../operational/runbook#stale-feed-or-failed-upload).

## Troubleshooting

Every failure from the helper starts `sherwood: research-export:` or `sherwood: research-upload:`. When the NAS turns a request away, the export step says `the NAS answered` and the upload step says `the NAS refused the feed:`, each followed by the status and the NAS's message. The script's own messages start with the time and `research-job:`.

| What you see | What it means | What to do |
|---|---|---|
| `401 Unauthorized` and `This route needs the feed token.` | The token in `research.yaml` is not the one in the NAS settings file. | Write the config file again with the token from the NAS settings file, as in step 2. If you changed the NAS's token, restart sherwood so it reads the new one. |
| `403 Forbidden` and `This route is for the research job's script, not a browser.` | The request carried a browser header (`Sec-Fetch-Site`). The helper never sends one, so something else sent it, such as a browser, a copied `curl` command or a proxy in between. | Use the helper, not a browser or a copied request. |
| `403 Forbidden` and `This app is only for its owner's tailnet login.` | The Mac is on the tailnet under another login, or as a tagged device. | Sign the Mac in to the tailnet as your `tailnet_login`. |
| `asking the NAS for the export:` or `sending the feed:` followed by a network error | The Mac could not reach the NAS: it is off the tailnet, the NAS is down, or `nas_url` is wrong. | Open the app on the Mac's browser to check the address, then run the job again. |
| `the NAS refused the feed: 413 Request Entity Too Large` | The file was over the NAS's 1 MiB limit. The helper refuses a file that large before sending it, so this means the Mac's helper and the NAS were built from different versions. | Pull and build the helper again, as in step 1. |
| `... was not sent:` followed by a reason | The feed file is off-schema: it has a field the schema does not allow, a wrong `version`, more than 200 items, or it is not JSON at all. The NAS would refuse the whole file, so the helper did not send it, and there is no feed tonight. | Look at `run/out/feed.json` in the work folder to see what the session wrote, then run the job again. If it happens often, the skill may need a change. |
| `the NAS refused the feed: 400 Bad Request` and `The feed file was refused:` | The NAS found the file off-schema although the Mac's check passed. The two were built from different versions. | Pull and build the helper again, as in step 1. |
| `dropped item 3 (holding_note EXMPL): ...` | One item broke a rule, such as a missing source, a note on a stock you don't hold, or a signal key that is not in the export. The other items were still uploaded. | Nothing, if it is now and then. The NAS checks again against the holdings and signals as they are when the file arrives, so it can drop an item the Mac kept if something changed overnight; its answer lists those. Many drops every night mean the skill needs a look. |
| `config file ... can be read by others; run chmod 600 on it` | The config file's permissions are too open. | Run `chmod 600` on it. |
| `config file ... still holds the example's feed_token` or `nas_url must use https` | The config file still has the example's values. | Write it again, as in step 2. |
| Claude Code says it does not know an option, such as `--restricted` | Claude Code is older than the job needs. | Run `claude update`, check `claude --version` shows 2.1.248 or later, and run the job again. |
| Claude says you are not logged in, or asks you to run `/login` | The Claude login lapsed. | Run `claude` in a Terminal window, then `/login`, and run the job again. No one else can do this for you. |
| `env: node: No such file or directory` | Your `claude` was installed with npm and needs Node, which is not on launchd's short `PATH`. | Add the folder that holds `node` (`dirname "$(command -v node)"` prints it) to the front of `PATH` in the plist, then unload and load the job again. |
| `the research session wrote no feed file; nothing to upload` | Claude ran but wrote no `out/feed.json`, often because it stopped early. Nothing was sent. | Read the lines above it in the log for Claude's own message. |
| `RUN_DIR must not contain a space` | The work folder's path has a space in it. | Move the work folder to a path without spaces and update `RESEARCH_DIR` in the plist. |
| `no research skill at ...` | The script cannot find the skill in your clone, usually because the clone moved. | Fix the script's path in the plist. |

## The risk that remains

Claude reads untrusted web pages, and a page can carry text written to steer it. The job limits what such a page could make it do: Claude has no shell and no token, can write only inside its output folder, and can upload nothing, and the feed it writes cannot become an order. One gap is left, and it is worth knowing about. Claude's web fetches run on the Mac, and the Mac is on your tailnet as the owner, so a fetch of sherwood's own Serve address would pass the login check and could read the app's pages. The job denies Claude any fetch to a `.ts.net` address, and the skill forbids fetching tailnet, local or bare IP addresses. A bare tailnet IP address is not caught by that rule, but sherwood's HTTPS certificate is for its `.ts.net` name, so a fetch by IP address should fail. The feed routes also need the feed token, which Claude never has. If sherwood is ever served over plain `http` on the tailnet, this gap needs looking at again.

## Related

- [Settings and strategy file reference](../reference/settings-and-strategy-files#feed_token) explains the feed token and what it opens.
- [Runbook](../operational/runbook#stale-feed-or-failed-upload) covers a stale feed or a failed upload.
- [Install sherwood on the NAS](install-sherwood-on-the-nas) covers Tailscale Serve and the NAS settings file.
