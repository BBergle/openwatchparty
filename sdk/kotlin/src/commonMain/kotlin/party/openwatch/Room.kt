package party.openwatch

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/** The room's playback phase (SPEC.md §12.1). */
public enum class Phase(public val wire: String) {
    /** No media. */
    IDLE("idle"),
    PAUSED("paused"),

    /** Will play once the readiness barrier releases. */
    WAITING("waiting"),
    PLAYING("playing"),
    ;

    internal companion object {
        fun of(wire: String?): Phase? = entries.firstOrNull { it.wire == wire }
    }
}

/** Who may play, pause, seek and change the rate (SPEC.md §11.3). */
public enum class Control(public val wire: String) {
    HOST("host"),
    EVERYONE("everyone"),
    ;

    internal companion object {
        fun of(wire: String?): Control? = entries.firstOrNull { it.wire == wire }
    }
}

/** A participant's player as reported in `status` (SPEC.md §7.5, SDK.md §10.2). */
public enum class PlayerState(public val wire: String) {
    /** Not in the player. */
    IDLE("idle"),

    /** Opening, searching, seeking, or waiting on the user's mismatch choice. */
    LOADING("loading"),

    /** Able to play at once, close enough to the room's position that following it needs no seek. */
    READY("ready"),

    /** Was playing, now stalled. */
    BUFFERING("buffering"),
    ;

    internal companion object {
        fun of(wire: String?): PlayerState? = entries.firstOrNull { it.wire == wire }
    }
}

public enum class MatchState(public val wire: String) {
    /** Candidates are being tried, or the user hasn't decided about a mismatch yet. */
    SEARCHING("searching"),

    /** The chosen file's duration is within 1500 ms of the media's. */
    MATCHED("matched"),

    /** The user plays a file outside that margin, with an offset. */
    MISMATCHED("mismatched"),

    /** No file plays: none found, or the user declined. */
    UNAVAILABLE("unavailable"),
    ;

    internal companion object {
        fun of(wire: String?): MatchState? = entries.firstOrNull { it.wire == wire }
    }
}

/** A participant's file for the current media. [deltaMs] is its duration − the media's `durationMs`. */
public data class Match(val state: MatchState, val deltaMs: Long? = null) {
    internal fun toJson(): JsonObject = buildJsonObject {
        put("state", state.wire)
        deltaMs?.let { put("deltaMs", it) }
    }

    internal companion object {
        /** Null unless `state` is a v1 state and `deltaMs`, when present, an int. */
        fun fromJson(json: JsonElement?): Match? {
            val o = json.asObject() ?: return null
            val state = MatchState.of(o["state"].asString()) ?: return null
            val delta = o["deltaMs"]
            return Match(state, if (delta == null) null else delta.asInt() ?: return null)
        }
    }
}

/** The SDK's connection to the room (SDK.md §16.6). */
public enum class Connection {
    /** Not in a room and not trying to be. */
    IDLE,

    /** `create` or `join` is under way. */
    CONNECTING,

    /** A member with an open connection. */
    CONNECTED,

    /** The connection dropped; the SDK is resuming (SDK.md §14). */
    RECONNECTING,

    /** The party ended ([PartyState.endReason]). */
    ENDED,
}

/** Why the party ended (SDK.md §7.4). */
public enum class EndReason {
    /** The app called [WatchParty.leave]. */
    LEFT,

    /** The host ended the room, or the relay closed it for a reason this SDK doesn't know. */
    ENDED,

    /** The host removed this participant. */
    KICKED,

    /** The room reached its maximum age. */
    EXPIRED,

    /** A resume on another connection took this participant over. */
    REPLACED,

    /** Resuming became impossible; [PartyEvent.Ended.error] says why. */
    LOST,
}

/** One entry of the room's `participants`. */
public data class Participant(
    val peerId: String,
    val name: String,
    /** `<app>/<version>`. */
    val client: String,
    val host: Boolean,
    val connected: Boolean,
    val player: PlayerState,
    val match: Match?,
    val offsetMs: Long,
)

