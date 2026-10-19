# Privacy notice for the public relay

Effective: 19 October 2026

## Summary

The public relay keeps people in a room in sync. To do that it processes your IP address in memory only, the display name and app name you send, your status in the room, and a description of what the room is watching. Your data in a room is deleted when you leave or the room ends, and a room ends at most 24 hours after it was created; the hosting provider's daily backup of the server's disk can hold a copy for up to a day after that. There are no accounts, no cookies and no tracking. The operator keeps usage counts for at most 400 days; they count how often each app created, joined or resumed a room and hold only app names and numbers.

## Who is responsible

The controller is Edvard Wikhall, in Sweden, contactable at `contact@openwatch.party`.

This notice covers the public relays listed at `https://openwatch.party/relays.json`. It does not cover the app you use, which receives the room's data and has its own privacy policy, or relays that other people run from the published software.

## What the relay processes

### Your connection

When your app connects, the relay sees the IP address of the connection. For its limits it uses an IPv4 address whole and, for IPv6, only its /64, /56 and /48 networks; the full address stays only in the open connection and goes when it closes. Addresses are held in memory only, are never written to disk or to a log, and a restart erases them.

Your app also sends clock pings and the relay answers with its clock time. Nothing is kept from these. To create a room, your app connects to every public relay at once, so every public relay briefly sees your address and pings under the same rules. Joining or resuming connects only to the relay that holds the room. The apps fetch the current relay list from `https://openwatch.party/relays.json` when they first connect, on a later connection once a day has passed, and when a room code points to no known relay.

### You in a room

Your app sends a display name of 1 to 32 characters, which can be almost any text (control and direction-control characters are refused) and does not have to be unique or your real name, and a `client` name that names the app and its version, such as `Example/1.0`. The relay gives you a peer ID and a random resume token. The token is sent to you when you join and again each time your app resumes; the relay keeps only its SHA-256 hash. While you are in the room the relay holds your status: whether you are the host, whether you are connected, your player state, whether your copy matches the room's title and by how much its duration differs, your playback offset, and optionally your connection's round-trip time, which is held in memory only to time the start of playback.

Nobody in the room receives your IP address or your round-trip time.

### What the room watches

The host's app sets a media descriptor: the kind (movie, episode or video), title, year, catalogue IDs such as IMDb, TMDB or TVDB, and for an episode the series title, year and IDs, season and episode. It also holds the duration, optional file hints (size, a file hash, and a file name without directories), optional app-private hints, and any other fields the app adds. The descriptor must not contain media itself or anything that locates or fetches media. Everyone in the room receives the descriptor unchanged.

The relay also holds the room's playback state: the position, whether it is playing, paused or waiting, the playback rate, whether the host or everyone may control playback, and whether the room is locked. With each change it tells everyone in the room which action caused it and who took it, and keeps neither.

### Saved rooms

So that rooms survive a restart, the relay saves each room to its disk. A saved room holds its code, creation time, media descriptor, position, rate, control setting and locked flag, and for each participant their peer ID, token hash, display name, `client` name, host flag, match and offset. It never holds tokens themselves, IP addresses or round-trip times. If a relay restarts, it restores rooms younger than 24 hours, paused, with everyone disconnected and able to resume for 5 minutes. The hosting provider backs up each server's disk once a day and keeps only the latest backup, so what is on the disk, saved rooms included, can remain in that backup for up to a day after it leaves the disk. The operator makes no other copy.

### Usage counts, live counts and logs

Each relay counts, for each UTC day and each app name, the requests it accepts to create, join or resume a room. These daily counts hold no IP addresses, display names, versions, room codes, tokens or titles. The app name is the part of the `client` name before the `/`, which the [terms of use](terms.md) require to name the app; the relay stores it as the app sent it.

The relay also keeps live counts of connections, rooms, participants (including per app name), messages and errors, available only on the server itself; the public deployment runs no service that collects them.

The relay's logs record only events about the relay itself, such as starting, certificate expiry, and errors reading or writing its own files. On the public relays the logs are rotated and firewall logging is off.

## How long it is kept

A room is deleted when the host ends it, when its last participant leaves or is removed, 5 minutes after nobody is connected, or 24 hours after it was created, whichever comes first; a restart does not extend that.

