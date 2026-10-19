/// The app's player, read and commanded by the SDK (SDK.md §16.4). The SDK calls it on the main actor only, one command
/// at a time per adapter. It never infers the user's presses from these values: the app sends them through the party.
/// The optional members have defaults in a protocol extension, so a 1.x release can add members without breaking an
/// adapter.
@MainActor
public protocol PlayerAdapter: AnyObject {
    /// The current position in the app's own file.
    var positionMs: Int64 { get }
    /// The file's duration; nil when unknown. Without it the SDK doesn't protect the end of a shorter file.
    var durationMs: Int64? { get }
    var paused: Bool { get }
    /// Stalled while trying to play.
    var buffering: Bool { get }
    /// The file is open and shows a frame. May stay false until the app's own start-up work is done.
    var firstFrameShown: Bool { get }
    /// Whether `setRate` works now; may change (false while audio passthrough is on, say). Default false.
    var canSetRate: Bool { get }
    /// How far from the requested position a seek may land: a player that seeks to keyframes declares its keyframe
    /// distance, and one that can't seek at all a very large value (the SDK then follows by rate only). Read on every
    /// step; a negative value counts as 0. Default 0.
    var seekAccuracyMs: Int64 { get }
    /// Starts playing and returns once the player plays. Throws when the player refuses to start: while it then stays
    /// paused, the party reports `loading` and emits `PlayBlockedEvent` (SDK.md §10.2).
    func play() async throws
    func pause() async throws
    /// Returns once the seek has landed.
    func seek(to positionMs: Int64) async throws
    /// Default: does nothing.
    func setRate(_ rate: Double) async throws
}

extension PlayerAdapter {
    public var canSetRate: Bool { false }
    public var seekAccuracyMs: Int64 { 0 }
    public func setRate(_ rate: Double) async throws {}
}

/// The app's search for the room's media (SDK.md §9.2): the files that are this title, in the app's order of
/// preference. It runs on the main actor and is cancelled (Task cancellation) when the room's media changes. A
/// resolver that throws, a cancellation of its own such as its own timeout included, or takes longer than 30 s,
/// returned none.
public struct Resolver<Ref> {
    let resolve: @MainActor (Media) async throws -> [Candidate<Ref>]

    public init(_ resolve: @escaping @MainActor (Media) async throws -> [Candidate<Ref>]) {
        self.resolve = resolve
    }
}

/// One file the user could play (SDK.md §9.2). Compared by identity in `PartyState`.
public final class Candidate<Ref>: Identifiable {
    /// The app's key, unique among the candidates.
    public let id: String
    /// The app's own value, handed back with the choice.
    public let ref: Ref
    /// When the app knows it. Counts only from 1 to 604800000 (7 days); any other value is no duration.
    public let durationMs: Int64?
    public let sizeBytes: Int64?
    public let oshash: String?
    public let name: String?
    /// Opens the file, learns its duration and closes it again; nil when the file doesn't open. Called only without a
    /// `durationMs` that counts; an answer outside 1 to 604800000 is a failed probe, and so is a throw, a cancellation
    /// of its own such as its own timeout included. The SDK cancels (Task cancellation) a probe it no longer needs; the
    /// probe then releases what it opened.
    public let probe: (@MainActor @Sendable () async throws -> Int64?)?

    public init(
        id: String,
        ref: Ref,
        durationMs: Int64? = nil,
        sizeBytes: Int64? = nil,
        oshash: String? = nil,
        name: String? = nil,
        probe: (@MainActor @Sendable () async throws -> Int64?)? = nil
    ) {
        self.id = id
        self.ref = ref
        self.durationMs = durationMs
        self.sizeBytes = sizeBytes
        self.oshash = oshash
        self.name = name
        self.probe = probe
    }
}

extension Candidate: @unchecked Sendable where Ref: Sendable {}

extension Candidate: CustomStringConvertible {
    public var description: String { "Candidate(\(id))" }
}

/// A candidate of the matching walk and its duration difference once known (SDK.md §9.5).
public struct Ranked<Ref>: Equatable {
    public let candidate: Candidate<Ref>
    public let deltaMs: Int64?

    public init(candidate: Candidate<Ref>, deltaMs: Int64?) {
        self.candidate = candidate
        self.deltaMs = deltaMs
    }

    public static func == (a: Ranked, b: Ranked) -> Bool { a.candidate === b.candidate && a.deltaMs == b.deltaMs }
}

extension Ranked: Sendable where Ref: Sendable {}

/// The closest file while the user decides whether to play it (SDK.md §9.6).
public struct Mismatch<Ref>: Equatable {
    public let candidate: Candidate<Ref>
    public let deltaMs: Int64

    public init(candidate: Candidate<Ref>, deltaMs: Int64) {
        self.candidate = candidate
        self.deltaMs = deltaMs
    }

    public static func == (a: Mismatch, b: Mismatch) -> Bool { a.candidate === b.candidate && a.deltaMs == b.deltaMs }
}

extension Mismatch: Sendable where Ref: Sendable {}

/// Everything the UI shows, as one value (SDK.md §16.6). It changes on events, never per tick.
public struct PartyState<Ref>: Equatable {
    public var connection: Connection
    /// Own peer ID while in a room.
    public var peerId: String?
    /// The last accepted snapshot.
    public var room: Room?
    /// The current descriptor.
    public var media: Media?
    /// As reported in `status`.
    public var match: Match?
    /// The file to open for the current media. For the party's own media (SDK.md §9.1), id `""` with the `ref` passed
    /// to `setMedia` and the descriptor's duration.
    public var choice: Candidate<Ref>?
    /// The file offered while the user decides.
    public var mismatch: Mismatch<Ref>?
    /// The walk's ranked candidates.
    public var candidates: [Ranked<Ref>]
    /// Local position = room position + `offsetMs` (SDK.md §13).
    public var offsetMs: Int64
    /// As reported in `status`.
    public var player: PlayerState
    public var endReason: EndReason?

