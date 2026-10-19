# Changelog

## 1.0.0

- First release: `WatchParty` with `create()`, `join()`, `leave()` and `close()`, an optional resolver and injectable `connect`, `nowMs` and `random`, implementing the client behaviour in [`spec/SDK.md`](../../spec/SDK.md).
- `Relays.public()`, the public relays with background and forced refresh and injectable `httpGet`, and `Relays.at(baseUrl)`, one self-hosted relay.
- Broadcast `states` and `events`, resolver matching with probes and the mismatch flow, and a `PlayerAdapter` apps extend, whose `play` throws when the player refuses to start, so the SDK reports `loading` and emits `PlayBlockedEvent`; `detachPlayer` and `close()` return release completions. `parseMedia` decodes media descriptors.
- Playback controls (`play`, `pause`, `seek`, `setRate`), a local `setOffset`, host controls (`setMedia`, `clearMedia`, `setControl`, `transferHost`, `kick`, `lock`, `end`), reconnect and resume after a drop, which sends a `setMedia` lost on the drop again while still host, and `PartyException` errors with `ErrorCode` constants.
- Pure Dart from 3.4, on native platforms and the web and in Flutter apps, with the test suite running every table in `conformance/vectors/`.
