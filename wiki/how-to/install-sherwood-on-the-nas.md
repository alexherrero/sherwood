# Install sherwood on the NAS

This page takes you from a checkout of the repo to sherwood running on your NAS, open on your phone as an app, and able to send you notifications. It assumes a NAS that runs Docker with `docker compose` (on Unraid, the Compose Manager plugin provides it) and has Tailscale installed and signed in to your tailnet. Every name, address and path below is a placeholder; put your own in its place.

Sherwood starts in paper mode, so nothing on this page touches a broker or real money. Logging in to the broker is a separate step, in [Log in to the broker on the NAS](log-in-to-the-broker-on-the-nas).

## Before you start

You need:

- A shell on the NAS, as a user who can run `docker` and `tailscale`.
- Your tailnet login, exactly as Tailscale shows it in the admin console, such as `you@example.com`.
- Your phone's tailnet address. The Tailscale app on the phone shows it, and so does your tailnet's machine list in the admin console. It looks like `100.64.0.10`.
- HTTPS certificates turned on for your tailnet in the Tailscale admin console. Tailscale Serve needs them to give sherwood an `https://` address.

## 1. Get the code and the Compose file

Put a checkout of the repo somewhere on the NAS, for example with `git clone https://github.com/alexherrero/sherwood.git`, or by downloading and unpacking the source archive from GitHub. In the checkout, copy the example Compose file:

```sh
cd sherwood
cp compose.example.yaml compose.yaml
```

