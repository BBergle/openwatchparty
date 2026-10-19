/// The app's player, which the SDK reads and commands so that it follows the room (SDK.md §16.4).
///
/// The SDK calls the adapter on the main actor only, and runs one command at a time per adapter.
///
/// The SDK never infers the user's presses from these values. The app sends them through the party instead.
///
/// The optional members have defaults in a protocol extension, so a 1.x release can add members without breaking an
/// adapter.
@MainActor
public protocol PlayerAdapter: AnyObject {
    /// The current position in the app's own file.
    var positionMs: Int64 { get }
    /// The file's duration, or nil when it is unknown.
    ///
    /// Without a duration, the SDK doesn't protect the end of a shorter file.
    var durationMs: Int64? { get }
    var paused: Bool { get }
    /// Whether the player is stalled while trying to play.
    var buffering: Bool { get }
    /// Whether the file is open and shows a frame.
    ///
    /// The value may stay false until the app's own start-up work is done.
    var firstFrameShown: Bool { get }
    /// Whether `setRate` works right now.
    ///
    /// The value may change, for example to false while audio passthrough is on. The default is false.
    var canSetRate: Bool { get }
    /// How far from the requested position a seek may land.
    ///
    /// - A player that seeks to keyframes declares its keyframe distance.
    /// - A player that can't seek at all declares a very large value. The SDK then follows the room by rate only.
    ///
    /// The SDK reads the value on every step, and counts a negative value as 0. The default is 0.
    var seekAccuracyMs: Int64 { get }
    /// Starts playing, and returns once the player plays.
    ///
    /// - Throws: An error when the player refuses to start. While the player then stays paused, the party reports
    ///   `loading` and emits `PlayBlockedEvent` (SDK.md §10.2).
    func play() async throws
    func pause() async throws
    /// Seeks to `positionMs`, and returns once the seek has landed.
    func seek(to positionMs: Int64) async throws
    /// Sets the playback rate.
    ///
    /// The default implementation does nothing.
    func setRate(_ rate: Double) async throws
}

extension PlayerAdapter {
    public var canSetRate: Bool { false }
    public var seekAccuracyMs: Int64 { 0 }
    public func setRate(_ rate: Double) async throws {}
}

/// The app's search for the room's media among the user's files (SDK.md §9.2).
///
/// The closure returns the files that are this title, in the app's order of preference. It runs on the main actor. When
/// the room's media changes, the SDK cancels it through Task cancellation.
///
/// A resolver that throws counts as having returned no candidates, and so does one that takes longer than 30 seconds. A
/// cancellation of its own, such as its own timeout, counts as a throw.
public struct Resolver<Ref> {
    let resolve: @MainActor (Media) async throws -> [Candidate<Ref>]

    public init(_ resolve: @escaping @MainActor (Media) async throws -> [Candidate<Ref>]) {
        self.resolve = resolve
    }
}

/// One file the user could play (SDK.md §9.2).
///
/// `PartyState` compares candidates by identity.
public final class Candidate<Ref>: Identifiable {
    /// The app's key, unique among the candidates.
    public let id: String
    /// The app's own value, handed back with the choice.
    public let ref: Ref
    /// The file's duration, when the app knows it.
    ///
    /// Only a value from 1 to 604800000 ms, which is 7 days, counts. The SDK treats any other value as no duration.
    public let durationMs: Int64?
    public let sizeBytes: Int64?
    public let oshash: String?
    public let name: String?
    /// Opens the file, learns its duration and closes the file again.
    ///
    /// The probe returns nil when the file doesn't open. The SDK calls it only when the candidate has no `durationMs`
    /// that counts.
    ///
    /// A probe fails when it returns a duration outside 1 to 604800000, or when it throws. A cancellation of the
    /// probe's own, such as its own timeout, counts as a throw.
    ///
    /// The SDK cancels a probe it no longer needs through Task cancellation. The probe then releases what it opened.
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

/// A candidate of the matching walk, with its duration difference once that is known (SDK.md §9.5).
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

/// The closest file, offered while the user decides whether to play it (SDK.md §9.6).
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

/// Everything the UI shows, as one value (SDK.md §16.6).
///
/// The state changes on events, never per tick.
public struct PartyState<Ref>: Equatable {
    public var connection: Connection
    /// The party's own peer ID while it is in a room.
    public var peerId: String?
    /// The last accepted snapshot.
    public var room: Room?
    /// The current media descriptor.
    public var media: Media?
    /// The party's match for the current media, as reported in `status`.
    public var match: Match?
    /// The file to open for the current media.
    ///
    /// For the party's own media (SDK.md §9.1), the choice is a candidate with id `""`, the `ref` passed to `setMedia`
    /// and the descriptor's duration.
    public var choice: Candidate<Ref>?
    /// The file offered while the user decides.
    public var mismatch: Mismatch<Ref>?
    /// The walk's ranked candidates.
    public var candidates: [Ranked<Ref>]
    /// The offset of the user's file from the room's timeline (SDK.md §13).
    ///
    /// The local position is the room position plus `offsetMs`.
    public var offsetMs: Int64
    /// The party's player state, as reported in `status`.
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

