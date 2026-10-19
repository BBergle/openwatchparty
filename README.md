# Open Watch Party

Open Watch Party (OWP) is an open protocol, a free public relay and client SDKs that let people watch one title together across different apps.

## How it works

Someone creates a room and shares its 6-character code. Each person plays their own copy in their own app. The relay holds the shared timeline (it sees the title and positions, never a file or stream) and the SDK keeps the player on it. The app supplies a player adapter for its player and a resolver that finds its own copy of the host's title.

## Repository map

| Path | What it is |
|---|---|
| `spec/` | SPEC.md (wire protocol), SDK.md (client behaviour), schemas, examples |
| `relay/` | The relay `owp-relay` (Go), Dockerfile, `compose.yaml` |
| `conformance/` | `owp-conform` relay suite, test tables every SDK runs, `owp-peer` scripted participant |
| `sdk/typescript/` | TypeScript SDK, npm `openwatchparty` |
| `sdk/dart/` | Dart SDK, package `openwatchparty` |
| `sdk/kotlin/` | Kotlin Multiplatform SDK, `party.openwatch:openwatchparty` |
| `sdk/swift/` | Swift SDK, SwiftPM product `OpenWatchParty` |
| `examples/web/` | An HTML `<video>` page on the TypeScript SDK |
| `docs/` | Integration guide, terms, privacy notice |
| `relays.json` | The public relay list |
| `website/` | https://openwatch.party (VitePress) |

## Where to start

| You are | Start here |
|---|---|
| App developer | [Integration guide](docs/integration.md) · [TypeScript](sdk/typescript/README.md) · [Dart](sdk/dart/README.md) · [Kotlin](sdk/kotlin/README.md) · [Swift](sdk/swift/README.md) · [web example](examples/web/README.md) · [`owp-peer`](conformance/peer/README.md) |
| Relay operator | [Relay guide](relay/README.md) · [self-hosting](relay/README.md#self-hosting) |
| Implementer in another language | [SPEC.md](spec/SPEC.md) · [SDK.md](spec/SDK.md) · [test tables](conformance/vectors/README.md) · [relay suite](conformance/relay/README.md) |
| Contributor | [CONTRIBUTING.md](CONTRIBUTING.md) · [AGENTS.md](AGENTS.md) |

## Public relay, security, license

Open-source apps use the public relays free without asking; any other app needs written permission first (contact@openwatch.party): see [the terms](docs/terms.md#who-may-use-the-public-relay) and [privacy notice](docs/privacy.md). Report vulnerabilities per [SECURITY.md](SECURITY.md), not in a public issue. The repository is [Apache-2.0](LICENSE).
