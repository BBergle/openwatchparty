# Scripted peer

`owp-peer` is a scripted participant on the TypeScript SDK: it creates or joins a room and runs a list of steps, so an app under test has someone else in the room. It imports the SDK's TypeScript sources directly, so it needs this repository and Node 22.18+ (23.6+ on 23, any 24+); nothing to install or build. Run it from the repository root.

## Quick start

Host (prints `created <CODE> p1`, stays until Ctrl-C), against a [local relay](../../relay/README.md):

```sh
node conformance/peer/owp-peer.js --relay http://127.0.0.1:8080 --name Ann create \
  media '{"kind":"movie","title":"Heat","year":1995,"durationMs":600000}' stay
```

Guest with a file 10 s shorter, accepting the mismatch and checking sync:

```sh
node conformance/peer/owp-peer.js --relay http://127.0.0.1:8080 --name Bob --file 590000 --timeout 45 join K7MQ2X \
  accept wait me.match=mismatched phase=playing sleep 1500 wait me.drift=0~350 stay
```

`--timeout 45` covers a guest that starts 0.35–2 s behind; it plays 4% faster and catches up in up to about 40 s. Guest output, excerpt (`ended left` is after Ctrl-C):

```
joined K7MQ2X p2
media Heat kind=movie duration=600000
room rev=5 epoch=1 phase=playing position=0 rate=1 control=host locked=false host=Ann participants=2 waitingOn=0
step accept
mismatch delta=-10000
match mismatched delta=-10000
player ready
step wait me.match=mismatched phase=playing
step wait me.drift=0~350
step stay
ended left
```

## Usage and options

```
usage: owp-peer [options] create [steps…]
       owp-peer [options] join CODE [steps…]
```

`--help` prints the full usage (options, steps, conditions, exits) and exits 0.

| Option | Default | Meaning |
|---|---|---|
| `--relay URL\|public` | `public` | A relay base URL, or the SDK's public list; a `ws(s)://…/v1/ws` URL becomes its `http(s)://` base |
| `--name NAME` | `Peer` | Display name, 1-32 characters |
| `--client APP/VER` | `owp-peer/0.1.0` | Client name |
| `--file MS\|none` | the media's duration | The file this peer finds for shared media: its duration, or none |
| `--timeout S` | `30` | How long `wait`, `accept`, `decline` and `end` may take |
| `--json` | off | Print events as JSON lines |

`join` accepts any code form the SDK normalizes (lower case, `ct6-3qx`). Steps run in order; after the last, the peer leaves. A refused command prints `error` and the script goes on.

Command steps other than `end` don't wait for the relay: they send and the script goes on. A step that depends on an earlier step's effect needs an explicit `wait` on that effect first, as in `media … wait phase=playing pause wait phase=paused`, or `pause wait phase=paused seek 120000 wait position=120000` before an `expect` on the new position. Without the `wait`, `pause` right after `media` is refused (`error invalid_message`): the party hasn't seen the media yet. An `expect` right after `seek` still sees the old position.

## Steps

| Step | Effect |
|---|---|
| `media JSON\|@FILE [MS]` | Host: share a descriptor ([SPEC.md](../../spec/SPEC.md) §10), inline or from your own file, from MS (default 0; a bad descriptor is a usage error); the host plays a simulated file of its duration |
| `file MS\|none` | Change the file found. With none of this peer's files chosen, search again. With one chosen, `file MS` plays the new file at once, with no prompt. `file none` keeps the chosen file playing |
| `accept` / `decline` | Wait for the mismatch prompt, then answer it |
| `attach` / `detach` | Attach/detach the simulated player (attached by default) |
| `play` `pause` `seek MS` `rate R` `offset MS` | Party commands; `seek` is in this peer's file, `rate` 0.5-2.0, `offset` whole ms (local = room + offset) |
| `stall MS` | Player buffers for MS, script goes on; no file yet: `error no_player re=stall` |
| `control host\|everyone` `lock on\|off` `transfer WHO` `kick WHO` `clear` `end` `leave` | Room commands; WHO is a name or peer ID; `clear` clears media; `end` waits for the relay to end the room |
| `sleep MS` | Wait |
| `wait COND…` | Until all conditions hold at once, or fail after `--timeout` |
| `expect COND…` | All hold now, or fail |
| `stay` | Until the room ends or SIGINT/SIGTERM |

## Conditions

`KEY=VALUE`; numbers take `~tolerance` (`position=61000~500` = 60500-61500). Missing text reads `none`; a missing number never holds. Room keys: `phase` (`idle paused waiting playing`), `participants` (count, disconnected included), `waitingOn` (count), `position`, `rate`, `control` (`host everyone`), `locked`, `host` (name, peer ID or `none`), `connection` (`idle connecting connected reconnecting ended`), `ended` (`left ended kicked expired replaced lost`). Participant keys `WHO.key` (WHO a name, peer ID or `me`): `player` (`idle loading ready buffering`), `match` (`searching matched mismatched unavailable`), `delta` (file − media duration), `offset`, `connected`. Only `me` has `me.position` and `me.drift` (player − (room + offset)).

## Player, output, exits

The resolver returns one candidate of the `--file`/`file` duration, or none; more than 1.5 s off the media's duration is a mismatch. The simulated player runs on the local clock at its rate, stops at the file's end, and takes commands at once.

One line per event on stdout, `created`/`joined` first: `room`, `status`, `participant joined|left`, `action <kind> by=…`, `media <title>|none`, `match`, `mismatch`, `player`, `connection` (only in/out of `reconnecting`), `error <code> re=… message=…`, `step`, `fail <step> reason=…` (unmet conditions with values), `ended <reason>`. `--json` prints the same as JSON objects, e.g. `{"type":"created","code":"MQT5MH","peerId":"p1"}`, `{"type":"ended","reason":"left"}`.

Exit 0: all steps ran, or in `stay` the party ended (other than `lost`) or a signal came. Exit 1: a `wait`/`expect`/`accept`/`decline`/`end` failed, or a signal outside `stay`. Exit 2: usage (option, step, condition, name, client, descriptor, Node version). Exit 3: create/join failed or the party ended unexpectedly (not after `leave`/`end`, and not while a `wait` holds, so `wait ended=kicked` works).

## `scenario.sh`

`conformance/peer/scenario.sh http://127.0.0.1:8080` runs a host and guest against a running relay. The guest joins. The host shares. Both start together. Then pause, seek, play again, 1.5x, end. The output is prefixed `host  | `/`guest | `. Exits non-zero if either peer fails. See the [TypeScript SDK](../../sdk/typescript/README.md), [SDK.md](../../spec/SDK.md) and the [integration guide](../../docs/integration.md).
