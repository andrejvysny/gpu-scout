# GPU Scout

A small Linux service for on-demand GPU availability and training-completion alerts in Slack. Install the same binary on each server; there is no coordinator, database, inbound listener, or server-to-server connection.

GPU Scout polls one Slack channel every 15 seconds. Each instance handles commands addressed to its configured server. Optional Socket Mode receivers accept `/scout` commands and route them through the same channel using authenticated message metadata. Both servers can receive requests for either host, so no single receiver is required. It samples watched NVIDIA GPUs every 30 seconds through NVML, the management library installed with the NVIDIA driver.

## Requirements

- Linux x86_64, glibc 2.36 or newer, and a working NVIDIA driver with NVML.
- A user systemd manager with lingering enabled for startup at boot and operation after logout.
- Outbound HTTPS access to `slack.com`.
- An internal Slack app that can read and post in the configured channel.

The intended hosts are `dencun` (two RTX 4090 GPUs) and `pectra` (RTX PRO 6000 Blackwell Max-Q and RTX 5090). Their supplied system output reports glibc 2.39 and `Linger=yes`. Actual NVML metrics and Slack delivery must still be checked on each host. MIG monitoring is not supported in v1.

## Slack setup

1. Create an internal Slack app from [deploy/slack-app-manifest.json](deploy/slack-app-manifest.json), then install it in your workspace.
2. Invite GPU Scout to the channel used for commands and alerts. A dedicated channel is recommended.
3. Copy its channel ID and the app's **Bot User OAuth Token** (`xoxb-...`). Use the same app, token, and channel on both servers.
4. For slash commands, open **Basic Information**, find **App-Level Tokens**, and generate a token with `connections:write`. Save this `xapp-...` token as `SLACK_APP_TOKEN` in each server's private environment file. The app manifest enables Socket Mode and registers `/scout`.

The manifest requests `channels:history`, `groups:history`, `chat:write`, and `commands`. If only a public or private channel is used, remove the unused history scope. Slash commands use outbound Socket Mode connections; no public endpoint, inbound port, or signing-secret setup is needed. Without `SLACK_APP_TOKEN`, the service continues to support plain channel commands and rich replies.

Every human user in the configured channel can create a watch. Cancellation and watch listings are scoped to the requester. The app only processes that configured channel, but its Slack token can access other conversations where the bot has been invited; keep its membership limited accordingly. Never commit tokens or post them in Slack.

### Upgrade an existing v0.1 Slack app

Update the **existing app** under **App Manifest** using the new JSON, then reinstall it to authorize the additional `commands` scope. Keep the existing channel membership. Check the current Bot User OAuth Token after reinstallation and update it on both hosts if Slack changed it. Generate one app-level token with `connections:write` and add the same value to both hosts:

```text
SLACK_BOT_TOKEN=xoxb-YOUR-BOT-TOKEN
SLACK_APP_TOKEN=xapp-YOUR-APP-LEVEL-TOKEN
```

Upgrade both binaries before using slash commands: v0.1 ignores the routing metadata. The updated binary reads existing watches, state cursors, and pending notifications without deleting or resetting them. Rich formatting works immediately after upgrading, even before slash commands are configured. Neither the app-level token nor the bot token should appear in chat, command-line arguments, or Git.

## Build

```sh
make linux
```

Docker builds and tests the Linux AMD64 artifact using Go and C tooling on Debian Bookworm (glibc 2.36). The outputs are `dist/gpu-scout`, `dist/SHA256SUMS`, and `dist/gpu-scout-linux-amd64.tar.gz`. Docker is a build dependency only; servers do not need Docker, Go, or Python. The binary dynamically loads the existing NVIDIA driver library and uses the host's libc.

For development, `make test` runs the race detector and `go vet`. Non-Linux builds can run the portable tests and CLI, but `serve` and `doctor` explicitly reject GPU monitoring. Linux builds require `CGO_ENABLED=1`.

## Install on each host

Transfer the release archive to the host, extract it, and verify the checksum:

```sh
tar -xzf gpu-scout-linux-amd64.tar.gz
sha256sum -c SHA256SUMS
sh scripts/install.sh ./gpu-scout pectra C0123456789
```