    /// The party's own entry in `room.participants`.
    public var me: Participant? { room?.participant(peerId) }

    public var isHost: Bool { me?.host == true }

    /// Whether the party may control playback.
    ///
    /// It may when it is the host, or when the room's control is `everyone`.
    public var canControl: Bool { isHost || (me != nil && room?.control == .everyone) }

    public static func == (a: PartyState, b: PartyState) -> Bool {
        a.connection == b.connection && a.peerId == b.peerId && a.room == b.room && a.media == b.media
            && a.match == b.match && a.choice === b.choice && a.mismatch == b.mismatch && a.candidates == b.candidates
            && a.offsetMs == b.offsetMs && a.player == b.player
            && a.endReason == b.endReason
    }
}

extension PartyState: Sendable where Ref: Sendable {}

/// Something that happened in the room (SDK.md §15).
///
/// Everything else is part of `PartyState`.
///
/// A later SDK 1.x may add events. Switch over the event types the app knows, with a `default:` branch that ignores the
/// others.
public protocol PartyEvent: Sendable {}

/// An action in the room (SDK.md §15).
///
/// `kind` is `play`, `pause`, `seek`, `rate`, `media`, `stall`, `ended` or `host`, or a newer kind.
public struct ActionEvent: PartyEvent {
    public let kind: String
    /// The actor's peer ID, or nil when the relay acted.
    public let byPeerId: String?
    /// The actor's entry, or nil when the actor has left or is the relay.
    public let by: Participant?
    public let byMe: Bool
    public let room: Room
}

public struct ParticipantJoinedEvent: PartyEvent {
    public let participant: Participant
}

/// A participant left the room.
///
/// The event carries the participant's last known entry.
public struct ParticipantLeftEvent: PartyEvent {
    public let participant: Participant
}

/// The relay reported an error.
///
/// The relay refused a command, a `status` or a `ping`, or it sent an error about nothing in particular.
public struct ErrorEvent: PartyEvent {
    public let error: PartyError
}

/// The resolver threw or took longer than 30 seconds.
///
/// Matching went on with no candidates. After a timeout, `error` is a `TimeoutError`.
public struct ResolveFailedEvent: PartyEvent {
    public let error: any Error
}

/// The player refused to start (SDK.md §10.2).
///
/// The player's `play` threw `error`, and the player stayed paused. Ask the user to press play on the player itself.
///
/// The party emits this event again only after the player played, the room stopped playing or the adapter unbound.
public struct PlayBlockedEvent: PartyEvent {
    public let error: any Error
}

/// The party ended.
///
/// For `EndReason.lost`, `error` is the cause.
public struct EndedEvent: PartyEvent {
    public let reason: EndReason
    public let error: PartyError?
}

/// The error reported for a resolver call that took longer than 30 seconds (SDK.md §9.2).
public struct TimeoutError: Error, Hashable, Sendable, CustomStringConvertible {
    public let timeoutMs: Int64

    public var description: String { "timed out after \(timeoutMs) ms" }
}

/// A refusal or a failure.
///
/// - `code` is the relay's error code, from the open set of SPEC.md Appendix A. It can also be one of the SDK's own
///   codes: `invalid_code`, `unreachable` and `cancelled`.
/// - `re` is the type of the refused message.
/// - `message` is for logs only. When the error came from the relay, it is the relay's own text.
///
/// Apps show users their own text for each `code`, and a generic text for codes they don't know (SDK.md §15).
public struct PartyError: Error, Sendable, CustomStringConvertible {
    /// An error code.
    ///
    /// A code is a string, so a code from a newer relay passes through.
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
    /// The underlying failure of an `unreachable` error that the SDK raised.
    public let underlying: (any Error)?

    public init(_ code: Code, _ message: String? = nil, re: String? = nil, underlying: (any Error)? = nil) {
        self.code = code
        self.message = message ?? code.rawValue
        self.re = re
        self.underlying = underlying
    }

    public var description: String { "PartyError(\(code): \(message)\(re.map { ", re: \($0)" } ?? ""))" }
}
