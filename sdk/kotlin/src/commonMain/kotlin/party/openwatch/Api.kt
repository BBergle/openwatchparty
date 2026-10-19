package party.openwatch

/**
 * The app's player, which the SDK reads and commands (SDK.md §16.4).
 *
 * The SDK calls the adapter only on the party's scope, one command at a time per adapter. It never infers the user's
 * presses from these values, because the app sends those through the party.
 *
 * New members come with defaults, so an implementation keeps compiling.
 */
public interface PlayerAdapter {
    /** The current position in the app's own file. */
    public val positionMs: Long

    /**
     * The file's duration, or null when it is unknown.
     *
     * Without a duration, the SDK doesn't protect the end of a shorter file.
     */
    public val durationMs: Long?

    public val paused: Boolean

    /** Whether the player is stalled while trying to play. */
    public val buffering: Boolean

    /**
     * Whether the file is open and shows a frame.
     *
     * It may stay false until the app's own start-up work is done.
     */
    public val firstFrameShown: Boolean

    /**
     * Whether [setRate] works now.
     *
     * The value may change, for example to false while audio passthrough is on.
     */
    public val canSetRate: Boolean get() = false

    /**
     * How far from the requested position a seek may land.
     *
     * A player that seeks to keyframes declares its keyframe distance. A player that can't seek at all declares a very
     * large value, and the SDK then follows by rate only.
     *
     * The SDK reads this value on every step. A negative value counts as 0.
     */
    public val seekAccuracyMs: Long get() = 0

    /**
     * Starts playing, and returns once the player plays.
     *
     * Throws when the player refuses to start. While the player then stays paused, the party reports `loading` and
     * emits [PartyEvent.PlayBlocked] (SDK.md §10.2).
     */
    public suspend fun play()

    public suspend fun pause()

    /** Seeks to [positionMs], and returns once the seek has landed. */
    public suspend fun seek(positionMs: Long)

    public suspend fun setRate(rate: Double) {}
}

/** The app's search for the room's media (SDK.md §9.2). */
public fun interface Resolver<T> {
    /**
     * Returns the files that are this title, in the app's order of preference.
     *
     * The SDK cancels the call when the room's media changes. The call counts as returning no candidates when either
     * of these holds:
     *
     * - It throws, including a cancellation of its own, such as its own timeout.
     * - It takes longer than the resolve timeout of 30 seconds (SDK.md §2).
     */
    public suspend fun resolve(media: Media): List<Candidate<T>>
}

/** One file the user could play (SDK.md §9.2). */
public class Candidate<T>(
    /** The app's key, unique among the candidates. */
    public val id: String,
    /** The app's own value, handed back with the choice. */
    public val ref: T,
    /**
     * The file's duration, when the app knows it.
     *
     * Only values from 1 to 604800000 (7 days) count. The SDK treats any other value as no duration.
     */
    public val durationMs: Long? = null,
    public val sizeBytes: Long? = null,
    public val oshash: String? = null,
    public val name: String? = null,
    /**
     * Opens the file, learns its duration and closes it again.
     *
     * It returns null when the file doesn't open. The SDK calls it only when [durationMs] doesn't count. The probe
     * fails when any of these holds:
     *
     * - It returns null or a value outside 1 to 604800000.
     * - It throws, including a cancellation of its own, such as its own timeout.
     *
     * The SDK cancels a probe it no longer needs. The probe then releases what it opened.
     */
    public val probe: (suspend () -> Long?)? = null,
) {
    override fun toString(): String = "Candidate($id)"
}

/** A candidate of the matching walk, with its duration difference once that is known (SDK.md §9.5). */
public data class Ranked<T>(val candidate: Candidate<T>, val deltaMs: Long?)

/** The closest file while the user decides whether to play it (SDK.md §9.6). */
public data class Mismatch<T>(val candidate: Candidate<T>, val deltaMs: Long)

/**
 * Everything the UI shows, as one value (SDK.md §16.6).
 *
 * It changes on events, never per tick.
 */