| Data | Where | Kept until |
| --- | --- | --- |
| Your full IP address, in an open connection | Memory | That connection closes |
| IP address (IPv4), or IPv6 /64, /56 and /48, for the join-lookup limit | Memory | At most about 80 seconds after the last join attempt |
| The same, as creator of a room | Memory | The room is deleted, at most 24 hours; a restart drops it |
| IPv4 address or IPv6 /64, for app names it added to the day's usage counts | Memory | Within a minute after that UTC day ends, or a restart |
| Display name, `client` name, peer ID, token hash, host flag, match, offset | Memory and disk | You leave, the host removes you, you stay disconnected 5 minutes, or the room is deleted; off the disk within 5 seconds, and anything left in memory goes when your connection closes |
| Player state and round-trip time | Memory | The same, and anything left goes when your connection closes |
| Media descriptor | Memory and disk | The host replaces or clears it, or the room is deleted |
| Playback state | Memory and disk | The room is deleted, and anything left in memory goes when the connections close |
| Daily counts per app name | Disk | At most 400 days after the day they count |
| Anything on the disk above | The hosting provider's backup of the disk | The next daily backup replaces it, at most about a day after it leaves the disk |

## What the relay doesn't do

There are no accounts or sign-in. The relay sets no cookies and keeps none of the cookies, User-Agent or Origin values a connection may carry. There is no tracking, analytics or advertising, and the relay makes no outbound connections except to its certificate authority, to obtain and renew its certificate.

The relay never receives, fetches or links to video; each person plays their own copy. There is no profiling and no automated decision with legal or similar effect on you. Apart from protocol errors, the only automatic refusals are the published limits and the list of refused app names. Your data is not sold, and it is shared only as described below.

## Who receives it

- Everyone in your room and their apps receive your peer ID, display name, `client` name, host and connection status, player state, match and offset, together with the media descriptor and playback state. Anyone who has the room code can join, up to 8 participants, until the host locks the room.
- Hosting providers host the servers, process the traffic to them and hold the daily backup of each server's disk.
- Authorities receive data only when the law requires it, and then only what still exists: there is nothing about a room deleted more than a day ago, and no address that is no longer in memory.

## Where it is processed

The public relays are those listed in `https://openwatch.party/relays.json`; each entry names where it runs. A room lives its whole life on one relay; the relays share no data and never contact each other. Joining a room held on another relay therefore sends your data to that relay. For a relay outside the EEA, the safeguard relied on is the standard contractual clauses adopted by the European Commission, part of the hosting provider's data processing agreement (GDPR Article 46(2)(c)). You can ask for a copy at `contact@openwatch.party`.

## Purposes and legal bases

References are to the EU and UK GDPR.

- Running the room you ask to be in, covering the room data described above: necessary to provide the service you request under the [terms of use](terms.md), Article 6(1)(b).
- Applying limits against abuse, using IP addresses in memory only: the operator's legitimate interest in protecting the public relay, the people using it and the accuracy of its usage counts from abuse and overload, Article 6(1)(f).
- Complying with the law where a legal obligation requires a disclosure: Article 6(1)(c).

The daily counts per app name count the use of apps, not people: the terms require an app name to name an app.

## Your rights

You have the rights of access, rectification, erasure, restriction, portability, and objection to processing based on legitimate interest. Send any request to `contact@openwatch.party`; it will be answered within one month.

In practice, your app already receives everything the room holds about you, leaving a room removes you from it, and the host ending a room deletes it. When a room ends, its names, app names, token hashes and descriptor are cleared, and what is left of the room in memory goes when the connections close. After that, at most an IP address remains in memory for the limits described above, with no name, room or title attached, and the daily disk backup can hold the room for up to a day.

You may complain to Integritetsskyddsmyndigheten (IMY), the Swedish Authority for Privacy Protection, at [imy.se/en](https://www.imy.se/en/), or to the supervisory authority where you live or work, or where you believe the infringement took place.

## Security

Connections use TLS 1.2 or later. The public relays terminate the encrypted connection themselves, so no proxy or content delivery network in between decrypts your traffic, but the relay is not end-to-end encrypted: it reads the room's data in order to run the room. If you do not want an operator to be able to read what a room watches, you can use a [relay you run yourself](../relay/README.md). Saved room files are readable only by the relay's user account on the server. How to report a vulnerability is described in [SECURITY.md](../SECURITY.md).

## Changes to this notice

This notice is updated when what the relay processes changes. Every change is published in this file in the repository at [github.com/edde746/openwatchparty](https://github.com/edde746/openwatchparty), and the effective date above is updated.
