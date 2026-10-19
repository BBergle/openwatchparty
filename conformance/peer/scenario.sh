#!/usr/bin/env bash
# One owp-peer host and one owp-peer guest in a room on a running relay: once the guest is in, they share media, start
# together, pause, seek, play at 1.5x and end the room, checking at each stage that both simulated players follow the
# room. Prints both peers' output and exits non-zero when either peer fails.
#
# The host shares only after the guest has joined (within --timeout), so the room's start waits for both players
# however late the guest's process starts. A guest that joined a room already playing would open its file at 0 and,
# 350-2000 ms behind, be nudged at 4% rather than seeked: up to (2000 - 350) / 0.04 = 41 s to come within 350 ms.
#
# usage: conformance/peer/scenario.sh http://127.0.0.1:18200
set -euo pipefail

relay="${1:?usage: scenario.sh RELAY_BASE_URL}"
here="$(cd "$(dirname "$0")" && pwd)"
peer=(node "$here/owp-peer.js" --relay "$relay" --timeout 20)
media='{"kind":"movie","title":"Heat","year":1995,"ids":{"imdb":"tt0113277"},"durationMs":600000}'
out="$(mktemp -d)"
trap 'kill $(jobs -p) 2>/dev/null || true; rm -rf "$out"' EXIT

"${peer[@]}" --name Host create \
  wait participants=2 \
  media "$media" \
  wait phase=playing Guest.player=ready \
  sleep 2000 expect me.drift=0~350 \
  pause wait phase=paused \
  seek 120000 wait position=120000 \
  sleep 1500 play wait phase=playing \
  rate 1.5 wait rate=1.5 \
  sleep 3000 expect me.drift=0~350 \
  end >"$out/host" &
host=$!

code=""
for _ in $(seq 100); do
  code="$(awk '$1 == "created" { print $2 }' "$out/host")"
  [ -n "$code" ] && break
  sleep 0.1
done
if [ -z "$code" ]; then
  cat "$out/host"
  echo "scenario: the host did not create a room" >&2
  exit 1
fi

"${peer[@]}" --name Guest join "$code" \
  wait phase=playing me.player=ready \
  sleep 1500 expect me.drift=0~350 \
  wait phase=paused position=120000 me.position=120000 \
  wait phase=playing rate=1.5 \
  sleep 1500 expect me.drift=0~350 \
  stay >"$out/guest" &
guest=$!

host_exit=0
guest_exit=0
wait "$host" || host_exit=$?
wait "$guest" || guest_exit=$?
sed 's/^/host  | /' "$out/host"
sed 's/^/guest | /' "$out/guest"
echo "host exited $host_exit, guest exited $guest_exit"
[ "$host_exit" -eq 0 ] && [ "$guest_exit" -eq 0 ]