The example mounts `/mnt/user/appdata/sherwood` as the container's `/data`. If your application-data folder lives somewhere else, change the left-hand side of that `volumes` line. The second `volumes` line mounts your backup share as `/backup` for the nightly backup; change its left-hand side to a folder on your backup share, which must already exist and be writable by the container's user (see [`backup`](../reference/settings-and-strategy-files#backup) in the settings reference). Leave the rest of the file as it is, and in particular leave the `ports` line alone; the section on Tailscale Serve below explains why.

## 2. Make the application-data folder

Everything that belongs to your install lives in this one folder on the NAS, and nothing of yours is in the image. Create it:

```sh
mkdir -p /mnt/user/appdata/sherwood
```

The image runs as user 10001, and that user must be able to write the folder. Choose one of these:

- **Give the folder to user 10001.** Run `chown -R 10001:10001 /mnt/user/appdata/sherwood`.
- **Run the container as the folder's owner.** Uncomment the `user:` line in `compose.yaml` and set it to the folder's owner. On Unraid, appdata usually belongs to `nobody:users`, so the line is `user: "99:100"`.

Either works. What matters is that the files sherwood writes, and later the broker helper's own file, belong to the same user the container runs as.

## 3. Write the settings and strategy files

Copy the two example files into the folder:

```sh
cp settings.example.yaml /mnt/user/appdata/sherwood/settings.yaml
cp strategy.example.yaml /mnt/user/appdata/sherwood/strategy.yaml
```

Then edit `settings.yaml`:

- Set `tailnet_login` to your tailnet login, exactly as Tailscale reports it, letter case included.
- Under `approval_devices`, put your phone's tailnet address in place of `100.64.0.10`.
- Replace `feed_token` with at least 32 random characters, for example the output of `openssl rand -base64 32`.
- Put your own Tiingo and Stooq keys under `prices`. Without them sherwood stores no prices and makes no signals.
- Leave `mode: paper`.
- Leave the VAPID keys for now. Step 7 fills them in. Until then sherwood starts with notifications off.

The [settings and strategy file reference](../reference/settings-and-strategy-files) explains every field and the error you see when one is wrong.

The example strategy file holds placeholder limits and strategies so that sherwood can start. They are not a recommendation. Replace the file with one made for you before you rely on anything sherwood suggests.

The settings file holds secrets, so make it readable by its owner only, and give both files to the user the container runs as (10001, or the user you set in step 2):

```sh
chmod 600 /mnt/user/appdata/sherwood/settings.yaml
chown 10001:10001 /mnt/user/appdata/sherwood/settings.yaml /mnt/user/appdata/sherwood/strategy.yaml
```

## 4. Build and start the container

From the checkout:

```sh
docker compose up -d --build
docker logs sherwood
```

The log is one JSON line per event. You should see `web server listening` and then `sherwood started` with `"mode":"paper"`, and a `notifications are off` warning until step 7 is done. If sherwood refuses to start, the last line says why, such as `sherwood: refusing to start: settings file is invalid: tailnet_login is missing`, and lists every problem in that file at once. Fix them, then run `docker compose up -d` again.

Check that the web port is published on the NAS's loopback address and nowhere else:

```sh
docker port sherwood
```

The only line should be `8080/tcp -> 127.0.0.1:8080`.

## 5. Put sherwood behind Tailscale Serve

Tailscale Serve gives sherwood an HTTPS address on your tailnet and passes each request on to the container. Point it at the loopback port:

```sh
tailscale serve --bg http://127.0.0.1:8080
```

This serves sherwood at `https://<nas-name>.<tailnet-name>.ts.net/`. If something else on the NAS already uses Serve's default port, add `--https=<port>` and use that port in the address. `tailscale serve status` shows what is being served. Never use `tailscale funnel` for sherwood: Funnel puts a site on the public internet.

From your phone, open `https://<nas-name>.<tailnet-name>.ts.net/health`. It should say `ok`. Then open the address without `/health` and you should see the Today view. `docker logs sherwood` gains a `request` line for each, with the route and status but no address or login.

### Why the port must stay on loopback

Sherwood knows who is calling only because Tailscale Serve tells it. Serve adds your tailnet login to every request, in a `Tailscale-User-Login` header, and the caller's tailnet address, in `X-Forwarded-For`. Sherwood turns away any request whose login is not the one in `settings.yaml`, and allows approvals only from the addresses in `approval_devices`.

Those headers can be trusted only because nothing but Serve can reach the port. Anything else that can reach `127.0.0.1:8080` could send the same headers and be taken for you. **Sherwood cannot check this from inside the container.** It sees the requests, not how Docker published the port, so keeping the port private is your job:

- Keep the `ports` line as `"127.0.0.1:8080:8080"`. Never change it to `"8080:8080"` or `"0.0.0.0:8080:8080"`, and never run the container with host networking.
- Remember that programs on the NAS itself, and other containers that use host networking, can reach the NAS's loopback address. Only run things there that you trust as much as yourself.
- Keep sherwood on its own Compose network. Do not attach other containers to it.
- After any change to the Compose file, run `docker port sherwood` again and expect only `127.0.0.1:8080`.

A request from a tagged device carries no login, so sherwood refuses it. Open sherwood from your own phone or computer signed in as you.

## 6. Put the app on your phone's home screen

Sherwood is a web app you can install. Installing it is what makes it open full screen, and on an iPhone it is also what allows notifications.

**iPhone (Safari, iOS 16.4 or later):**

1. Open sherwood's address in Safari.
2. Tap Share, then **Add to Home Screen**. The name reads "Sherwood" and the icon is a pale pine tree on green. Tap **Add**.
3. Open sherwood from the new home-screen icon. It opens full screen, with no Safari address bar.

**Android (Chrome):**

1. Open sherwood's address in Chrome.
2. From the ⋮ menu, choose **Install app** (on some phones, **Add to Home screen**, then **Install**).
3. Open sherwood from the launcher. It opens on its own, with no address bar.

Tap through the six views at the bottom. Each opens, with the current one highlighted. If the phone leaves the tailnet, the app says "Sherwood can't reach the NAS" rather than showing an old page, because it never keeps your data on the phone.

## 7. Turn on notifications

Notifications need a VAPID key pair, which you make once on the NAS.

1. Make the pair with the `openssl` commands in the reference's [`notifications.web_push` section](../reference/settings-and-strategy-files#notificationsweb_push), or with `npx web-push generate-vapid-keys`.
2. Put the two halves in `settings.yaml` as `vapid_public_key` and `vapid_private_key`. Set `contact` to a `mailto:` address you read; push services use it only to reach you about a problem.
3. Restart the container so it reads the settings again: `docker compose restart`. Check `docker logs sherwood` once more. The `notifications are off` warning should be gone. If it is still there, its reason names the field to fix.
4. On the phone, open sherwood. **On an iPhone, open it from the home-screen icon, not from a Safari tab**, because iOS offers notifications only to installed web apps. On Android, use the installed app too.
5. Go to Settings and tap **Turn on notifications on this device**, then **Allow** when the phone asks. The page says "Notifications are on for this device." and "1 device has notifications on."
6. Tap **Send a test notification**. Within a few seconds a notification from Sherwood says "Notifications are working". Tapping it opens sherwood on Settings.

If the page says "Notifications could not be turned on" and names the push service, open sherwood in Safari on an iPhone or Chrome on Android, from the home-screen app. If it says "Nothing was sent", no device has notifications on yet. If a test never arrives, check that notifications for Sherwood are allowed in the phone's own settings.

If you ever make a new key pair, every device has to turn notifications on again.

## What's next

Sherwood is now running in paper mode and you can reach it from your phone. To bring in your current holdings, follow [Log in to the broker on the NAS](log-in-to-the-broker-on-the-nas).
