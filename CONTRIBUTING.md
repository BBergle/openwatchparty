# Contributing

Send changes as pull requests to `github.com/edde746/openwatchparty`. CI runs on every push and pull request, and the owner decides what merges.

Issues are enabled; discussions are not. Report vulnerabilities as [SECURITY.md](SECURITY.md) describes, not in an issue. Automated contributors also follow [AGENTS.md](AGENTS.md), which lists what changes together, the test ports and what never to run.

## Prerequisites

Install the toolchains for the parts you work on:

- **Go 1.26** for the spec checks, the relay, the conformance suite and the test tables. Every SDK's integration tests also need Go, because they build the relay.
- **Node 22 or later** for the TypeScript SDK. `owp-peer` needs 22.18 or later, and the website's CI uses Node 24.
- **Dart 3.4 or later** for the Dart SDK. Formatting checks need Dart 3.7 or later.
- **JDK 17 or later** for the Kotlin SDK (CI uses 21). The Android target also needs an Android SDK, and the Apple targets need macOS with Xcode.
- **A Swift 6 toolchain** for the Swift SDK, which comes with Xcode on Apple platforms.
- **Docker** to build the relay image.

## Build and test

Before you open a pull request, run the checks for each part you touched. [AGENTS.md](AGENTS.md#checks-per-area) lists the exact commands for each area.

Each component has its own guide: the [relay conformance suite](conformance/relay/README.md), the [test tables](conformance/vectors/README.md), and the SDKs for [TypeScript](sdk/typescript/README.md), [Dart](sdk/dart/README.md), [Kotlin](sdk/kotlin/README.md) and [Swift](sdk/swift/README.md).

## Changing the protocol

Propose protocol changes as pull requests that touch `spec/`.

Within v1, a change must be one that an existing peer can safely ignore, such as a new optional field, a new message type, a new value of an open enum, a new `/v1/info` key or a new ID namespace. Any other change is v2, served at `/v2/…`. See [SPEC.md §4](spec/SPEC.md) and [SDK.md](spec/SDK.md).

Relay policy values are each relay's choice, and `GET /v1/info` reports them. Changing one needs no client release, with one exception: `resumeMs` is fixed at 300000 on every v1 relay, and clients rely on that value.

Most protocol changes move several parts at once. Check the [what-changes-together table](AGENTS.md#what-changes-together) first, and land all the changes in a row together, in one pull request.

## Commit messages

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/): `type(scope): subject`, lowercase and imperative, with no trailing period. The body says what changed and why.

- Types: `fix`, `feat`, `docs`, `test`, `ci`, `chore`.
- Scopes: `spec`, `relay`, `conformance`, `vectors`, `peer`, `examples`, `website`, `sdk-typescript`, `sdk-dart`, `sdk-kotlin`, `sdk-swift`, and `sdk` for all four SDKs.
- A change that spans several parts has no scope, as in `ci: test the relay on the latest Go 1.26 patch`.
- A breaking change puts `!` after the scope and adds a `BREAKING CHANGE:` footer.

There is no CLA, and commits need no `Signed-off-by`.

## Code style

- Go: `gofmt`.
- Dart: format width 120 and the strict analyzer.
- TypeScript: `strict` with `noUncheckedIndexedAccess`.
- Kotlin: `explicitApi()` and the official style.
- Swift: Swift 6 language mode, with no warnings.

Source files have no license headers.

## Writing

Documents, READMEs and public API doc comments should be easy to read, not just short:

- **Orient first.** Start each document and each major section with what it covers, then give the detail.
- **One idea per paragraph,** in two to four sentences. When a paragraph collects many facts, turn it into a list, a table or a code example.
- **Use the structure the content has.** Steps in order become a numbered list, parallel rules or options a bulleted list, and reference material a table with a short "what it does" column.
- **Write full sentences** with one main idea each, in plain words, present tense and active voice. Avoid semicolon chains, nested parentheses and telegraphic shorthand.
- **Use code formatting only for things you type or see in code,** such as identifiers, values, paths and commands.
- **Address the reader as "you"** in guides and READMEs. SPEC.md and SDK.md use the third person and RFC 2119 keywords.
- **Doc comments start with a one-sentence summary,** then add details.
- No filler, hype or emojis. Put each paragraph, list item and table row on one line, with no hard line breaks. Keep heading text stable, because other documents link to it.

## License

Contributions are licensed under [Apache-2.0](LICENSE), as its section 5 describes. Submit only work you may license that way, and include others' code only under an Apache-2.0-compatible license.
