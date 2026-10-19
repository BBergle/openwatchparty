# Contributing

Send changes as pull requests to `github.com/edde746/openwatchparty`; CI runs on every push and pull request, and the owner decides what merges. Issues are enabled, discussions are not. Report vulnerabilities per [SECURITY.md](SECURITY.md), not in an issue. Automated contributors follow [AGENTS.md](AGENTS.md) (what changes together, ports, never-run list).

## Prerequisites

Go 1.26 (spec, relay, conformance, vectors, and every SDK's integration tests, which build the relay); Node 22+ for the TypeScript SDK (22.18+ for `owp-peer`, CI uses 24 for the website); Dart 3.4+ (formatting 3.7+); JDK 17 or newer for Kotlin (CI uses 21), plus an Android SDK for the Android target and macOS with Xcode for the Apple targets; a Swift 6 toolchain for Swift (Xcode on Apple platforms); Docker for the image.

## Build and test

Run the checks for each part you touch before opening a pull request: [AGENTS.md](AGENTS.md#checks-per-area) lists the exact commands per area. Component guides: [relay conformance](conformance/relay/README.md), [vectors](conformance/vectors/README.md), and the SDK APIs ([TypeScript](sdk/typescript/README.md), [Dart](sdk/dart/README.md), [Kotlin](sdk/kotlin/README.md), [Swift](sdk/swift/README.md)).

## Changing the protocol

Propose protocol changes as pull requests touching `spec/`. Within v1 a change must be ignorable by an existing peer (new optional fields, message types, open-enum values, `/v1/info` keys, ID namespaces); anything else is v2 at `/v2/…` — see [SPEC.md §4](spec/SPEC.md) and [SDK.md](spec/SDK.md). Relay policy values are settings in `GET /v1/info` and need no client release, except `resumeMs`, for which SDKs assume 300000 on public relays: raising it changes SDK.md and all four SDKs. Most protocol changes move several parts at once: check the [what-changes-together table](AGENTS.md#what-changes-together) first, and land each row's changes together in one pull request.

## Commit messages

`type(scope): subject` ([Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)): lowercase, imperative, no trailing period; the body says what changed and why. Types: `fix`, `feat`, `docs`, `test`, `ci`, `chore`. Scopes: `spec`, `relay`, `conformance`, `vectors`, `peer`, `examples`, `website`, `sdk-typescript`, `sdk-dart`, `sdk-kotlin`, `sdk-swift`, `sdk` (all four); no scope when spanning parts, as in `ci: test the relay on the latest Go 1.26 patch`. Breaking changes take `!` after the scope and a `BREAKING CHANGE:` footer. No CLA, no `Signed-off-by`.

## Style and license

Go `gofmt`; Dart format width 120, strict analyzer; TypeScript `strict` with `noUncheckedIndexedAccess`; Kotlin `explicitApi()` and official style; Swift 6 language mode, no warnings. Prose: plain and short, no hard line breaks in Markdown. No license headers in source files. Contributions are under [Apache-2.0](LICENSE) (§5); submit only work you may license that way, and others' code only under an Apache-2.0-compatible license.
