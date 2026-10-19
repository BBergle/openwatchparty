# Terms of use of the public relay

Effective: 19 October 2026

## Summary

These terms say who may use the public relay and how. In short:

- An open-source app may use the public relay free of charge, with no registration, key or permission.
- Any other app needs written permission before it connects.
- Every app must send its real app name in its `client` name.

## Who runs it and what these terms cover

The public relay is run by Edvard Wikhall, in Sweden. You can reach the operator at contact@openwatch.party.

The public relay means the relays listed at https://openwatch.party/relays.json. These terms cover connecting to those relays, and distributing an app that connects to them.

Connecting, or distributing an app that connects, means you accept these terms. If you do not accept them, do not connect.

These terms cover the hosted service only. The specification, the relay software and the SDKs are licensed under Apache-2.0, and these terms do not change that license.

A relay you run yourself is not the public relay, and these terms do not apply to it. Apps using a self-hosted relay cannot share rooms with apps using the public relay.

## Definitions

These words have a specific meaning in these terms:

- An app is any program that connects to the public relay, whether directly or through an SDK.
- The app name is the part of an app's `client` name before the first `/`.
- An open-source app is an app whose own source code is published for anyone to read and download, under one or more licenses approved by the Open Source Initiative. The published source has to be the source of the version that connects.

The Open Source Initiative lists the approved licenses at [opensource.org/licenses](https://opensource.org/licenses).

The open-source definition looks only at the app's own code, not at third-party libraries or platform components. Selling the app does not matter if its source is public under such a license.

## Who may use the public relay

An open-source app may use the public relay free of charge, with no registration, key or permission. Any other app needs written permission before it connects. If an app stops being open source, it needs permission from then on.

To ask for permission, write to contact@openwatch.party. The terms of a permission, including any payment, are agreed privately in writing. Email counts as writing.

A permission covers that app and its app name only. It may set terms for its app that differ from these terms.

Anyone may instead run their own relay from the published software, for any app, without asking. See [running your own relay](../relay/README.md).

## The `client` name

Every request to create or join a room carries a `client` name, which names the app and its version. It has the form `<app>/<version>`, for example `Example/1.0`, with two limits:

- It is between 1 and 64 characters long.
- It has at least one character on each side of the first `/`.

Anything else is refused as an invalid message. The exact format is in the [specification](../spec/SPEC.md#63-name-and-client).

The `<app>` part must name the real app sending the message. The SDKs take the client name from the app, with no default. Everyone in a room can see each participant's client name.

### Refused apps

The operator can refuse an app by putting its app name on the relay's deny list. The version is ignored, so a refusal covers every version. It also covers every spelling that differs only in ASCII letter case.

The relay answers these requests with the error `client_blocked`, "this relay does not serve this app":

- A refused app's requests to create or join a room.
- A request to resume as a participant whose stored client name is refused.

## Usage records

Each relay counts the requests it accepts to create, join or resume a room, for each UTC day and each app name. This is the record of which apps use the public relay, and how much.

The counts hold no IP addresses, display names, versions, room codes, tokens or titles. The operator keeps usage counts for at most 400 days. The [privacy notice](privacy.md) describes in full what the relay processes.

## Acceptable use

These are the rules for using the public relay:

- Use the public relay only for rooms in which people watch a title together.
- Do not use it as a general message bus. Put nothing unrelated to watching together in names, media descriptors, `ext` data or any other field, and do not use it to pass data between programs.
- Send only messages that follow the specification.
- Put nothing in a media descriptor, including its `ext` data, that locates or fetches media, such as URLs, magnet links, info hashes or stream links. The relay can't detect these, so this is the sender's responsibility.
- Keep within the limits the relay publishes at `GET /v1/info`. If the relay answers `rate_limited`, slow down. Do not retry in a loop after a refusal.
- Do not guess, enumerate or collect room codes, and do not use codes obtained that way.
- Do not get around a limit, for example by spreading connections over many IP addresses.
- Do not disrupt, overload or attack the relay, and do not test its capacity. Security research that follows the rules in [SECURITY.md](../SECURITY.md) is allowed.
- Do not use the public relay for anything unlawful.

## Telling your users

An app that uses the public relay must tell its users that it does so. It must also give them access to the [privacy notice](privacy.md), for example by linking to it from the app's own privacy policy.

## When these terms are broken

If an app breaks these terms, the operator may refuse its app name on the public relay and end any permission given for it.

## The service

The public relay is provided "as is" and "as available", without warranty, as far as the law allows.

There is no guarantee of availability:

- The public relay may be slow, interrupted or unavailable.
- The operator may change its limits, relays or software, or stop it, at any time without notice.
- A failure can end rooms.

## Liability

As far as the law allows, the operator is not liable for indirect or consequential damages, or for lost profits or lost data.

The operator's total liability is capped at what you paid for the public relay in the 12 months before the claim. For a free open-source app, that is zero.

Nothing in these terms limits liability where the law does not allow a limit, such as for intent, gross negligence, death or personal injury caused by negligence, or mandatory consumer rights.

## Ending

You may stop using the public relay at any time. The operator may suspend or end an app's or a person's use at any time.

The sections on usage records, liability and governing law continue to apply after use ends.

## Changes to these terms

Changes are published in this file, in the repository at [github.com/edde746/openwatchparty](https://github.com/edde746/openwatchparty). The effective date at the top is updated with them.

A change takes effect 30 days after publication. It takes effect on publication instead where the law requires it, or where it is needed to stop abuse. Using the public relay after a change takes effect means you accept it.

A change to these terms does not alter a written permission agreement, unless that agreement says so.

## Governing law and jurisdiction

Swedish law governs these terms, and the Swedish courts have jurisdiction. A consumer keeps the protection of the mandatory law of the country where they live.
