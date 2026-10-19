import { roomPosition, type Timeline } from "./timeline.ts";

/**
 * What a reconciliation step reads from the player. `rate` is the rate the SDK last set on it; `seekAccuracyMs` is the
 * adapter's read, already an integer ≥ 0.
 */
export interface StepPlayer {
  positionMs: number;
  paused: boolean;
  buffering: boolean;
  durationMs: number | null;
  canSetRate: boolean;
  rate: number;
  seekAccuracyMs: number;
}

/** One reconciliation step's input (SDK.md §11.1). */
export interface StepInput {
  room: Timeline;
  /** The media's `durationMs`. */
  durationMs: number;
  serverNowMs: number;
  offsetMs: number;
  player: StepPlayer;
  nudging: boolean;
  /** Time since the SDK last issued a seek on this player; null when it never did. */
  sinceSeekMs: number | null;
}

export type StepAction =
  | { do: "pause" }
  | { do: "setRate"; rate: number }
  | { do: "seek"; positionMs: number }
  | { do: "play" };

export interface StepResult {
  /** In the order pause, setRate, seek, play. */
  actions: StepAction[];
  nudging: boolean;
  /** When set, run a step again after this many ms (the play timer). */
  playInMs: number | null;
}

const DEADBAND_MS = 350;
const NUDGE_EXIT_MS = 150;
const SEEK_MS = 2000;
const SEEK_ONLY_MS = 1000;
const NUDGE = 0.04;
const SEEK_LEAD_MS = 250;
const SEEK_COOLDOWN_MS = 2000;
const HELD_SEEK_MS = 500;
const END_MARGIN_MS = 1000;
const RATE_EPSILON = 0.001;

/** @internal The seek threshold `ms` for an adapter that declares `seekAccuracyMs` (SDK.md §11.2). */
export function seekThreshold(ms: number, seekAccuracyMs: number): number {
  return Math.max(ms, seekAccuracyMs);
}

/**
 * Where a held player waits, and when it may start: the step's hold rule (SDK.md §11.2). Null when the player isn't
 * held (it follows the running room).
 */
export function holdPoint(
  room: Timeline,
  offsetMs: number,
  serverNowMs: number,
  playerDurationMs: number | null,
): { holdAtMs: number; playInMs: number | null } | null {
  const anchor = room.positionMs + offsetMs;
  let playInMs: number | null = null;
  if (room.phase === "playing") {
    // The quotient is its own double (SDK.md §2).
    const startAt = room.atMs + (anchor < 0 ? -anchor / room.rate : 0);
    if (serverNowMs >= startAt) return null;
    playInMs = Math.ceil(startAt - serverNowMs);
  }
  return { holdAtMs: clampLocal(anchor, playerDurationMs), playInMs };
}

/** One reconciliation step (SDK.md §11.2). Pure: the caller runs the actions and keeps `nudging`. */
export function reconcile(input: StepInput): StepResult {
  const out: StepResult = { actions: [], nudging: false, playInMs: null };
  const { room, player } = input;
  if (room.phase === "idle") return out;
  const rate = room.rate;
  const accuracy = player.seekAccuracyMs;
  const cooled = input.sinceSeekMs === null || input.sinceSeekMs >= SEEK_COOLDOWN_MS;
  const setRate = (want: number) => {
    if (player.canSetRate && Math.abs(player.rate - want) > RATE_EPSILON) {
      out.actions.push({ do: "setRate", rate: Math.round(want * 1e6) / 1e6 });
    }
  };

  const hold = holdPoint(room, input.offsetMs, input.serverNowMs, player.durationMs);
  if (hold !== null) {
    out.playInMs = hold.playInMs;
    if (!player.paused) out.actions.push({ do: "pause" });
    setRate(rate);
    if (Math.abs(player.positionMs - hold.holdAtMs) > seekThreshold(HELD_SEEK_MS, accuracy) && cooled) {
      out.actions.push({ do: "seek", positionMs: hold.holdAtMs });
    }
    return out;
  }

  const end = endLimit(player.durationMs);
  const target = Math.max(0, roomPosition(room, input.durationMs, input.serverNowMs) + input.offsetMs);
  let nudging = input.nudging && player.canSetRate;
  if (end !== null && target >= end && player.positionMs >= end) {
    setRate(rate);
    return out;
  }
  if (player.buffering) {
    out.nudging = nudging;
    return out;
  }
  const drift = player.positionMs - target;
  const size = Math.abs(drift);
  let seek = false;
  if (player.canSetRate) {
    if (size > seekThreshold(SEEK_MS, accuracy)) {
      nudging = false;
      seek = cooled;
    } else if (nudging) {
      nudging = size >= NUDGE_EXIT_MS;
    } else if (size > DEADBAND_MS) {
      nudging = true;
    }
  } else if (size > seekThreshold(SEEK_ONLY_MS, accuracy)) {
    seek = cooled;
  }
  setRate(nudging ? rate * (drift > 0 ? 1 - NUDGE : 1 + NUDGE) : rate);
  if (seek) out.actions.push({ do: "seek", positionMs: clampLocal(target + SEEK_LEAD_MS, player.durationMs) });
  if (player.paused) out.actions.push({ do: "play" });
  out.nudging = nudging;
  return out;
}

// The end margin: seeks never go past the local file's duration − 1000 ms.
function endLimit(durationMs: number | null): number | null {
  return durationMs !== null && durationMs > 0 ? Math.max(0, durationMs - END_MARGIN_MS) : null;
}

function clampLocal(x: number, durationMs: number | null): number {
  const end = endLimit(durationMs);
  return end === null ? Math.max(0, x) : Math.min(Math.max(0, x), end);
}
