// swift-tools-version: 6.0
// The Open Watch Party Swift SDK. The manifest sits at the repository root because SwiftPM resolves a package by its
// repository URL from the root; the sources are in sdk/swift.
import PackageDescription

let package = Package(
    name: "OpenWatchParty",
    // iOS 16, macOS 13, tvOS 16, watchOS 9: ContinuousClock and Task.sleep(for:), which give the SDK its monotonic
    // local time and its timers. visionOS 1 has both.
    platforms: [.iOS(.v16), .macOS(.v13), .tvOS(.v16), .watchOS(.v9), .visionOS(.v1)],
    products: [
        .library(name: "OpenWatchParty", targets: ["OpenWatchParty"]),
    ],
    targets: [
        .target(name: "OpenWatchParty", path: "sdk/swift/Sources/OpenWatchParty"),
        .testTarget(
            name: "OpenWatchPartyTests",
            dependencies: ["OpenWatchParty"],
            path: "sdk/swift/Tests/OpenWatchPartyTests"
        ),
    ],
    swiftLanguageModes: [.v6]
)
