import type { PartyError } from "./error.ts";

/** The room's phase (SPEC.md §9.1). */
export type Phase = "idle" | "paused" | "waiting" | "playing";
/** Who may send play, pause, seek and rate (SPEC.md §11.3). */
export type Control = "host" | "everyone";
/** A participant's player state, as reported in `status` (SPEC.md §7.5). */
export type PlayerState = "idle" | "loading" | "ready" | "buffering";
/** A participant's match state (SPEC.md §7.5). */
export type MatchState = "searching" | "matched" | "mismatched" | "unavailable";
/** The state of the party's connection (SDK.md §16.6). */
export type Connection = "idle" | "connecting" | "connected" | "reconnecting" | "ended";
/** Why a party ended (SDK.md §7.4). */
export type EndReason = "left" | "ended" | "kicked" | "expired" | "replaced" | "lost";
/** The kind of a media descriptor (SPEC.md §10). */
export type MediaKind = "movie" | "episode" | "video";

// Every type the SDK publishes is read-only all the way down, and the SDK freezes the objects and arrays it creates
// for them (SDK.md §16.6). Writing to one throws in strict mode.

/** The file hints of a media descriptor (SPEC.md §10). */
export interface MediaFile {
  readonly sizeBytes?: number;
  /** The file's OpenSubtitles hash, as 16 lower-case hex digits. */
  readonly oshash?: string;
  /** The file's name, without directories. */
  readonly name?: string;
}

/** The series of an episode (SPEC.md §10). */
export interface Series {
  readonly title: string;
  readonly year?: number;
  readonly ids?: Readonly<Record<string, string>>;
}

/**
 * The media descriptor (SPEC.md §10).
 *
 * Positions in the room are in the timeline of the file this descriptor describes.
 */
export interface Media {
  readonly kind: MediaKind;
  readonly title: string;
  readonly year?: number;
  /**
   * The title's IDs, keyed by namespace.
   *
   * The v1 namespaces are `imdb`, `tmdb`, `tvdb`, `plex`, `anilist`, `anidb`, `kitsu` and `mal`.
   */
  readonly ids?: Readonly<Record<string, string>>;
  /** The title's series, which an episode must have. */
  readonly series?: Series;
  readonly season?: number;
  readonly episode?: number;
  /** The reference file's duration. */
  readonly durationMs: number;
  readonly file?: MediaFile;
  /** App-private hints, keyed by reverse-DNS name. They take at most 1024 bytes. */
  readonly ext?: Readonly<Record<string, unknown>>;
}

/** A match state and its duration difference, as `{state, deltaMs}` on the wire. */
export interface Match {
  readonly state: MatchState;
  /** The participant's own file duration minus the descriptor's `durationMs`. */
  readonly deltaMs?: number;
}

/** One entry of the snapshot's `participants`. */
export interface Participant {
  readonly peerId: string;
  readonly name: string;
  readonly client: string;
  readonly host: boolean;
  readonly connected: boolean;
  readonly player: PlayerState;
  readonly match: Match | null;
  readonly offsetMs: number;
}

/** The room snapshot (SPEC.md §9). */
export interface Room {
  readonly rev: number;
  readonly code: string;
  readonly epoch: number;
  readonly phase: Phase;
  readonly positionMs: number;
  readonly atMs: number;
  readonly rate: number;
  readonly control: Control;
  readonly locked: boolean;
  readonly waitingOn: readonly string[];
  readonly participants: readonly Participant[];
}

/** One relay of a relay list (SPEC.md §5.2). */
export interface Relay {
  readonly id: string;
  readonly name: string;
  /**
   * The relay's WebSocket URL, `wss://…/v1/ws`.
   *
   * A relay from `Relays.at` with an `http://` base URL has a `ws://` URL instead.
   */
  readonly url: string;
  /** The first symbols of the room codes this relay owns. */
  readonly prefixes: string;
}

/**
 * The app's player, which the SDK reads and commands (SDK.md §16.4).
 *
 * Reads are cheap and synchronous. A command resolves once the player has done it, and `seek` resolves once the seek
 * has landed.
 *
 * A command must finish or throw. Until it does, the adapter's later commands wait for it, and so does the completion
 * of `detachPlayer` and `close`.
 *
 * `setRate` is optional. An adapter with `setRate` can set rates. Without it, the SDK follows the room by seeking only.
 */
