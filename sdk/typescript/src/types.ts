import type { PartyError } from "./error.ts";

/** Room phase (SPEC.md §9.1). */
export type Phase = "idle" | "paused" | "waiting" | "playing";
/** Who may send play, pause, seek and rate (SPEC.md §11.3). */
export type Control = "host" | "everyone";
/** A participant's player state as reported in `status` (SPEC.md §7.5). */
export type PlayerState = "idle" | "loading" | "ready" | "buffering";
/** A participant's match state (SPEC.md §7.5). */
export type MatchState = "searching" | "matched" | "mismatched" | "unavailable";
/** The party's connection (SDK.md §16.6). */
export type Connection = "idle" | "connecting" | "connected" | "reconnecting" | "ended";
/** Why a party ended (SDK.md §7.4). */
export type EndReason = "left" | "ended" | "kicked" | "expired" | "replaced" | "lost";
/** The media descriptor's kind (SPEC.md §10). */
export type MediaKind = "movie" | "episode" | "video";

// Every type the SDK publishes is read-only all the way down, and the SDK freezes the objects and arrays it creates
// for them (SDK.md §16.6). Writing to one throws in strict mode.

/** File hints of a descriptor (SPEC.md §10). */
export interface MediaFile {
  readonly sizeBytes?: number;
  /** OpenSubtitles hash: 16 lower-case hex digits. */
  readonly oshash?: string;
  /** File name without directories. */
  readonly name?: string;
}

/** The series of an episode (SPEC.md §10). */
export interface Series {
  readonly title: string;
  readonly year?: number;
  readonly ids?: Readonly<Record<string, string>>;
}

/** The media descriptor (SPEC.md §10). Positions in the room are in this file's timeline. */
export interface Media {
  readonly kind: MediaKind;
  readonly title: string;
  readonly year?: number;
  /** Namespace → ID: `imdb`, `tmdb`, `tvdb`, `plex`, `anilist`, `anidb`, `kitsu`, `mal`. */
  readonly ids?: Readonly<Record<string, string>>;
  /** Required for an episode. */
  readonly series?: Series;
  readonly season?: number;
  readonly episode?: number;
  /** The reference file's duration. */
  readonly durationMs: number;
  readonly file?: MediaFile;
  /** App-private hints keyed by reverse-DNS name, at most 1024 bytes. */
  readonly ext?: Readonly<Record<string, unknown>>;
}

/** `{state, deltaMs}` as on the wire. `deltaMs` is own duration − `durationMs`. */
export interface Match {
  readonly state: MatchState;
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
  /** The relay's `wss://…/v1/ws` URL; `ws://` for `Relays.at` with an `http://` base. */
  readonly url: string;
  /** The first code symbols this relay owns. */
  readonly prefixes: string;
}

/**
 * The app's player, read and commanded by the SDK (SDK.md §16.4). Reads are cheap and synchronous. Commands resolve
 * once the player has done them; `seek` resolves once the seek has landed. An adapter with `setRate` can set rates;
 * without it the SDK follows by seeking only. A command must finish or throw: until it does, the adapter's later
 * commands and the completion of `detachPlayer` and `close` wait for it.
 */
export interface PlayerAdapter {
  /** Current position in the app's own file. */
  readonly positionMs: number;
  /** The file's duration; null when unknown. */
  readonly durationMs: number | null;
  readonly paused: boolean;
  /** Stalled while trying to play. */
  readonly buffering: boolean;
  /** The file is open and shows a frame. */
  readonly firstFrameShown: boolean;
  /**
   * How far from the requested position a seek may land, in ms: a player that seeks to keyframes declares its keyframe
   * distance, and one that can't seek at all a very large value (it is then followed by rate only). Absent, or not an
   * integer ≥ 0, counts as 0.
   */
  readonly seekAccuracyMs?: number;
  /**
   * Throws when the player refuses to start, such as a browser that wants a user gesture; the SDK then reports
   * `loading` and emits `playBlocked` (SDK.md §10.2).
   */
  play(): Promise<void> | void;
  pause(): Promise<void> | void;
  seek(positionMs: number): Promise<void> | void;
  setRate?(rate: number): Promise<void> | void;
}

