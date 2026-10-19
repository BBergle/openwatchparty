import Foundation
import Testing
@testable import OpenWatchParty

/// Every table of conformance/vectors (SDK.md §17), run against the SDK's own functions.
@MainActor
@Suite struct VectorTests {
    struct Case {
        let name: String
        let input: JSON
        let expect: JSON
    }

    private func cases(_ table: String) throws -> [Case] {
        let doc = try #require(JSON.parse(try TestData.vectors(table)), "\(table).json is no JSON")
        #expect(doc["table"]?.string == table)
        let cases = try #require(doc["cases"]?.array).map { c in
            Case(name: c["name"]?.string ?? "?", input: c["input"] ?? .null, expect: c["expect"] ?? .null)
        }
        #expect(!cases.isEmpty, "\(table) has no cases")
        return cases
    }

    /// Runs `check` on every case of `table`, naming the case in each failure.
    private func forEach(_ table: String, _ check: (Case, Comment) throws -> Void) throws {
        for c in try cases(table) {
            do {
                try check(c, "\(table): \(c.name)")
            } catch {
                Issue.record("\(table): \(c.name): \(error)")
            }
        }
    }

    @Test func timeline() throws {
        try forEach("timeline") { c, label in
            let room = c.input["room"]
            let phase = try #require(room?["phase"]?.string.flatMap(Phase.init(rawValue:)))
            let atMs = try #require(room?["atMs"]?.int)
            let now = try #require(c.input["serverNowMs"]?.int)
            let position = roomPosition(
                phase: phase, positionMs: try #require(room?["positionMs"]?.int), atMs: atMs,
                rate: try #require(room?["rate"]?.number), durationMs: try #require(c.input["durationMs"]?.int), t: now
            )
            #expect(position == c.expect["positionMs"]?.int, label)
            #expect(roomRunning(phase: phase, atMs: atMs, t: now) == c.expect["running"]?.bool, label)
        }
    }

    @Test func clockSync() throws {
        try forEach("clock-sync") { c, label in
            var sync = ClockSync()
            var results: [String] = []
            for e in try #require(c.input["events"]?.array) {
                if let id = e["ping"]?.int {
                    sync.ping(id: id, atMs: try #require(e["atMs"]?.int))
                } else {
                    let result = sync.pong(
                        id: try #require(e["pong"]?.int), serverTime: try #require(e["serverTime"]?.int),
                        atMs: try #require(e["atMs"]?.int)
                    )
                    results.append("\(result.rawValue) \(sync.best.map { "\($0.offsetMs) \($0.rttMs)" } ?? "nil nil")")
                }
            }
            let expected = try #require(c.expect["pongs"]?.array).map { p in
                "\(p["result"]?.string ?? "?") \(p["offsetMs"]?.int.map(String.init) ?? "nil") \(p["rttMs"]?.int.map(String.init) ?? "nil")"
            }
            #expect(results == expected, label)
        }
    }

    @Test func reconcile() throws {
        try forEach("reconcile") { c, label in
            let room = try #require(c.input["room"])
            let player = try #require(c.input["player"])
            let out = OpenWatchParty.reconcile(StepInput(
                phase: try #require(room["phase"]?.string.flatMap(Phase.init(rawValue:))),
                positionMs: try #require(room["positionMs"]?.int),
                atMs: try #require(room["atMs"]?.int),
                rate: try #require(room["rate"]?.number),
                durationMs: try #require(c.input["durationMs"]?.int),
                nowMs: try #require(c.input["serverNowMs"]?.int),
                offsetMs: try #require(c.input["offsetMs"]?.int),
                playerPositionMs: try #require(player["positionMs"]?.int),
                paused: try bool(player["paused"]),
                buffering: try bool(player["buffering"]),
                playerDurationMs: player["durationMs"]?.int,
                canSetRate: try bool(player["canSetRate"]),
                seekAccuracyMs: player["seekAccuracyMs"]?.int ?? 0,
                appliedRate: try #require(player["rate"]?.number),
                nudging: try bool(c.input["nudging"]),
                sinceSeekMs: c.input["sinceSeekMs"]?.int
            ))
            let actions = out.actions.map { action -> String in
                switch action {
                case .pause: "pause"
                case .setRate(let rate): "setRate \(micro(rate))"
                case .seek(let positionMs): "seek \(positionMs)"
                case .play: "play"
                }
            }
            let expected = try #require(c.expect["actions"]?.array).map { a -> String in
                switch a["do"]?.string {
                case "setRate": "setRate \(micro(a["rate"]?.number ?? .nan))"
                case "seek": "seek \(a["positionMs"]?.int.map(String.init) ?? "?")"
                case let kind: kind ?? "?"
                }
            }
            #expect(actions == expected, label)
            #expect(out.nudging == c.expect["nudging"]?.bool, label)
            #expect(out.playInMs == c.expect["playInMs"]?.int, label)
        }
    }

    /// Rates compared in millionths, as the README asks.
    private func micro(_ rate: Double) -> Int64 { Int64((rate * 1e6).rounded()) }

    private func bool(_ json: JSON?) throws -> Bool {
        guard let b = json?.bool else { throw TestFailure("not a boolean: \(String(describing: json))") }
        return b
    }

    @Test func matching() async throws {
        for c in try cases("matching") {
            let label: Comment = "matching: \(c.name)"
            let clock = VirtualClock()
            let media = try parseMedia(JSONValue(try #require(c.input["media"])))
            let candidates = try #require(c.input["candidates"]?.array).map { j in
                let id = j["id"]?.string ?? "?"
                var probe: (@MainActor @Sendable () async throws -> Int64?)?
                if let p = j["probe"], let takes = p["takesMs"]?.int {
                    let duration = p["durationMs"]?.int
                    probe = {
                        try await clock.sleep(takes)
                        return duration
                    }
                }
                return Candidate(
                    id: id, ref: id, durationMs: j["durationMs"]?.int, sizeBytes: j["sizeBytes"]?.int,
                    oshash: j["oshash"]?.string, name: j["name"]?.string, probe: probe
                )
            }
            var published: [Ranked<String>] = []
            let finished = Latch<Result<WalkResult<String>, any Error>>()
            Task { @MainActor in
                do {
                    finished.open(.success(try await walk(media: media, candidates: candidates, clock: clock) { published = $0 }))
                } catch {
                    finished.open(.failure(error))
                }
            }
            await clock.advance(by: 2 * Owp.probeBudgetMs)
            #expect(finished.isOpen, "\(label): the walk ended")
            let result = try await finished.wait().get()
            #expect(result.ranked.map(\.id) == c.expect["order"]?.array?.compactMap(\.string), label)
            let expectedCandidates = try #require(c.expect["candidates"]?.array).map { "\($0["id"]?.string ?? "?") \($0["deltaMs"]?.int.map(String.init) ?? "nil")" }
            #expect(published.map { "\($0.candidate.id) \($0.deltaMs.map(String.init) ?? "nil")" } == expectedCandidates, label)
            #expect(result.probed == c.expect["probed"]?.array?.compactMap(\.string), label)
            let outcome: (String, String?, Int64?)
            switch result.outcome {
            case .matched(let candidate, let deltaMs): outcome = ("matched", candidate.id, deltaMs)
            case .mismatch(let candidate, let deltaMs): outcome = ("mismatch", candidate.id, deltaMs)
            case .unavailable: outcome = ("unavailable", nil, nil)
            }
            #expect(outcome.0 == c.expect["result"]?.string, label)
            #expect(outcome.1 == c.expect["choice"]?.string, label)
            #expect(outcome.2 == c.expect["deltaMs"]?.int, label)
        }
    }

    @Test func sameTitle() throws {
        try forEach("same-title") { c, label in
            let a = try parseMedia(JSONValue(try #require(c.input["a"])))
            let b = try parseMedia(JSONValue(try #require(c.input["b"])))
            let same = c.expect["same"]?.bool
            #expect(OpenWatchParty.sameTitle(a, b) == same, label)
            #expect(OpenWatchParty.sameTitle(b, a) == same, label)
        }
    }

    @Test func codeRouting() throws {
        try forEach("code-routing") { c, label in
            let relays = try #require(c.input["relays"]?.array).map { r in
                let id = r["id"]?.string ?? "?"
                return Relay(id: id, name: id, url: "wss://\(id).example/v1/ws", prefixes: r["prefixes"]?.string ?? "")
            }
            let typed = try #require(c.input["code"]?.text)
            let code = typed.unpaired ? nil : normalizeCode(typed.value)
            if let error = c.expect["error"]?.string {
                #expect(error == PartyError.Code.invalidCode.rawValue, label)
                #expect(code == nil, label)
                if !typed.unpaired {
                    do {
                        _ = try formatCode(typed.value)
                        Issue.record("\(label): formatCode accepted it")
                    } catch let e as PartyError {
                        #expect(e.code == .invalidCode, label)
                    }
                }
            } else {
                let expected = try #require(c.expect["code"]?.string)
                #expect(code == expected, label)
                #expect(route(expected, relays)?.id == c.expect["relay"]?.string, label)
                #expect(try formatCode(typed.value) == "\(expected.prefix(3))-\(expected.suffix(3))", label)
            }
        }
    }

    @Test func relayList() throws {
        try forEach("relay-list") { c, label in
            let relays = parseRelayList(c.input["list"])
            if let error = c.expect["error"]?.string {
                #expect(error == "invalid_list", label)
                #expect(relays == nil, label)
            } else {
                let expected = try #require(c.expect["relays"]?.array).map { r in
                    ["id", "name", "url", "prefixes"].map { r[$0]?.string ?? "?" }
                }
                #expect(relays?.map { [$0.id, $0.name, $0.url, $0.prefixes] } == expected, label)
            }
        }
    }

    /// The constructor refuses what names.json marks invalid with `invalid_message` (SDK.md §7.1). A Swift `String`
    /// can't hold an unpaired surrogate, so no app can pass one; for those cases the check the SDK applies to relay
    /// input, which sees them, must refuse.
    @Test func names() throws {
        let relays = notFoundRelays(VirtualClock())
        try forEach("names") { c, label in
            let value = try #require(c.input["value"]?.text)
            let field = c.input["field"]?.string
            let valid = try bool(c.expect["valid"])
            if value.unpaired {
                let problem = field == "name"
                    ? nameProblem(value.value, unpaired: true) : clientProblem(value.value, unpaired: true)
                #expect(!valid && problem != nil, label)
                return
            }
            let (name, client) = field == "name" ? (value.value, "Test/1.0") : ("Ben", value.value)
            do {
                _ = try WatchParty<String>(relays: relays, client: client, name: name)
                #expect(valid, "\(label): the constructor accepted it")
            } catch let error as PartyError {
                #expect(!valid && error.code == .invalidMessage, "\(label): the constructor threw \(error)")
            }
        }
    }

    /// The check of a `room` message's snapshot and `action` (SDK.md §8): a `room` that fails it is dropped.
    @Test func roomCheck() throws {
        try forEach("room-check") { c, label in
            #expect((c.input["room"].flatMap(In.room) != nil) == c.expect["valid"]?.bool, label)
        }
    }

    /// The descriptor checks of SDK.md §9.7: on receive the SDK's own reading, which also sees unpaired surrogates, and
    /// `parseMedia` for the rest, which throws `invalid_message`; on send `checkMedia` on the `Media` the app built,
    /// sizes included. A send case with a value no `Media` can hold (an unpaired surrogate, a number beyond an int) has
    /// no `Media` to build: it must be invalid.
    @Test func mediaCheck() throws {
        try forEach("media-check") { c, label in
            let json = try #require(c.input["media"])
            let valid: Bool
            switch c.input["on"]?.string {
            case "receive":
                valid = json.object.flatMap(receivedMedia) != nil
                if !json.holdsUnpaired {
                    do {
                        _ = try parseMedia(JSONValue(json))
                        #expect(valid, "\(label): parseMedia accepted it")
                    } catch let error as PartyError {
                        #expect(!valid && error.code == .invalidMessage, "\(label): parseMedia threw \(error)")
                    }
                }
            case "send":
                valid = json.object.flatMap(readMedia).map { !$0.unpaired && checkMedia($0.media) == nil } ?? false
            case let on:
                throw TestFailure("unknown direction \(on ?? "nil")")
            }
            #expect(valid == c.expect["valid"]?.bool, label)
        }
    }

    /// A new table in conformance/vectors fails here until a runner reads it; `party` runs in `PartyTraceTests`.
    @Test func everyTableRuns() throws {
        let run: Set = [
            "timeline", "clock-sync", "reconcile", "matching", "same-title", "code-routing", "relay-list",
            "names", "room-check", "media-check", "party",
        ]
        let files = try FileManager.default.contentsOfDirectory(
            at: TestData.root.appending(path: "conformance/vectors"), includingPropertiesForKeys: nil
        )
        let tables = Set(files.filter { $0.pathExtension == "json" }.map { $0.deletingPathExtension().lastPathComponent })
        #expect(tables == run)
    }
}
