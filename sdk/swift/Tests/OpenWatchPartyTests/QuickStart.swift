// The quick start of the Swift SDK, compiled with the tests against the public API only (no `@testable`), so the
// sample an app copies keeps compiling. Nothing here connects to a relay.
import Foundation
import OpenWatchParty
import Testing

// --- quick start ---
/// The app's player as the party sees it: read on every tick, commanded by the party.
@MainActor
final class VideoPlayer: PlayerAdapter {
    var positionMs: Int64 = 0
    var durationMs: Int64? = 10_200_000
    var paused = true
    var buffering = false
    var firstFrameShown = true

    func play() async throws { paused = false } // throw when the player refuses to start
    func pause() async throws { paused = true }
    func seek(to positionMs: Int64) async throws { self.positionMs = positionMs }
}

@MainActor
func watchTogether(code: String?) async throws {
    let heat = URL(fileURLWithPath: "/Movies/Heat.mkv")
    let party = try WatchParty<URL>(
        relays: .public(),
        client: "Example/1.0",
        name: "Anna",
        resolver: Resolver { media in
            // The files in the app's library that are this title, best first.
            media.title == "Heat" ? [Candidate(id: "heat", ref: heat, durationMs: 10_200_000)] : []
        }
    )
    // Subscribe before create or join: a stream gets what is published from then on, nothing earlier.
    let states = party.states()
    let events = party.events()
    Task {
        for await event in events {
            if event is PlayBlockedEvent { print("Tap play to start the video") }
        }
    }
    if let code {
        try await party.join(code) // "k7m-q2x" works too
    } else {
        let code = try await party.create()
        print("Room code: \(try formatCode(code))")
        let media = Media(kind: .movie, title: "Heat", durationMs: 10_200_000, year: 1995, ids: ["imdb": "tt0113277"])
        try party.setMedia(media, ref: heat)
    }
    let player = VideoPlayer()
    var opened: URL?
    for await state in states {
        let file = state.choice?.ref
        if let file, file != opened {
            opened = file // open `file` in the player, then:
            party.attachPlayer(player, ref: file)
        }
        if state.connection == .ended { break }
    }
    await party.close().value
}
// --- end of quick start ---

@MainActor
@Suite
struct QuickStartTests {
    @Test func theSamplePlayerTakesTheDefaultsOfTheOptionalMembers() async throws {
        let player = VideoPlayer()
        #expect(!player.canSetRate)
        #expect(player.seekAccuracyMs == 0)
        try await player.setRate(1.5)
        #expect(try formatCode("k7m q2x") == "K7M-Q2X")
    }
}
