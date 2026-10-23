#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
  echo "Usage: sh scripts/install.sh BINARY SERVER SLACK_CHANNEL_ID" >&2
  exit 2
fi
binary=$1
server=$2
channel=$3
case "$server" in ''|*[!a-zA-Z0-9_-]*) echo 'Invalid server name' >&2; exit 2;; esac
case "$server" in [a-zA-Z0-9]*) ;; *) echo 'Invalid server name' >&2; exit 2;; esac
if [ "${#server}" -gt 63 ] || [ "${#channel}" -lt 3 ]; then
  echo 'Invalid server name or Slack channel ID' >&2
  exit 2
fi
case "$channel" in C*|G*) ;; *) echo 'Invalid Slack channel ID' >&2; exit 2;; esac
case "$channel" in *[!A-Z0-9]*) echo 'Invalid Slack channel ID' >&2; exit 2;; esac
if [ "$(uname -sm)" != 'Linux x86_64' ]; then
  echo 'This installer targets Linux x86_64.' >&2
  exit 1
fi

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
umask 077
mkdir -p "$HOME/.local/bin" "$HOME/.config/gpu-scout" "$HOME/.config/systemd/user"
chmod 700 "$HOME/.config/gpu-scout"
install -m 755 "$binary" "$HOME/.local/bin/.gpu-scout.new.$$"
mv "$HOME/.local/bin/.gpu-scout.new.$$" "$HOME/.local/bin/gpu-scout"
install -m 644 "$root/deploy/gpu-scout.service" "$HOME/.config/systemd/user/gpu-scout.service"
if [ ! -f "$HOME/.config/gpu-scout/config.json" ]; then
  printf '{"server":"%s","slack_channel":"%s"}\n' "$server" "$channel" > "$HOME/.config/gpu-scout/config.json"
fi
if [ ! -f "$HOME/.config/gpu-scout/environment" ]; then
  printf 'SLACK_BOT_TOKEN=\n' > "$HOME/.config/gpu-scout/environment"
fi
systemctl --user daemon-reload
printf '%s\n' 'Installed; existing configuration preserved. Service not started.' \
  'Set SLACK_BOT_TOKEN in ~/.config/gpu-scout/environment, run doctor, then:' \
  'systemctl --user enable --now gpu-scout.service'
