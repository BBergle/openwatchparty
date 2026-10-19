package party.openwatch

/** The constants of SDK.md §2. They are fixed by the spec, not settings. */
internal object Owp {
    const val ALPHABET: String = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
    const val CODE_LENGTH: Int = 6

    // Conventions (§2)
    /** Largest magnitude of an int (SPEC.md §2). */
    const val MAX_INT: Long = 9007199254740991

    /** Largest delay of one timer; a longer wait runs as several. */
    const val MAX_TIMER_MS: Long = 2147483647

    // Clock sync (§5)
    const val BURST_SPACING_MS: Long = 500
    const val BURST_SIZE: Int = 3
    const val PING_INTERVAL_MS: Long = 5000
    const val PING_FORGET_MS: Long = 10000
    const val CLOCK_WINDOW: Int = 8
    const val MAX_RTT_MS: Long = 1000
    const val CLOCK_STEP_SLACK_MS: Long = 100

    // Relay list and connections (§3, §6, §7, §14)
    const val PUBLIC_LIST_URL: String = "https://openwatch.party/relays.json"
    /** To open a connection, for the relay's answer (§7.1), and for the relay choice of `create` (§6). */
    const val CONNECT_TIMEOUT_MS: Long = 10000
    const val HTTP_TIMEOUT_MS: Long = 5000
    const val HTTP_MAX_BODY_BYTES: Int = 65536
    const val LIST_REFRESH_MS: Long = 24 * 60 * 60 * 1000L
    const val SILENCE_TIMEOUT_MS: Long = 15000
    const val BACKOFF_START_MS: Long = 500
    const val BACKOFF_MAX_MS: Long = 8000
    const val RESUME_WINDOW_MS: Long = 300000

    // Matching (§9)
    const val RESOLVE_TIMEOUT_MS: Long = 30000
    const val MATCH_TOLERANCE_MS: Long = 1500
    const val MAX_PROBES: Int = 4
    const val PROBE_BUDGET_MS: Long = 60000

    // Status (§10)
    const val STATUS_HEARTBEAT_MS: Long = 15000
    const val STATUS_SPACING_MS: Long = 250

    // Following (§11, §12)
    const val TICK_MS: Long = 500
    const val DEADBAND_MS: Long = 350
    const val NUDGE_EXIT_MS: Long = 150
    const val NUDGE_FACTOR: Double = 0.04
    const val SEEK_MS: Long = 2000
    const val SEEK_ONLY_MS: Long = 1000
    const val HELD_SEEK_MS: Long = 500
    const val SEEK_LEAD_MS: Long = 250
    const val SEEK_COOLDOWN_MS: Long = 2000
    const val END_MARGIN_MS: Long = 1000
    const val RATE_EPSILON: Double = 0.001
    const val COMMAND_HOLD_MS: Long = 2000

    // Protocol limits (SPEC.md §13.1)
    const val MAX_NAME_LENGTH: Int = 32
    const val MAX_CLIENT_LENGTH: Int = 64
    const val MIN_RATE: Double = 0.5
    const val MAX_RATE: Double = 2.0
    const val MAX_MEDIA_BYTES: Int = 4096
    const val MAX_EXT_BYTES: Int = 1024

    /** A descriptor's `durationMs` is 1 to this (7 days); so is every candidate duration that counts (§9.2). */
    const val MAX_DURATION_MS: Long = 604800000

    /** An offset is at most this (7 days) either way (§13). */
    const val MAX_OFFSET_MS: Long = 604800000

    /** The v1 `ids` namespaces (SPEC.md §10). */
    val NAMESPACES: List<String> = listOf("imdb", "tmdb", "tvdb", "plex", "anilist", "anidb", "kitsu", "mal")
}
