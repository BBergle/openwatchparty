/// The room's playback phase (SPEC.md §12.1).
public enum Phase: String, Hashable, Sendable, CaseIterable {
    /// The room has no media.
    case idle
    case paused
    /// The room plays once the readiness barrier releases.
    case waiting
    case playing
}

/// Who may play, pause, seek and change the rate (SPEC.md §11.3).
public enum Control: String, Hashable, Sendable, CaseIterable {
    case host
    case everyone
}

/// A participant's player, as reported in `status` (SPEC.md §7.5, SDK.md §10.2).
public enum PlayerState: String, Hashable, Sendable, CaseIterable {
    /// The user is not in the player.
    case idle
    /// The player is opening, searching or seeking, or waits for the user's choice about a mismatch.
    case loading
    /// The player can play at once, and is close enough to the room's position that following it needs no seek.
    case ready
    /// The player was playing and is now stalled.
    case buffering
}

public enum MatchState: String, Hashable, Sendable, CaseIterable {
    /// Candidates are being tried, or the user hasn't decided about a mismatch yet.
    case searching
    /// The chosen file's duration is within 1500 ms of the media's.
    case matched
    /// The user plays a file outside that margin, with an offset.
    case mismatched
    /// No file plays, because none was found or the user declined.
    case unavailable
}

/// A participant's file for the current media.
///
/// `deltaMs` is the file's duration minus the media's `durationMs`.
public struct Match: Hashable, Sendable {
    public let state: MatchState
    public let deltaMs: Int64?

    public init(_ state: MatchState, deltaMs: Int64? = nil) {
        self.state = state
        self.deltaMs = deltaMs
    }

    /// Nil unless `state` is a v1 state and `deltaMs`, when present, an int.
    init?(json: JSON) {
        guard let o = json.object, let state = o["state"]?.string.flatMap(MatchState.init(rawValue:)) else { return nil }
        var delta: Int64?
        if let d = o["deltaMs"] {
            guard let int = d.int else { return nil }
            delta = int
        }
        self.init(state, deltaMs: delta)
    }

    var wireText: String {
        var w = JSONObjectWriter()
        w.put("state", state.rawValue)
        if let deltaMs { w.put("deltaMs", deltaMs) }
        return w.done()
    }
}

/// The SDK's connection to the room (SDK.md §16.6).
public enum Connection: Hashable, Sendable, CaseIterable {
    /// The party is not in a room and not trying to be.
    case idle
    /// `create`, `join` or `resume` is under way.
    case connecting
    /// The party is a member with an open connection.
    case connected
    /// The connection dropped, and the SDK is resuming (SDK.md §14).
    case reconnecting
    /// The party ended, for the reason in `PartyState.endReason`.
    case ended
}

/// Why the party ended (SDK.md §7.4).
public enum EndReason: Hashable, Sendable, CaseIterable {
    /// The app called `leave()`.
    case left
    /// The host ended the room, or the relay closed it for a reason this SDK doesn't know.
    case ended
    /// The host removed this participant.
    case kicked
    /// The room reached its maximum age.
    case expired
    /// A resume on another connection took this participant over.
    case replaced
    /// Resuming became impossible, and `EndedEvent.error` says why.
    case lost
}

/// One entry of the room's `participants`.
public struct Participant: Hashable, Sendable {
    public let peerId: String
    public let name: String
    /// The participant's app, as `<app>/<version>`.
    public let client: String
    public let host: Bool
    public let connected: Bool
    public let player: PlayerState
    public let match: Match?
    public let offsetMs: Int64
}

/// The room snapshot (SPEC.md §9).
public struct Room: Hashable, Sendable {
    public let rev: Int64
    public let code: String
    public let epoch: Int64
    public let phase: Phase
    public let positionMs: Int64
    /// A relay time.
    ///
    /// In `playing`, it is the moment at which playback runs from `positionMs`.
    public let atMs: Int64
    public let rate: Double
    public let control: Control
    public let locked: Bool
    /// The peer IDs the barrier waits for.
    public let waitingOn: [String]
    /// Every participant, connected or not, in join order.
    public let participants: [Participant]

