import Foundation
import Testing
@testable import OpenWatchParty

/// SDK.md §3.1 and §17: the compiled-in list is a copy of `relays.json` at the repository root.
@Suite struct PublicRelaysTests {
    @Test func theEmbeddedListEqualsTheRepositoryFile() throws {
        let canonical = try String(contentsOf: TestData.root.appending(path: "relays.json"), encoding: .utf8)
        #expect(publicRelaysJSON == canonical, "PublicRelays.swift differs from relays.json")
    }

    @Test func theEmbeddedListIsAValidPublicList() throws {
        let relays = try #require(parseRelayList(JSON.parse(publicRelaysJSON)))
        #expect(relays.map(\.id) == ["na", "eu", "asia"])
    }
}