/** The room snapshot (SPEC.md §9). */
public data class Room(
    val rev: Long,
    val code: String,
    val epoch: Long,
    val phase: Phase,
    val positionMs: Long,
    /** Relay time; in [Phase.PLAYING], the moment playback runs from [positionMs]. */
    val atMs: Long,
    val rate: Double,
    val control: Control,
    val locked: Boolean,
    /** The peer IDs the barrier waits for. */
    val waitingOn: List<String>,
    /** Every participant, connected or not, in join order. */
    val participants: List<Participant>,
) {
    internal companion object {
        private val PEER_ID = Regex("^[A-Za-z0-9_-]{1,16}$")

        /**
         * The snapshot of a `room` or of `joined.room`; null when it is malformed (SDK.md §8, `snapshot.schema.json`):
         * a member missing or of the wrong type, an int out of range (`positionMs` above 604800000, an `offsetMs`
         * beyond 604800000 either way), `rate` outside 0.5–2.0, an unknown value of a closed enum, a bad peer ID,
         * `name` or `client`, other than exactly one host, a repeated peer ID in `waitingOn` or a non-empty one outside
         * `waiting`, or an `idle` room away from position 0. Unknown members are ignored.
         */
        fun fromJson(json: JsonElement?): Room? {
            val o = json.asObject() ?: return null
            val phase = Phase.of(o["phase"].asString()) ?: return null
            val positionMs = o["positionMs"].asInt()?.takeIf { it in 0..Owp.MAX_DURATION_MS } ?: return null
            if (phase == Phase.IDLE && positionMs != 0L) return null
            val waitingOn = o["waitingOn"].asArray()?.map { id -> id.asString()?.takeIf { isPeerId(it) } ?: return null } ?: return null
            if (waitingOn.toSet().size != waitingOn.size || (phase != Phase.WAITING && waitingOn.isNotEmpty())) return null
            val participants = o["participants"].asArray()?.map { participant(it) ?: return null } ?: return null
            if (participants.count { it.host } != 1) return null
            return Room(
                rev = o["rev"].asInt()?.takeIf { it >= 1 } ?: return null,
                code = o["code"].asString()?.takeIf { isRoomCode(it) } ?: return null,
                epoch = o["epoch"].asInt()?.takeIf { it >= 0 } ?: return null,
                phase = phase,
                positionMs = positionMs,
                atMs = o["atMs"].asInt()?.takeIf { it >= 0 } ?: return null,
                rate = o["rate"].asNumber()?.takeIf { it >= Owp.MIN_RATE && it <= Owp.MAX_RATE } ?: return null,
                control = Control.of(o["control"].asString()) ?: return null,
                locked = o["locked"].asBoolean() ?: return null,
                waitingOn = waitingOn,
                participants = participants,
            )
        }

        private fun participant(json: JsonElement): Participant? {
            val o = json.asObject() ?: return null
            val match = when (val m = o["match"] ?: return null) {
                is JsonNull -> null
                else -> Match.fromJson(m) ?: return null
            }
            return Participant(
                peerId = o["peerId"].asString()?.takeIf { isPeerId(it) } ?: return null,
                name = o["name"].asString()?.takeIf { nameProblem(it) == null } ?: return null,
                client = o["client"].asString()?.takeIf { clientProblem(it) == null } ?: return null,
                host = o["host"].asBoolean() ?: return null,
                connected = o["connected"].asBoolean() ?: return null,
                player = PlayerState.of(o["player"].asString()) ?: return null,
                match = match,
                offsetMs = o["offsetMs"].asInt()?.takeIf { it in -Owp.MAX_OFFSET_MS..Owp.MAX_OFFSET_MS } ?: return null,
            )
        }

        /** A peer ID as SPEC.md §11.1 shapes it. */
        fun isPeerId(s: String): Boolean = PEER_ID.matches(s)

        /** A room code as the relay sends it: 6 alphabet symbols. */
        fun isRoomCode(s: String): Boolean = s.length == Owp.CODE_LENGTH && s.all { Owp.ALPHABET.indexOf(it) >= 0 }
    }
}