    /// The snapshot of a `room` or of `joined.room`; nil when it is malformed (SDK.md §8, `snapshot.schema.json`): a
    /// member missing or of the wrong type, an int out of range, `rate` outside 0.5–2.0, an unknown value of a closed
    /// enum, a bad peer ID, `name` or `client`, other than exactly one host, a repeated peer ID in `waitingOn` or a
    /// non-empty one outside `waiting`, or an `idle` room away from position 0. Unknown members are ignored.
    init?(json: JSON?) {
        guard let o = json?.object,
              let phase = o["phase"]?.string.flatMap(Phase.init(rawValue:)),
              let positionMs = o["positionMs"]?.int, (0...Owp.maxDurationMs).contains(positionMs),
              phase != .idle || positionMs == 0,
              let waitingJSON = o["waitingOn"]?.array else { return nil }
        var waitingOn: [String] = []
        for id in waitingJSON {
            guard let s = id.string, isPeerId(s) else { return nil }
            waitingOn.append(s)
        }
        guard Set(waitingOn).count == waitingOn.count, phase == .waiting || waitingOn.isEmpty,
              let participantsJSON = o["participants"]?.array else { return nil }
        var participants: [Participant] = []
        for p in participantsJSON {
            guard let participant = Room.participant(p) else { return nil }
            participants.append(participant)
        }
        guard participants.filter(\.host).count == 1,
              let rev = o["rev"]?.int, rev >= 1,
              let code = o["code"]?.string, isRoomCode(code),
              let epoch = o["epoch"]?.int, epoch >= 0,
              let atMs = o["atMs"]?.int, atMs >= 0,
              let rate = o["rate"]?.number, rate >= Owp.minRate, rate <= Owp.maxRate,
              let control = o["control"]?.string.flatMap(Control.init(rawValue:)),
              let locked = o["locked"]?.bool else { return nil }
        self.init(
            rev: rev, code: code, epoch: epoch, phase: phase, positionMs: positionMs, atMs: atMs, rate: rate,
            control: control, locked: locked, waitingOn: waitingOn, participants: participants
        )
    }

    init(
        rev: Int64, code: String, epoch: Int64, phase: Phase, positionMs: Int64, atMs: Int64, rate: Double,
        control: Control, locked: Bool, waitingOn: [String], participants: [Participant]
    ) {
        self.rev = rev
        self.code = code
        self.epoch = epoch
        self.phase = phase
        self.positionMs = positionMs
        self.atMs = atMs
        self.rate = rate
        self.control = control
        self.locked = locked
        self.waitingOn = waitingOn
        self.participants = participants
    }

    private static func participant(_ json: JSON) -> Participant? {
        guard let o = json.object, let matchJSON = o["match"] else { return nil }
        var match: Match?
        if !matchJSON.isNull {
            guard let m = Match(json: matchJSON) else { return nil }
            match = m
        }
        guard let peerId = o["peerId"]?.string, isPeerId(peerId),
              let name = o["name"]?.text, nameProblem(name.value, unpaired: name.unpaired) == nil,
              let client = o["client"]?.text, clientProblem(client.value, unpaired: client.unpaired) == nil,
              let host = o["host"]?.bool,
              let connected = o["connected"]?.bool,
              let player = o["player"]?.string.flatMap(PlayerState.init(rawValue:)),
              let offsetMs = o["offsetMs"]?.int, (-Owp.maxOffsetMs...Owp.maxOffsetMs).contains(offsetMs) else { return nil }
        return Participant(
            peerId: peerId, name: name.value, client: client.value, host: host, connected: connected, player: player,
            match: match, offsetMs: offsetMs
        )
    }

    /// The first entry with `peerId`.
    func participant(_ peerId: String?) -> Participant? {
        guard let peerId else { return nil }
        return participants.first { $0.peerId == peerId }
    }
}
