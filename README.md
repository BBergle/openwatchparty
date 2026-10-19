# Open Watch Party

Open Watch Party (OWP) lets people watch one title together even when each of them uses a different app. It is an open protocol, a free public relay and client SDKs for TypeScript, Dart, Kotlin and Swift.

## How it works

Someone creates a room and shares its 6-character code. Everyone who joins plays their own copy of the title in their own app.

The relay holds the room's shared timeline. It sees the title and the playback position, but never a file or a stream. In each app, the SDK keeps the player on that timeline.

Your app supplies two pieces of code:

- A **player adapter**, which lets the SDK read and control your player.
- A **resolver**, which finds the user's own copy of the title the host picked.

## Where to start

| You are | Start here |
|---|---|
| An app developer | The [integration guide](docs/integration.md), then the SDK for your language: [TypeScript](sdk/typescript/README.md), [Dart](sdk/dart/README.md), [Kotlin](sdk/kotlin/README.md) or [Swift](sdk/swift/README.md). The [web example](examples/web/README.md) is a complete page, and [`owp-peer`](conformance/peer/README.md) is a scripted participant for testing your app. |
| A relay operator | The [relay guide](relay/README.md) and its [self-hosting](relay/README.md#self-hosting) section. |
| Implementing OWP in another language | The protocol in [SPEC.md](spec/SPEC.md), client behaviour in [SDK.md](spec/SDK.md), the [test tables](conformance/vectors/README.md) and the [relay suite](conformance/relay/README.md). |
| A contributor | [CONTRIBUTING.md](CONTRIBUTING.md) and, for automated contributors, [AGENTS.md](AGENTS.md). |

## Repository map

| Path | What it is |
|---|---|
| `spec/` | The protocol: SPEC.md (wire protocol and relay), SDK.md (client behaviour), JSON schemas and examples |
| `relay/` | The relay, `owp-relay`, written in Go, with its Dockerfile and `compose.yaml` |
| `conformance/` | The `owp-conform` relay suite, the test tables every SDK runs, and the `owp-peer` scripted participant |
| `sdk/typescript/` | The TypeScript SDK, npm package `openwatchparty` |
| `sdk/dart/` | The Dart SDK, pub.dev package `openwatchparty` |
| `sdk/kotlin/` | The Kotlin Multiplatform SDK, Maven `party.openwatch:openwatchparty` |
| `sdk/swift/` | The Swift SDK, SwiftPM product `OpenWatchParty` |
| `examples/web/` | A web page with an HTML `<video>` player, built on the TypeScript SDK |
| `docs/` | The integration guide, the terms of use and the privacy notice |
| `relays.json` | The public relay list |
| `website/` | The https://openwatch.party site, built with VitePress |

## Public relay

Open-source apps may use the public relays for free, without asking. Any other app needs written permission first: write to contact@openwatch.party. The [terms of use](docs/terms.md#who-may-use-the-public-relay) give the rules, and the [privacy notice](docs/privacy.md) says what the relays keep.

## Security

Report vulnerabilities privately, as [SECURITY.md](SECURITY.md) describes. Don't open a public issue.

## License

The repository is licensed under [Apache-2.0](LICENSE).
