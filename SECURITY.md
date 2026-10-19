# Security

## How to report

Report privately through [GitHub](https://github.com/edde746/openwatchparty/security/advisories/new) or to contact@openwatch.party, not in a public issue or pull request. Say what you found, the component and version, steps to reproduce, and expected impact.

## In scope

- The relay ([`relay/`](relay/README.md), image `ghcr.io/edde746/owp-relay`, the public relays at https://openwatch.party/relays.json), the SDKs ([TypeScript](sdk/typescript/README.md), [Dart](sdk/dart/README.md), [Kotlin](sdk/kotlin/README.md), [Swift](sdk/swift/README.md)), and [`spec/`](spec/SPEC.md) where a rule makes a flaw unavoidable for any implementation.
- Examples: acting in a room you are not in, resuming as another participant, or acting as host without being host; getting around the relay's limits or making it spend CPU or memory far beyond the traffic; crashing it, making it log names, codes, tokens, descriptors or addresses, write tokens or addresses to disk, or show room data to anyone outside the room; making an SDK fetch, open or run something, or crash its app, from a malicious relay or participant.

## Out of scope

- Apps built on OWP (report to their developers) and other people's relays (report their setup to their operator).
- By design: anyone with a code can join until the host locks the room; the relay reads descriptors and positions (v1 has no end-to-end encryption); codes can in principle be guessed, slowed by join limits.
- Denial of service by traffic volume alone; scanner reports without demonstrated impact.

## Testing rules

Test against a relay you run yourself ([relay guide](relay/README.md)). On the public relays, use only your own rooms, leave others' rooms and data alone, do not load-test or flood, and stop once shown. Research following these rules is allowed under [Acceptable use](docs/terms.md#acceptable-use); see also the [privacy notice](docs/privacy.md).

## What to expect

Acknowledgement within 7 days, coordinated disclosure within 90 days, fixes in the latest relay and SDK releases, a GitHub advisory with a CVE when one applies, and credit unless you decline it. No bug bounty.
