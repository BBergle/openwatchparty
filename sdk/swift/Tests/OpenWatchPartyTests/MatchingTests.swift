import Foundation
import Testing
@testable import OpenWatchParty

/// The matching rules of SDK.md §9 that `party.json` doesn't pin, by hand and with probes, in virtual time.
@MainActor
@Suite(.timeLimit(.minutes(1)))
struct MatchingTests {
    /// A resolver that records the titles it was asked for and answers `answer`.
    private func recording(_ resolved: Box<[String]>, _ answer: @escaping @MainActor (Media) async throws -> [Candidate<String>] = { _ in [] }) -> Resolver<String> {
        Resolver { media in
            resolved.value.append(media.title)
            return try await answer(media)
        }
    }

    @Test func theOwnChoiceCarriesTheDescriptorsDurationAndNoFileDetails() async throws {
        // party.json shows a choice as its id and ref only.
        let s = try await joinedSession(host: true)
        var shared = movie
        shared.file = MediaFile(sizeBytes: 4_700_000_000, oshash: "8e245d9679d31e12", name: "Heat.mkv")
        try s.party.setMedia(shared, ref: "heat.mkv")
        _ = try await s.socket.next("setMedia")
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, shared.toJSON(), host: true))
        await s.clock.run()
        expectOwnMedia(s.state, shared)
    }

    @Test func aProbeThatThrowsACancellationOfItsOwnReturnedNothingAndTheWalkGoesOn() async throws {
        let clock = VirtualClock()
        let probes = [
            Candidate<String>(id: "a", ref: "a", probe: {
                // Its own timeout: it gives up after a second with a cancellation of its own.
                try await clock.sleep(1_000)
                throw CancellationError()
            }),
            Candidate<String>(id: "b", ref: "b", probe: { throw CancellationError() }),
            Candidate<String>(id: "c", ref: "c", probe: { 600_000 }),
        ]
        let s = try await joinedSession(clock: clock, resolver: Resolver { _ in probes })
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await clock.advance(by: 2_000)
        #expect(s.state.choice?.id == "c")
        #expect(s.state.match == Match(.matched, deltaMs: 0))
    }

    @Test func aResolverThatThrowsACancellationOfItsOwnFailedAndOneTheSdkCancelledDidnt() async throws {
        let clock = VirtualClock()
        let s = try await joinedSession(clock: clock, resolver: Resolver { media in
            switch media.title {
            case "Heat":
                throw CancellationError()
            case "Ronin":
                // Its own timeout: a cancellation of its own after a second.
                try await clock.sleep(1_000)
                throw CancellationError()
            default:
                return try await clock.never()
            }
        })
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await clock.run()
        #expect(s.events.count == 1)
        #expect((s.events.last as? ResolveFailedEvent)?.error is CancellationError)
        #expect(s.state.match == Match(.unavailable))
        var ronin = movie
        ronin.title = "Ronin"
        s.socket.reply(mediaRoom(rev: 3, epoch: 2, ronin.toJSON()))
        await clock.advance(by: 1_001)
        #expect(s.events.count == 2)
        #expect((s.events.last as? ResolveFailedEvent)?.error is CancellationError)
        #expect(s.state.match == Match(.unavailable))
        // A new epoch cancels the resolver call: no event.
        var collateral = movie
        collateral.title = "Collateral"
        s.socket.reply(mediaRoom(rev: 4, epoch: 3, collateral.toJSON()))
        await clock.run()
        s.socket.reply(mediaRoom(rev: 5, epoch: 4, ronin.toJSON()))
        await clock.run()
        #expect(s.events.count == 2)
    }

    @Test func aResolverTimeoutCarriesTheTimeoutError() async throws {
        let clock = VirtualClock()
        let s = try await joinedSession(clock: clock, resolver: Resolver { _ in try await clock.never() })
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await clock.advance(by: 30_000)
        #expect((s.events.single as? ResolveFailedEvent)?.error as? TimeoutError == TimeoutError(timeoutMs: 30_000))
    }

    // MARK: choose (SDK.md §9.6)

    @Test func cancellingTheCallingTaskCancelsAManualProbe() async throws {
        let clock = VirtualClock()
        let s = try await joinedSession(clock: clock)
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await clock.run()
        let cancelled = Box(false)
        let choosing = Task {
            await s.party.choose(Candidate(id: "a", ref: "a", probe: {
                do {
                    return try await clock.never()
                } catch {
                    cancelled.value = true
                    throw error
                }
            }))
        }
        await clock.run()
        choosing.cancel()
        await clock.run()
        #expect(cancelled.value)
        #expect(await choosing.value == false)
        #expect(s.state.choice == nil)
    }

    @Test func choosingTheChosenIdAgainMakesTheGivenCandidateTheChoice() async throws {
        let s = try await joinedSession(resolver: Resolver { _ in [Candidate(id: "a", ref: "a.mkv", durationMs: 600_000)] })
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await s.clock.run()
        // The app found the same file again under a new ref, a refreshed URL say: the choice is that candidate, all of
        // it, where party.json shows only its id and ref.
        let refreshed = Candidate(id: "a", ref: "a-refreshed.mkv", durationMs: 601_000)
        #expect(await s.party.choose(refreshed))
        #expect(s.state.choice === refreshed)
    }

    // MARK: resolveAgain (SDK.md §9.1)

    @Test func resolveAgainKeepsTheChoiceWhenTheAnswerHasItsIdUnderAnotherRef() async throws {
        let found = Box([Candidate<String>(id: "a", ref: "a.mkv", durationMs: 600_000)])
        let s = try await joinedSession(resolver: Resolver { _ in found.value })
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, movie.toJSON()))
        await s.clock.run()
        let chosen = try #require(s.state.choice)
        found.value = [Candidate(id: "a", ref: "a2.mkv", durationMs: 600_000)]
        s.party.resolveAgain()
        await s.clock.run()
        #expect(s.state.choice === chosen)
    }

    // MARK: App input is copied (SDK.md §16.6)

    @Test func changingTheAppsValuesAfterSetMediaChangesNothing() async throws {
        let resolved = Box<[String]>([])
        let s = try await joinedSession(host: true, resolver: recording(resolved))
        var media = movie
        media.ids = ["imdb": "tt0113277"]
        media.ext = ["com.example": 1]
        try s.party.setMedia(media, ref: "heat.mkv")
        media.ids["imdb"] = "tt9999999"
        media.ext?["com.example"] = 2
        let sent = try #require(try await s.socket.next("setMedia")["media"])
        var shared = movie
        shared.ids = ["imdb": "tt0113277"]
        shared.ext = ["com.example": 1]
        #expect(sent == shared.toJSON())
        // The relay shares what was sent: the party's own media.
        s.socket.reply(mediaRoom(rev: 2, epoch: 1, sent, host: true))
        await s.clock.run()
        expectOwnMedia(s.state, shared)
        #expect(resolved.value.isEmpty)
    }
}

extension Array {
    /// The only element; nil when there are none or several.
    var single: Element? { count == 1 ? first : nil }
}