export interface PlayerAdapter {
  /** The current position in the app's own file. */
  readonly positionMs: number;
  /** The file's duration, or null when it is unknown. */
  readonly durationMs: number | null;
  readonly paused: boolean;
  /** True while the player is stalled trying to play. */
  readonly buffering: boolean;
  /** True when the file is open and shows a frame. */
  readonly firstFrameShown: boolean;
  /**
   * How far from the requested position a seek may land, in ms.
   *
   * A player that seeks to keyframes declares its keyframe distance. A player that can't seek at all declares a very
   * large value, and the SDK then follows it by rate only. A value that is absent, or not an integer ≥ 0, counts as 0.
   */
  readonly seekAccuracyMs?: number;
  /**
   * Starts the player, or throws when the player refuses to start.
   *
   * A browser refuses, for example, when it wants a user gesture. The SDK then reports `loading` and emits
   * `playBlocked` (SDK.md §10.2).
   */
  play(): Promise<void> | void;
  pause(): Promise<void> | void;
  seek(positionMs: number): Promise<void> | void;
  setRate?(rate: number): Promise<void> | void;
}

/**
 * One file the user could play (SDK.md §9.2).
 *
 * The SDK never changes or freezes a candidate.
 */
export interface Candidate<T> {
  /** The app's key for this file, unique among the candidates. */
  id: string;
  /** The app's own value for this file, which the SDK hands back in `choice`. */
  ref: T;
  /**
   * The file's duration, when known.
   *
   * Only an integer from 1 to 604800000 is a duration. Any other value counts as unknown.
   */
  durationMs?: number;
  sizeBytes?: number;
  oshash?: string;
  name?: string;
  /**
   * Opens the file, learns its duration and closes the file again.
   *
   * It returns the duration, or nothing when the file doesn't open. An answer that isn't an integer from 1 to
   * 604800000 is a failed probe.
   *
   * The SDK calls it only when the candidate has no known `durationMs`. It aborts `signal` when it no longer needs the
   * answer.
   */
  probe?: (signal: AbortSignal) => Promise<number | null | undefined>;
}

/** A candidate of the matching walk, with its `deltaMs` once known (SDK.md §9.5). */
export interface Ranked<T> {
  readonly candidate: Candidate<T>;
  readonly deltaMs: number | null;
}

/** The file offered while the user decides about a mismatch. */
export interface Mismatch<T> {
  readonly candidate: Candidate<T>;
  readonly deltaMs: number;
}

/**
 * The app's search, which finds the candidates for a title.
 *
 * `resolve` returns the candidates in the app's order of preference. Aborting `signal` cancels the search.
 */
export interface Resolver<T> {
  resolve(media: Media, signal: AbortSignal): Promise<Candidate<T>[]> | Candidate<T>[];
}

/** Everything the UI shows, as one value (SDK.md §16.6). */
export interface PartyState<T> {
  readonly connection: Connection;
  /** The party's own peer ID while it is in a room. */
  readonly peerId: string | null;
  /** The last accepted snapshot. */
  readonly room: Room | null;
  /** The current descriptor. */
  readonly media: Media | null;
  /** The match state the party reports in `status`. */
  readonly match: Match | null;
  /**
   * The file to open for the current media.
   *
   * The party's own media is the descriptor it last sent in `setMedia`. For that media, the choice is a candidate
   * with id `""`, the `ref` passed to `setMedia` and the descriptor's `durationMs`.
   */
  readonly choice: Candidate<T> | null;
  /** The file offered while the user decides. */
  readonly mismatch: Mismatch<T> | null;
  /**
   * The walk's ranked candidates.
   *
   * After `resolveAgain()` with a choice, this is the resolver's new ranked list instead.
   */
  readonly candidates: readonly Ranked<T>[];
  /**
   * This participant's offset (SDK.md §13).
   *
   * The local position is the room position plus `offsetMs`.
   */
  readonly offsetMs: number;
  /** The player state the party reports in `status`. */
  readonly player: PlayerState;
  readonly endReason: EndReason | null;
  /** The party's own entry in `room.participants`. */
  readonly me: Participant | null;
  readonly isHost: boolean;
  /** True when the party is the host, or when `control` is `everyone`. */
  readonly canControl: boolean;
}

/**
 * What the SDK reports besides state (SDK.md §15).
 *
 * New event types may appear in SDK 1.x. Handle events by `type`, and ignore the ones you don't know in a `default`
 * branch.
 */
export type PartyEvent =
  | {
      readonly type: "action";
      readonly kind: string;
      /** The actor's peer ID, as the room's `action` names it, or null for the relay. */
      readonly byPeerId: string | null;
      /** The actor's entry, or null for the relay and for an actor who has left. */
      readonly by: Participant | null;
      readonly byMe: boolean;
      readonly room: Room;
    }
  | { readonly type: "participantJoined"; readonly participant: Participant }
  | { readonly type: "participantLeft"; readonly participant: Participant }
  | { readonly type: "error"; readonly error: PartyError }
  | { readonly type: "resolveFailed"; readonly error: unknown }
  /**
   * The player refused to start (SDK.md §10.2).
   *
   * A browser does this, for example, when it blocks playback without a user gesture. Ask the user to press play on
   * the player itself. `error` is what the adapter's `play` threw.
   */
  | { readonly type: "playBlocked"; readonly error: unknown }
  | { readonly type: "ended"; readonly reason: EndReason; readonly error: PartyError | null };