Use `dencun` instead of `pectra` on the other host. The installer writes the binary to `~/.local/bin/gpu-scout`, a user systemd unit, and initial configuration. It preserves existing configuration and does not start the service. Ensure `~/.local/bin` is in your shell's `PATH`.

Edit `~/.config/gpu-scout/environment` and set `SLACK_BOT_TOKEN=xoxb-...`. For slash commands, add `SLACK_APP_TOKEN=xapp-...`. The installer creates this file with private permissions. Keep tokens out of command-line arguments and shell history. Review `~/.config/gpu-scout/config.json`; [config.example.json](config.example.json) lists the configurable thresholds and intervals.

```sh
. "$HOME/.config/gpu-scout/environment"
export SLACK_BOT_TOKEN
export SLACK_APP_TOKEN
gpu-scout -config "$HOME/.config/gpu-scout/config.json" doctor
systemctl --user enable --now gpu-scout.service
systemctl --user status gpu-scout.service
```

`doctor` verifies readable GPU metrics, the state directory, bot identity, and channel-history access without posting a message. If `SLACK_APP_TOKEN` is configured, it also checks permission to open a Socket Mode connection. It does not prove that the slash command was registered in the app manifest. To verify `chat:write` and actual delivery, send `scout status pectra` in the channel after starting the service.

If the user manager reports `degraded`, inspect `systemctl --user --failed`. An unrelated failed unit does not require deleting or resetting GPU Scout's state. Do not change unrelated services as part of installation.

### Deploy both hosts from the source checkout

After building the archive, run this from a terminal where both SSH aliases work:

```sh
sh scripts/deploy.sh C0123456789
```

Replace the example with your actual Slack channel ID. The script copies the release to `dencun` and `pectra`, verifies the binary checksum, and installs the user service while preserving existing configuration. It never requests or prints the bot token. If `~/.config/gpu-scout/environment` already contains a token, it runs `doctor` through systemd's environment-file support, enables and restarts the service, and verifies that it is active. Otherwise it leaves startup pending: set the token privately on each host and rerun the command. It stops on the first SSH, preflight, or service failure; earlier hosts may already have been installed. Existing configuration retains its original channel ID.

To deploy or retry a single host, append its alias: `sh scripts/deploy.sh C0123456789 pectra`.

## Slack commands

With slash commands enabled, type these in the configured channel:

```text
/scout help
/scout status dencun
/scout status pectra
/scout arm dencun gpu=0 expires=10m
/scout watches dencun
/scout cancel dencun watch=WATCH_ID
```

`/scout` alone also opens the help guide. GPU selection is always explicit: `/scout arm dencun` returns usage guidance rather than choosing a GPU for you. Commands in other channels are rejected with a private reply. Help and validation errors are private; command results and alerts appear in the configured channel.

You can also send a new top-level plain message, without mentioning the bot:

```text
scout status pectra
scout arm pectra gpu=0
scout arm dencun gpu=1 expires=24h
scout watches pectra
scout cancel pectra watch=1790000000-000001
scout help pectra
```

Commands and server names are case-sensitive. `gpu=` accepts the current index or full UUID shown by `status`; the watch is bound to the UUID, so index changes cannot retarget it. Each user can have one active watch per GPU. Repeated arming returns the existing watch ID; cancel it before changing expiry. No expiry is applied unless `expires=` is supplied. Expiry accepts positive Go durations, such as `30m` or `24h`, up to `8760h`.

Status uses separate GPU cards with state, utilization, memory in GiB, and compute-process count. UUIDs and observation times appear as secondary context. Watch confirmations, cancellations, expiry, training completion, and idle alerts use structured Block Kit messages. The help guide explains usage and examples. Text fallbacks remain available for notifications, accessibility, and old pending messages.

### Slash routing and recovery

A receiver saves each valid request locally before acknowledging it to Slack. It posts a small request record to the channel with structured metadata; the named host processes it on its next history poll. At the default polling intervals, results normally arrive within about 30 seconds. Agents accept metadata only from the bot ID returned by their own `auth.test`, preserve the original requester, and deduplicate invocation IDs across retries and restarts. Messages from other bots and user-supplied metadata cannot impersonate a request.

