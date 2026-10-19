/// The constants of SDK.md §2. They are fixed by the spec, not settings.
enum Owp {
    static let alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
    static let codeLength = 6

    // Conventions (§2)
    /// Largest magnitude of an int (SPEC.md §2).
    static let maxInt: Int64 = 9_007_199_254_740_991

    /// Largest delay of one timer; a longer wait runs as several.
    static let maxTimerMs: Int64 = 2_147_483_647

    // Clock sync (§5)
    static let burstSpacingMs: Int64 = 500
    static let burstSize = 3
    static let pingIntervalMs: Int64 = 5000
    static let pingForgetMs: Int64 = 10000
    static let clockWindow = 8
    static let maxRttMs: Int64 = 1000
    static let clockStepSlackMs: Int64 = 100

    // Relay list and connections (§3, §6, §7, §14)
    static let publicListUrl = "https://openwatch.party/relays.json"
    /// To open a connection, for the relay's answer (§7.1), and for the `create` race (§6).
    static let connectTimeoutMs: Int64 = 10000
    static let httpTimeoutMs: Int64 = 5000
    static let httpMaxBodyBytes = 65536
    static let listRefreshMs: Int64 = 24 * 60 * 60 * 1000
    static let silenceTimeoutMs: Int64 = 15000
    static let backoffStartMs: Int64 = 500
    static let backoffMaxMs: Int64 = 8000
    static let resumeMs: Int64 = 300_000

    // Matching (§9)
    /// How long a resolver call may take before it counts as none.
    static let resolveTimeoutMs: Int64 = 30000
    static let matchToleranceMs: Int64 = 1500
    static let maxProbes = 4
    static let probeBudgetMs: Int64 = 60000

    // Status (§10)
    static let statusHeartbeatMs: Int64 = 15000
    static let statusSpacingMs: Int64 = 250

    // Following (§11, §12)
    static let tickMs: Int64 = 500
    static let deadbandMs: Int64 = 350
    static let nudgeExitMs: Int64 = 150
    static let nudgeFactor = 0.04
    static let seekMs: Int64 = 2000
    static let seekOnlyMs: Int64 = 1000
    static let heldSeekMs: Int64 = 500
    static let seekLeadMs: Int64 = 250
    static let seekCooldownMs: Int64 = 2000
    static let endMarginMs: Int64 = 1000
    static let rateEpsilon = 0.001
    static let commandHoldMs: Int64 = 2000

    // Protocol limits (SPEC.md §13.1)
    static let maxNameLength = 32
    static let maxClientLength = 64
    static let minRate = 0.5
    static let maxRate = 2.0
    static let maxMediaBytes = 4096
    static let maxExtBytes = 1024

    /// A descriptor's `durationMs` is 1 to this (7 days); so is every candidate duration that counts (§9.2).
    static let maxDurationMs: Int64 = 604_800_000

    /// An offset is at most this (7 days) either way (§13).
    static let maxOffsetMs: Int64 = 604_800_000

    /// The v1 `ids` namespaces (SPEC.md §10).
    static let namespaces = ["imdb", "tmdb", "tvdb", "plex", "anilist", "anidb", "kitsu", "mal"]

    /// The close status of a connection a `resume` elsewhere took over (SPEC.md Appendix B).
    static let closeReplaced = 4000

    /// Ints as SPEC.md §2 bounds them.
    static let intRange: ClosedRange<Int64> = -maxInt...maxInt
}
