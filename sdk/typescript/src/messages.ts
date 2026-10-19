import { ALPHABET } from "./code.ts";
import { PartyError } from "./error.ts";
import { frozen } from "./freeze.ts";
import { MAX_DURATION_MS, receivedMedia } from "./media.ts";
import { isObject } from "./relaylist.ts";
import { checkClient, checkText } from "./text.ts";
import type { Match, Media, Participant, Room } from "./types.ts";

// The checks of what the relay sends (SDK.md §8). Each parser returns null for a message that fails its schema, and
// the party changes no state for it.

const PEER_ID = /^[A-Za-z0-9_-]{1,16}$/;
const TOKEN = /^[A-Za-z0-9_-]{43}$/;
const CODE = new RegExp(`^[${ALPHABET}]{6}$`);

/** @internal A `joined` answer (SPEC.md §8.1). */
export interface Joined {
  code: string;
  peerId: string;
  token: string;
  room: Room;
  media: Media | null;
}

/** @internal The action a `room` message carries (SPEC.md §9.1); `by` is null for the relay. */
export interface Action {
  kind: string;
  by: string | null;
}

/** @internal The largest offset either way: 7 days (SDK.md §13). */
export const MAX_OFFSET_MS = 604_800_000;

/** An int of SPEC.md §2 (integral, within ±(2^53 − 1)) from `min` to `max`. */
function int(v: unknown, min = -Number.MAX_SAFE_INTEGER, max = Number.MAX_SAFE_INTEGER): v is number {
  return Number.isSafeInteger(v) && (v as number) >= min && (v as number) <= max;
}

/**
 * The snapshot fields of a `room` message or `joined.room`, checked against `snapshot.schema.json` (SDK.md §8), as a
 * frozen snapshot; null when they fail. Unknown members are ignored.
 */
function parseRoom(v: unknown): Room | null {
  if (!isObject(v)) return null;
  const { rev, code, epoch, phase, positionMs, atMs, rate, control, locked, waitingOn, participants } = v;
  if (
    !int(rev, 1) ||
    typeof code !== "string" ||
    !CODE.test(code) ||
    !int(epoch, 0) ||
    (phase !== "idle" && phase !== "paused" && phase !== "waiting" && phase !== "playing") ||
    !int(positionMs, 0, MAX_DURATION_MS) ||
    !int(atMs, 0) ||
    typeof rate !== "number" ||
    !(rate >= 0.5 && rate <= 2) ||
    (control !== "host" && control !== "everyone") ||
    typeof locked !== "boolean" ||
    !Array.isArray(waitingOn) ||
    !Array.isArray(participants) ||
    participants.length === 0
  ) {
    return null;
  }
  if (phase === "idle" && positionMs !== 0) return null;
  if (phase !== "waiting" && waitingOn.length > 0) return null;
  const waiting = new Set<string>();
  for (const peerId of waitingOn) {
    if (typeof peerId !== "string" || !PEER_ID.test(peerId) || waiting.has(peerId)) return null;
    waiting.add(peerId);
  }
  const list: Participant[] = [];
  for (const entry of participants) {
    const p = parseParticipant(entry);
    if (p === null) return null;
    list.push(p);
  }
  if (list.filter((p) => p.host).length !== 1) return null;
  return frozen({
    rev,
    code,
    epoch,
    phase,
    positionMs,
    atMs,
    rate,
    control,
    locked,
    waitingOn: [...waiting],
    participants: list,
  });
}

function parseParticipant(v: unknown): Participant | null {
  if (!isObject(v)) return null;
  const { peerId, name, client, host, connected, player, match, offsetMs } = v;
  if (typeof peerId !== "string" || !PEER_ID.test(peerId)) return null;
  if (checkText(name, 32) !== null || checkClient(client) !== null) return null;
  if (typeof host !== "boolean" || typeof connected !== "boolean" || !int(offsetMs, -MAX_OFFSET_MS, MAX_OFFSET_MS)) return null;
  if (player !== "idle" && player !== "loading" && player !== "ready" && player !== "buffering") return null;
  let parsed: Match | null = null;
  if (match !== null) {
    parsed = parseMatch(match);
    if (parsed === null) return null;
  }
  return { peerId, name: name as string, client: client as string, host, connected, player, match: parsed, offsetMs };
}

function parseMatch(v: unknown): Match | null {
  if (!isObject(v)) return null;
  const { state, deltaMs } = v;
  if (state !== "searching" && state !== "matched" && state !== "mismatched" && state !== "unavailable") return null;
  if (deltaMs === undefined) return { state };
  return int(deltaMs) ? { state, deltaMs } : null;
}

/**
 * @internal A message's `media` member: null, or a descriptor that passes the receive check; undefined when it is
 * absent or neither.
 */
export function mediaMember(v: unknown): Media | null | undefined {
  return v === null ? null : (receivedMedia(v) ?? undefined);
}

/**
 * @internal A `room` message's snapshot and `action` (SDK.md §8): an `action`, when present, is an object with `kind`
 * a non-empty string and `by` null or a peer ID. Null when either fails. Its `media` is the party's to check, on a new
 * epoch only (SDK.md §9.1).
 */
export function parseRoomMessage(m: Record<string, unknown>): { room: Room; action: Action | null } | null {
  const room = parseRoom(m);
  if (room === null) return null;
  if (!("action" in m)) return { room, action: null };
  const action = m["action"];
  if (!isObject(action) || !("by" in action)) return null;
  const { kind, by } = action;
  if (typeof kind !== "string" || kind === "") return null;
  if (by !== null && !(typeof by === "string" && PEER_ID.test(by))) return null;
  return { room, action: { kind, by } };
}

/** @internal A `joined` checked against `joined.schema.json`, its room and media included; null when it fails. */
export function parseJoined(m: Record<string, unknown>): Joined | null {
  const { code, you, serverTime } = m;
  if (typeof code !== "string" || !CODE.test(code) || !isObject(you) || !int(serverTime, 0)) return null;
  const { peerId, token } = you;
  if (typeof peerId !== "string" || !PEER_ID.test(peerId) || typeof token !== "string" || !TOKEN.test(token)) {
    return null;
  }
  const room = parseRoom(m["room"]);
  const media = mediaMember(m["media"]);
  return room === null || media === undefined ? null : { code, peerId, token, room, media };
}

/**
 * @internal An `error` as a `PartyError` whose message is the relay's text, for logs only (SDK.md §15); null when the
 * message fails `error.schema.json`.
 */
export function parseRelayError(m: Record<string, unknown>): PartyError | null {
  const { code, message, re } = m;
  if (typeof code !== "string" || code === "" || typeof message !== "string") return null;
  if (re !== null && typeof re !== "string") return null;
  return new PartyError(code, message, re);
}
