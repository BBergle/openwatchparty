import Foundation
import Testing
@testable import OpenWatchParty

/// A player's refusal to start, compared by its reason.
struct Refusal: Error, Equatable {
    let reason: String
}

/// The player rules of SDK.md §10-§13 that `party.json` doesn't pin, in virtual time.
@MainActor
@Suite(.timeLimit(.minutes(1)))
struct PlayerTests {
    // MARK: Local commands and offset (SDK.md §12, §13)

    @Test func outOfRangeIntegersThrowBeforeAnyLocalEffect() async throws {
        let a = TestPlayer()
        let s = try await boundSession(a)
        let max: Int64 = (1 << 53) - 1
        expectPartyError(.invalidMessage) { try s.party.setOffset(604_800_001) }
        expectPartyError(.invalidMessage) { try s.party.setOffset(-604_800_001) }
        #expect(s.state.offsetMs == 0)
        try s.party.setOffset(-604_800_000)
        #expect(s.state.offsetMs == -604_800_000)
        expectPartyError(.invalidMessage) { try s.party.seek(to: max + 1) }
        expectPartyError(.invalidMessage) { try s.party.seek(to: max) } // max - offset is beyond an int
        expectPartyError(.invalidMessage) { try s.party.setMedia(movie, ref: "heat.mkv", positionMs: max + 1) }
        var long = movie
        long.durationMs = 604_800_001
        expectPartyError(.invalidMessage) { try s.party.setMedia(long, ref: "heat.mkv") }
        await s.clock.run()
        #expect(s.state.player == .ready, "still bound")
        a.positionMs = max // the adapter's position - offset is beyond an int
        expectPartyError(.invalidMessage) { try s.party.pause() }
        await s.clock.run()
        #expect(a.log == ["setRate 1.0"])
        let sent = s.socket.drain()
        #expect(!sent.contains { $0 == "seek" || $0 == "pause" || $0 == "setMedia" }, "\(sent)")
    }

    @Test func setRateRefusesNaN() async throws {
        let s = try await boundSession(TestPlayer())
        expectPartyError(.invalidMessage) { try s.party.setRate(.nan) }
    }

    // MARK: A player that refuses to start (SDK.md §10.2 rule 8)

    @Test func playBlockedCarriesTheErrorThePlayerThrew() async throws {
        let a = TestPlayer()
        let s = try await boundSession(a)
        let refusal = Refusal(reason: "autoplay wants a user gesture")
        a.refusal = refusal
        s.socket.reply(snapshot(rev: 3, phase: "playing", atMs: s.relayNow))
        await s.clock.run()
        #expect((s.events.single as? PlayBlockedEvent)?.error as? Refusal == refusal)
    }

    @Test func aPlayThatThrowsACancellationOfItsOwnRefusedToStart() async throws {
        let a = TestPlayer()
        let s = try await boundSession(a)
        a.refusal = CancellationError()
        s.socket.reply(snapshot(rev: 3, phase: "playing", atMs: s.relayNow))
        await s.clock.run()
        #expect(s.state.player == .loading)
        #expect(s.events.single is PlayBlockedEvent)
        await s.clock.advance(by: 600)
        #expect(a.log.filter { $0 == "play" }.count == 2, "the lane goes on: \(a.log)")
    }
}