/** One file the user could play (SDK.md §9.2). The SDK never changes or freezes it. */
export interface Candidate<T> {
  /** The app's key for it, unique among the candidates. */
  id: string;
  /** The app's own value, handed back with `choice`. */
  ref: T;
  /** The file's duration when known: an integer from 1 to 604800000. Any other value counts as unknown. */
  durationMs?: number;
  sizeBytes?: number;
  oshash?: string;
  name?: string;
  /**
   * Opens the file, learns its duration and closes it again. Returns the duration, or nothing when the file doesn't
   * open; an answer that isn't an integer from 1 to 604800000 is a failed probe. Called only without a known
   * `durationMs`. The SDK aborts `signal` when it no longer needs the answer.
   */
  probe?: (signal: AbortSignal) => Promise<number | null | undefined>;
}

/** A candidate of the walk and its `deltaMs` once known (SDK.md §9.5). */
export interface Ranked<T> {
  readonly candidate: Candidate<T>;
  readonly deltaMs: number | null;
}

/** The file offered while the user decides about a mismatch. */
export interface Mismatch<T> {
  readonly candidate: Candidate<T>;
  readonly deltaMs: number;
}

/** The app's search: the candidates for a title, in the app's order of preference. Abort `signal` cancels it. */
export interface Resolver<T> {
  resolve(media: Media, signal: AbortSignal): Promise<Candidate<T>[]> | Candidate<T>[];
}

/** Everything the UI shows, as one value (SDK.md §16.6). */
export interface PartyState<T> {
  readonly connection: Connection;
  /** Own peer ID while in a room. */
  readonly peerId: string | null;
  /** The last accepted snapshot. */
  readonly room: Room | null;
  /** The current descriptor. */
  readonly media: Media | null;
  /** As reported in `status`. */
  readonly match: Match | null;
  /**
   * The file to open for the current media. For the party's own media (the descriptor it last sent in `setMedia`), a
   * candidate with id `""`, the `ref` passed there and the descriptor's `durationMs`.
   */
  readonly choice: Candidate<T> | null;
  /** The file offered while the user decides. */
  readonly mismatch: Mismatch<T> | null;
  /** The walk's ranked candidates, or after `resolveAgain()` with a choice, the resolver's new ranked list. */
  readonly candidates: readonly Ranked<T>[];
  /** Local position = room position + `offsetMs` (SDK.md §13). */
  readonly offsetMs: number;
  /** As reported in `status`. */
  readonly player: PlayerState;
  readonly endReason: EndReason | null;
  /** Own entry of `room.participants`. */
  readonly me: Participant | null;
  readonly isHost: boolean;
  /** Host, or `control` is `everyone`. */
  readonly canControl: boolean;
}

/**
 * What the SDK reports besides state (SDK.md §15). New event types may appear in SDK 1.x: handle events by `type` and
 * ignore the ones you don't know, in a `default` branch.
 */
export type PartyEvent =
  | {
      readonly type: "action";
      readonly kind: string;
      /** The actor's peer ID, as the room's `action` names it; null for the relay. */
      readonly byPeerId: string | null;
      /** The actor's entry; null when the actor has left or for the relay. */
      readonly by: Participant | null;
      readonly byMe: boolean;
      readonly room: Room;
    }
  | { readonly type: "participantJoined"; readonly participant: Participant }
  | { readonly type: "participantLeft"; readonly participant: Participant }
  | { readonly type: "error"; readonly error: PartyError }
  | { readonly type: "resolveFailed"; readonly error: unknown }
  /**
   * The player refused to start, such as a browser blocking playback without a user gesture (SDK.md §10.2): ask the
   * user to press play on the player itself. Carries what the adapter's `play` threw.
   */
  | { readonly type: "playBlocked"; readonly error: unknown }
  | { readonly type: "ended"; readonly reason: EndReason; readonly error: PartyError | null };
