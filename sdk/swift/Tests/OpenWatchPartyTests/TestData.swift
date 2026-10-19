import Foundation
@testable import OpenWatchParty

/// Files of the repository the tests read: the vectors and `relays.json`.
enum TestData {
    /// The repository root, found from this file's path (`sdk/swift/Tests/OpenWatchPartyTests/`).
    static let root: URL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()

    static func vectors(_ table: String) throws -> String {
        try String(contentsOf: root.appending(path: "conformance/vectors/\(table).json"), encoding: .utf8)
    }
}