public data class PartyState<T>(
    val connection: Connection = Connection.IDLE,
    /** The party's own peer ID while it is in a room. */
    val peerId: String? = null,
    /** The last accepted snapshot. */
    val room: Room? = null,
    /** The current descriptor. */
    val media: Media? = null,
    /** The match, as the party reports it in `status`. */
    val match: Match? = null,
    /**
     * The file to open for the current media.
     *
     * For the party's own media (SDK.md §9.1), it has the id `""`, the `ref` passed to `setMedia` and the descriptor's
     * duration.
     */
    val choice: Candidate<T>? = null,
    /** The file offered while the user decides. */
    val mismatch: Mismatch<T>? = null,
    /** The walk's ranked candidates. */
    val candidates: List<Ranked<T>> = emptyList(),
    /**
     * The offset of the user's file from the room (SDK.md §13).
     *
     * The local position is the room position plus [offsetMs].
     */
    val offsetMs: Long = 0,
    /** The player state, as the party reports it in `status`. */
    val player: PlayerState = PlayerState.IDLE,
    val endReason: EndReason? = null,
) {
    /** The party's own entry in [Room.participants]. */
    val me: Participant? get() = room?.participants?.firstOrNull { it.peerId == peerId }

    val isHost: Boolean get() = me?.host == true

    /**
     * Whether the party may control playback.
     *
     * It may when it is the host, or when the room's control is [Control.EVERYONE].
     */
    val canControl: Boolean get() = isHost || (me != null && room?.control == Control.EVERYONE)
}

/**
 * Something that happened in the room (SDK.md §15).
 *
 * Everything else the UI shows is in [PartyState].
 *
 * The interface isn't sealed, because SDK 1.x may add events. Handle events with an `else` branch that ignores the ones
 * the app doesn't know.
 */
public interface PartyEvent {
    /**
     * An accepted `room` carried the action that produced it.
     *
     * [kind] is `play`, `pause`, `seek`, `rate`, `media`, `stall`, `ended`, `host`, or a newer kind.
     */
    public data class Action(
        val kind: String,
        /** The actor's peer ID, or null for the relay. */
        val byPeerId: String?,
        /** The actor's entry, or null when the actor has left or is the relay. */
        val by: Participant?,
        val byMe: Boolean,
        val room: Room,
    ) : PartyEvent

    public data class ParticipantJoined(val participant: Participant) : PartyEvent

    public data class ParticipantLeft(val participant: Participant) : PartyEvent

    /** The relay refused a command. */
    public data class Error(val error: PartyException) : PartyEvent

    /** The resolver threw or took longer than 30 seconds, and matching went on with no candidates. */
    public data class ResolveFailed(val error: Throwable) : PartyEvent

    /**
     * The player refused to start (SDK.md §10.2).
     *
     * Its `play` threw [error], and the player stayed paused. Ask the user to press play on the player itself.
     *
     * The party emits this event again only after the player has played, the room has stopped playing or the adapter
     * was unbound.
     */
    public data class PlayBlocked(val error: Throwable) : PartyEvent

    /**
     * The party ended.
     *
     * For [EndReason.LOST], [error] is the cause.
     */
    public data class Ended(val reason: EndReason, val error: PartyException? = null) : PartyEvent
}

/**
 * A refusal or failure (SDK.md §15).
 *
 * [code] is either the relay's error code or one of the SDK's own: `invalid_code`, `unreachable` and `cancelled`. The
 * relay's codes are an open set (SPEC.md Appendix A). [re] is the type of the refused message.
 *
 * [message] is for logs only. When the error came from the relay, it is the relay's own text. Apps show users their
 * own text for [code], and a generic one for codes they don't know.
 */
public class PartyException(
    public val code: String,
    message: String = code,
    public val re: String? = null,
    cause: Throwable? = null,
) : Exception(message, cause) {
    public companion object {
        public const val INVALID_CODE: String = "invalid_code"
        public const val UNREACHABLE: String = "unreachable"
        public const val CANCELLED: String = "cancelled"
        public const val NOT_IN_ROOM: String = "not_in_room"
        public const val ALREADY_IN_ROOM: String = "already_in_room"
        public const val FORBIDDEN: String = "forbidden"
        public const val INVALID_MESSAGE: String = "invalid_message"
        public const val ROOM_NOT_FOUND: String = "room_not_found"
        public const val RELAY_FULL: String = "relay_full"
        public const val RESUME_FAILED: String = "resume_failed"
        public const val CLIENT_BLOCKED: String = "client_blocked"
    }
}
