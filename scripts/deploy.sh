#!/bin/sh
set -eu

if [ "$#" -lt 1 ]; then
  echo 'Usage: sh scripts/deploy.sh SLACK_CHANNEL_ID [dencun pectra]' >&2
  exit 2
fi
channel=$1
shift
case "$channel" in C*|G*) ;; *) echo 'Invalid Slack channel ID' >&2; exit 2;; esac
case "$channel" in *[!A-Z0-9]*) echo 'Invalid Slack channel ID' >&2; exit 2;; esac
if [ "${#channel}" -lt 3 ]; then
  echo 'Invalid Slack channel ID' >&2
  exit 2
fi
if [ "$#" -eq 0 ]; then
  set -- dencun pectra
fi
for server in "$@"; do
  case "$server" in dencun|pectra) ;; *) echo 'Only dencun and pectra are supported.' >&2; exit 2;; esac
done

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
archive=$root/dist/gpu-scout-linux-amd64.tar.gz
if [ ! -f "$archive" ]; then
  echo 'Release archive missing; run make linux first.' >&2
  exit 1
fi
for server in "$@"; do
  printf 'Deploying %s\n' "$server"
  ssh "$server" 'umask 077; mkdir -p "$HOME/.cache/gpu-scout-deploy"; chmod 700 "$HOME/.cache/gpu-scout-deploy"'
  scp "$archive" "$server:.cache/gpu-scout-deploy/release.tar.gz"
  ssh "$server" sh -s -- "$server" "$channel" <<'REMOTE'
set -eu
server=$1
channel=$2
stage=$HOME/.cache/gpu-scout-deploy
cd "$stage"
tar -xzf release.tar.gz
sha256sum -c SHA256SUMS
sh scripts/install.sh ./gpu-scout "$server" "$channel"
environment=$HOME/.config/gpu-scout/environment
if ! grep -q '^SLACK_BOT_TOKEN=.' "$environment"; then
  printf '%s: installed; set SLACK_BOT_TOKEN in %s and rerun deployment. Service was not started or restarted.\n' "$server" "$environment"
  exit 0
fi
systemd-run --user --wait --pipe --collect \
  --property="EnvironmentFile=$environment" \
  "$HOME/.local/bin/gpu-scout" -config "$HOME/.config/gpu-scout/config.json" doctor
systemctl --user enable gpu-scout.service
systemctl --user restart gpu-scout.service
sleep 1
systemctl --user --quiet is-active gpu-scout.service
printf '%s: GPU/Slack preflight passed; service is active.\n' "$server"
REMOTE
done
