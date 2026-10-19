# Terms of use of the public relay

Effective: 19 October 2026

## Summary

- An open-source app may use the public relay free of charge, with no registration, key or permission.
- Any other app needs written permission before it connects.
- Every app must send its real app name in its `client` name.

## Who runs it and what these terms cover

The public relay is run by Edvard Wikhall, in Sweden. You can reach the operator at `contact@openwatch.party`.

The public relay means the relays listed at `https://openwatch.party/relays.json`. These terms cover connecting to those relays, and distributing an app that connects to them. Connecting, or distributing an app that connects, means you accept these terms. If you do not accept them, do not connect.

These terms cover the hosted service only. The specification, the relay software and the SDKs are licensed under Apache-2.0, and these terms do not change that license. A relay you run yourself is not the public relay and these terms do not apply to it, and apps using a self-hosted relay cannot share rooms with apps using the public relay.

## Definitions

An app is any program that connects to the public relay, whether directly or through an SDK. The app name is the part of an app's `client` name before the first `/`.

An open-source app is an app whose own source code is published for anyone to read and download under one or more licenses approved by the Open Source Initiative ([opensource.org/licenses](https://opensource.org/licenses)), where the published source is the source of the version that connects. This applies to the app's own code, not to third-party libraries or platform components. Selling the app does not matter if its source is public under such a license.

## Who may use the public relay

If an app stops being open source, it needs permission from then on.

To ask for permission, write to `contact@openwatch.party`. The terms of a permission, including any payment, are agreed privately in writing, and email counts as writing. A permission covers that app and its app name only, and may set different terms for its app.

Anyone may instead run their own relay from the published software, for any app, without asking. See [running your own relay](../relay/README.md).

## The `client` name

Every request to create or join a room carries a `client` name in the form `<app>/<version>`, for example `Example/1.0`, between 1 and 64 characters long, with at least one character on each side of the first `/`; anything else is refused as an invalid message. The exact format is in the [specification](../spec/SPEC.md#63-name-and-client).

The `<app>` part must name the real app sending the message. The SDKs take the `client` name from the app, with no default. Everyone in a room can see each participant's `client` name.

The operator can refuse an app by putting its app name on the relay's deny list. The version is ignored, so a refusal covers every version, and every spelling that differs only in ASCII letter case. A refused app's requests to create or join a room, and a request to resume as a participant whose stored `client` name is refused, are answered with the error `client_blocked`, "this relay does not serve this app".

## Usage records

Each relay counts, for each UTC day and each app name, the requests it accepts to create, join or resume a room. This is the record of which apps use the public relay and how much. The counts hold no IP addresses, display names, versions, room codes, tokens or titles. The operator keeps usage counts for at most 400 days. The [privacy notice](privacy.md) describes what the relay processes in full.

## Acceptable use

- Use the public relay only for rooms in which people watch a title together. Do not use it as a general message bus: put nothing unrelated to watching together in names, media descriptors, `ext` data or any other field, and do not use it to pass data between programs.
- Send only messages that follow the specification.
- Put nothing in a media descriptor, including its `ext` data, that locates or fetches media, such as URLs, magnet links, info hashes or stream links. The relay can't detect these, so this is the sender's responsibility.
- Keep within the limits the relay publishes at `GET /v1/info`. If the relay answers `rate_limited`, slow down, and do not retry in a loop after a refusal.
- Do not guess, enumerate or collect room codes, and do not use codes obtained that way.
- Do not get around a limit, for example by spreading connections over many IP addresses.
- Do not disrupt, overload or attack the relay, and do not test its capacity. Security research that follows the rules in [SECURITY.md](../SECURITY.md) is allowed.
- Do not use the public relay for anything unlawful.

## Telling your users

An app that uses the public relay must tell its users that it does so, and give them access to the [privacy notice](privacy.md), for example by linking to it from the app's own privacy policy.

## When these terms are broken

If an app breaks these terms, the operator may refuse its app name on the public relay and end any permission given for it.

## The service

The public relay is provided "as is" and "as available", without warranty, as far as the law allows. There is no guarantee of availability: it may be slow, interrupted or unavailable, the operator may change its limits, relays or software, or stop it, at any time without notice, and a failure can end rooms.

## Liability

As far as the law allows, the operator is not liable for indirect or consequential damages, or for lost profits or lost data. The operator's total liability is capped at what you paid for the public relay in the 12 months before the claim, which is zero for a free open-source app. Nothing in these terms limits liability where the law does not allow a limit, such as for intent, gross negligence, death or personal injury caused by negligence, or mandatory consumer rights.

## Ending

You may stop using the public relay at any time. The operator may suspend or end an app's or a person's use at any time. The sections on usage records, liability and governing law continue to apply after use ends.

## Changes to these terms

Changes are published in this file in the repository at [github.com/edde746/openwatchparty](https://github.com/edde746/openwatchparty), and the effective date above is updated. A change takes effect 30 days after publication, or on publication where the law requires it or it is needed to stop abuse. Using the public relay after a change takes effect means you accept it. A change to these terms does not alter a written permission agreement unless that agreement says so.

## Governing law and jurisdiction

Swedish law governs these terms and the Swedish courts have jurisdiction. A consumer keeps the protection of the mandatory law of the country where they live.