    public init(
        connection: Connection = .idle,
        peerId: String? = nil,
        room: Room? = nil,
        media: Media? = nil,
        match: Match? = nil,
        choice: Candidate<Ref>? = nil,
        mismatch: Mismatch<Ref>? = nil,
        candidates: [Ranked<Ref>] = [],
        offsetMs: Int64 = 0,
        player: PlayerState = .idle,
        endReason: EndReason? = nil
    ) {
        self.connection = connection
        self.peerId = peerId
        self.room = room
        self.media = media
        self.match = match
        self.choice = choice
        self.mismatch = mismatch
        self.candidates = candidates
        self.offsetMs = offsetMs
        self.player = player
        self.endReason = endReason
    }

    /// Own entry of `room.participants`.
    public var me: Participant? { room?.participant(peerId) }

    public var isHost: Bool { me?.host == true }

    /// Host, or control is everyone.
    public var canControl: Bool { isHost || (me != nil && room?.control == .everyone) }

    public static func == (a: PartyState, b: PartyState) -> Bool {
        a.connection == b.connection && a.peerId == b.peerId && a.room == b.room && a.media == b.media
            && a.match == b.match && a.choice === b.choice && a.mismatch == b.mismatch && a.candidates == b.candidates
            && a.offsetMs == b.offsetMs && a.player == b.player
            && a.endReason == b.endReason
    }
}

extension PartyState: Sendable where Ref: Sendable {}

/// What happened in the room (SDK.md §15); the rest is `PartyState`. SDK 1.x may add events: switch over the types the
/// app knows, with a `default:` branch that ignores the others.
public protocol PartyEvent: Sendable {}

/// An action of the room (SDK.md §15): `kind` is `play`, `pause`, `seek`, `rate`, `media`, `stall`, `ended`, `host` or
/// newer.
public struct ActionEvent: PartyEvent {
    public let kind: String
    /// The actor; nil for the relay.
    public let byPeerId: String?
    /// The actor's entry; nil when the actor has left or is the relay.
    public let by: Participant?
    public let byMe: Bool
    public let room: Room
}

public struct ParticipantJoinedEvent: PartyEvent {
    public let participant: Participant
}

/// Carries the participant's last known entry.
public struct ParticipantLeftEvent: PartyEvent {
    public let participant: Participant
}

/// The relay refused a command, a `status` or a `ping`, or sent an error about nothing in particular.
public struct ErrorEvent: PartyEvent {
    public let error: PartyError
}

/// The resolver threw or took longer than 30 s (then `error` is a `TimeoutError`); matching went on with no
/// candidates.
public struct ResolveFailedEvent: PartyEvent {
    public let error: any Error
}

/// The player refused to start: its `play` threw `error` and it stayed paused (SDK.md §10.2). Ask the user to press
/// play on the player itself. Emitted again only after the player played, the room stopped playing or the adapter
/// unbound.
public struct PlayBlockedEvent: PartyEvent {
    public let error: any Error
}

/// The party ended; `error` is the cause for `EndReason.lost`.
public struct EndedEvent: PartyEvent {
    public let reason: EndReason
    public let error: PartyError?
}

/// The error a resolver call that took longer than 30 s reports (SDK.md §9.2).
public struct TimeoutError: Error, Hashable, Sendable, CustomStringConvertible {
    public let timeoutMs: Int64

    public var description: String { "timed out after \(timeoutMs) ms" }
}

/// A refusal or failure. `code` is the relay's error code (SPEC.md Appendix A, an open set) or the SDK's own
/// `invalid_code`, `unreachable` and `cancelled`; `re` is the refused message's type. `message` is for logs only: when
/// the error came from the relay it is the relay's own text. Apps show users their own text for `code`, and a generic
/// one for codes they don't know (SDK.md §15).
public struct PartyError: Error, Sendable, CustomStringConvertible {
    /// An error code: a string, so a newer relay's code passes through.
    public struct Code: RawRepresentable, Hashable, Sendable, ExpressibleByStringLiteral, CustomStringConvertible {
        public let rawValue: String

        public init(rawValue: String) { self.rawValue = rawValue }
        public init(stringLiteral value: String) { rawValue = value }
        public var description: String { rawValue }

        public static let invalidCode: Code = "invalid_code"
        public static let unreachable: Code = "unreachable"
        public static let cancelled: Code = "cancelled"
        public static let notInRoom: Code = "not_in_room"
        public static let alreadyInRoom: Code = "already_in_room"
        public static let forbidden: Code = "forbidden"
        public static let invalidMessage: Code = "invalid_message"
        public static let roomNotFound: Code = "room_not_found"
        public static let roomFull: Code = "room_full"
        public static let roomLocked: Code = "room_locked"
        public static let rateLimited: Code = "rate_limited"
        public static let relayFull: Code = "relay_full"
        public static let resumeFailed: Code = "resume_failed"
        public static let clientBlocked: Code = "client_blocked"
    }

    public let code: Code
    public let message: String
    public let re: String?
    /// What failed underneath, for an `unreachable` the SDK raised.
    public let underlying: (any Error)?

    public init(_ code: Code, _ message: String? = nil, re: String? = nil, underlying: (any Error)? = nil) {
        self.code = code
        self.message = message ?? code.rawValue
        self.re = re
        self.underlying = underlying
    }

    public var description: String { "PartyError(\(code): \(message)\(re.map { ", re: \($0)" } ?? ""))" }
}