Either host can receive a request for the other. Keep both receivers configured with the same app-level token for redundancy. If every receiver is offline, slash commands fail; plain commands and existing local watches remain available wherever a monitor is running. A receiver's acknowledgment means queued, not armed: wait for the target host's confirmation. Requests delayed more than five minutes expire rather than arming unexpectedly. Duplicate request records can appear after ambiguous delivery or a retry routed to a different receiver, but each target executes a request only once within the retained 24-hour deduplication window.

Replies and alerts mention the requester in the configured channel. Plain commands in threads, edited messages, untrusted bot messages, commands for other hosts, and messages older than five minutes are ignored. Old channel history is ignored on first startup. Commands received during an outage are processed after recovery only if still within five minutes; wait for confirmation before assuming a watch is armed. A misspelled server in a plain command, or an offline target, does not reply.

### What “unused” means

All conditions must hold at consecutive samples spanning at least two minutes:

- No compute processes reported by NVML.
- GPU utilization at most 5%.
- GPU memory use at most 512 MiB.

These thresholds are configurable. A GPU already idle when armed must pass a fresh confirmation window. Busy readings, unavailable metrics, a missing device, service restart, or a sample gap longer than twice the configured GPU polling interval reset the window. Each successful watch emits one logical notification and is retired.

This is sampled evidence, not proof of continuous inactivity between samples. Brief activity between samples can be missed. An idle GPU may still be booked, and a stopped process may have failed. GPU Scout does not check bookings, reserve hardware, start jobs, or infer training success from GPU load. Background graphics memory may require adjusting the threshold after inspecting a known idle baseline.

## Training completion

Launch a noninteractive training command over SSH under the same Linux account that owns the service:

```sh
gpu-scout run --label experiment-42 --notify-user U0123456789 -- python train.py
```

Use your Slack member ID for `--notify-user`. The wrapper preserves argument boundaries, environment, working directory, standard streams, and exit code. It forwards INT, TERM, HUP, and QUIT to the child's process group. Signal termination is reported as `128 + signal`; a command that cannot start returns 127. This wrapper does not manage interactive terminal foreground control.

Completion is queued in a private local spool, so the daemon can send it after a Slack outage or service restart. The wrapper needs no Slack token. Alerts contain the label, server, elapsed time, completion time, and exit status; they never contain the command arguments or captured training output. Use `tmux` or your existing job-launch mechanism if the run must survive SSH disconnection.

The wrapper does not detach jobs or turn them into systemd services. SIGKILL, host failure, or a crash before the completion record is written can prevent notification. Background children that outlive the wrapped command are not included in its completion result. If completion cannot be queued, an error is written to stderr while preserving the command's exit code.

## State and recovery

Default state is `$XDG_STATE_HOME/gpu-scout`, or `~/.local/state/gpu-scout` if unset; `state_dir` can set an absolute path explicitly. Ensure the wrapper and service use the same configuration and state directory. Config defaults follow `$XDG_CONFIG_HOME`; the supplied service and installer explicitly use `~/.config/gpu-scout/config.json`.

State directories use mode 0700, files use 0600, and updates use atomic replacement with filesystem synchronization. Only one daemon can hold a state directory's lock. Watches, Slack progress, and pending alerts survive restart; idle confirmation starts over. State from another server/channel or malformed JSON is rejected instead of silently discarded.

Slack failures retain queued messages and retry with exponential backoff, honoring `Retry-After`. Status and availability messages include the observation time: a delayed notification describes past conditions, so check current status before using a GPU. There is no exact-once delivery guarantee: a network timeout or crash after Slack accepts a message can cause a duplicate. Pending state and the wrapper spool are local to one account; this is not a multi-account training supervisor. No alert can be delivered while the host itself is offline.

```sh
journalctl --user -u gpu-scout.service -f
systemctl --user restart gpu-scout.service
systemctl --user disable --now gpu-scout.service
```

To upgrade, stop the service, run the installer with the new binary, run `doctor`, and restart it. Preserve the state directory. No automatic updates, GPU management calls, commits, or external project-management writes are performed.
