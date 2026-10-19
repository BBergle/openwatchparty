package party.openwatch

/**
 * The app's player, read and commanded by the SDK (SDK.md §16.4). The SDK calls it on the party's scope only, one
 * command at a time per adapter. It never infers the user's presses from these values: the app sends them through the
 * party. New members come with defaults, so an implementation keeps compiling.
 */
public interface PlayerAdapter {
    /** The current position in the app's own file. */
    public val positionMs: Long

    /** The file's duration; null when unknown. Without it the SDK doesn't protect the end of a shorter file. */
    public val durationMs: Long?

    public val paused: Boolean

    /** Stalled while trying to play. */
    public val buffering: Boolean

    /** The file is open and shows a frame. May stay false until the app's own start-up work is done. */
    public val firstFrameShown: Boolean

    /** Whether [setRate] works now; may change (false while audio passthrough is on, say). */
    public val canSetRate: Boolean get() = false

    /**
     * How far from the requested position a seek may land: a player that seeks to keyframes declares its keyframe
     * distance, and one that can't seek at all a very large value (the SDK then follows by rate only). Read on every
     * step; a negative value counts as 0.
     */
    public val seekAccuracyMs: Long get() = 0

    /**
     * Starts playing and returns once the player plays. Throws when the player refuses to start: while it then stays
     * paused, the party reports `loading` and emits [PartyEvent.PlayBlocked] (SDK.md §10.2).
     */
    public suspend fun play()

    public suspend fun pause()

    /** Returns once the seek has landed. */
    public suspend fun seek(positionMs: Long)

    public suspend fun setRate(rate: Double) {}
}

/** The app's search for the room's media (SDK.md §9.2). */
public fun interface Resolver<T> {
    /**
     * The files that are this title, in the app's order of preference. Cancelled when the room's media changes. A
     * resolver that throws, a cancellation of its own such as its own timeout included, or takes longer than the
     * party's `resolveTimeoutMs`, returned none.
     */
    public suspend fun resolve(media: Media): List<Candidate<T>>
}

/** One file the user could play (SDK.md §9.2). */
public class Candidate<T>(
    /** The app's key, unique among the candidates. */
    public val id: String,
    /** The app's own value, handed back with the choice. */
    public val ref: T,
    /** When the app knows it. Counts only from 1 to 604800000 (7 days); any other value is no duration. */
    public val durationMs: Long? = null,
    public val sizeBytes: Long? = null,
    public val oshash: String? = null,
    public val name: String? = null,
    /**
     * Opens the file, learns its duration and closes it again; null when the file doesn't open. Called only without a
     * [durationMs] that counts; an answer outside 1 to 604800000 is a failed probe, and so is a throw, a cancellation
     * of its own such as its own timeout included. The SDK cancels a probe it no longer needs; the probe then releases
     * what it opened.
     */
    public val probe: (suspend () -> Long?)? = null,
) {
    override fun toString(): String = "Candidate($id)"
}

/** A candidate of the matching walk and its duration difference once known (SDK.md §9.5). */
public data class Ranked<T>(val candidate: Candidate<T>, val deltaMs: Long?)

/** The closest file while the user decides whether to play it (SDK.md §9.6). */
public data class Mismatch<T>(val candidate: Candidate<T>, val deltaMs: Long)

/** Everything the UI shows, as one value (SDK.md §16.6). It changes on events, never per tick. */
public data class PartyState<T>(
    val connection: Connection = Connection.IDLE,
    /** Own peer ID while in a room. */
    val peerId: String? = null,
    /** The last accepted snapshot. */
    val room: Room? = null,
    /** The current descriptor. */
    val media: Media? = null,
    /** As reported in `status`. */
    val match: Match? = null,
    /**
     * The file to open for the current media. For the party's own media (SDK.md §9.1), id `""` with the `ref` passed
     * to `setMedia` and the descriptor's duration.
     */
    val choice: Candidate<T>? = null,
    /** The file offered while the user decides. */
    val mismatch: Mismatch<T>? = null,
    /** The walk's ranked candidates. */
    val candidates: List<Ranked<T>> = emptyList(),
    /** Local position = room position + [offsetMs] (SDK.md §13). */
    val offsetMs: Long = 0,
    /** As reported in `status`. */
    val player: PlayerState = PlayerState.IDLE,
    val endReason: EndReason? = null,
) {
    /** Own entry of [Room.participants]. */
    val me: Participant? get() = room?.participants?.firstOrNull { it.peerId == peerId }

    val isHost: Boolean get() = me?.host == true

    /** Host, or control is everyone. */
    val canControl: Boolean get() = isHost || (me != null && room?.control == Control.EVERYONE)
}

/**
 * What happened in the room (SDK.md §15). The rest is [PartyState]. Not sealed: SDK 1.x may add events, so handle them
 * with an `else` branch that ignores the ones the app doesn't know.
 */
public interface PartyEvent {
    /**
     * An accepted `room` carried the action that produced it: [kind] is `play`, `pause`, `seek`, `rate`, `media`,
     * `stall`, `ended`, `host` or newer.
     */
    public data class Action(
        val kind: String,
        /** The actor; null for the relay. */
        val byPeerId: String?,
        /** The actor's entry; null when the actor has left or is the relay. */
        val by: Participant?,
        val byMe: Boolean,
        val room: Room,
    ) : PartyEvent

    public data class ParticipantJoined(val participant: Participant) : PartyEvent

    public data class ParticipantLeft(val participant: Participant) : PartyEvent

    /** The relay refused a command. */
    public data class Error(val error: PartyException) : PartyEvent

    /** The resolver threw or took longer than 30 s; matching went on with no candidates. */
    public data class ResolveFailed(val error: Throwable) : PartyEvent

    /**
     * The player refused to start: its `play` threw [error] and it stayed paused (SDK.md §10.2). Ask the user to press
     * play on the player itself. Emitted again only after the player played, the room stopped playing or the adapter
     * unbound.
     */
    public data class PlayBlocked(val error: Throwable) : PartyEvent

    /** The party ended; [error] is the cause for [EndReason.LOST]. */
    public data class Ended(val reason: EndReason, val error: PartyException? = null) : PartyEvent
}

/**
 * A refusal or failure. [code] is the relay's error code (SPEC.md Appendix A, an open set) or the SDK's own
 * `invalid_code`, `unreachable` and `cancelled`; [re] is the refused message's type. [message] is for logs only: when
 * the error came from the relay it is the relay's own text. Apps show users their own text for [code], and a generic
 * one for codes they don't know (SDK.md §15).
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
