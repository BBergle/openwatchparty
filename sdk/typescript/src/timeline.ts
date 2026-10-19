import type { Phase } from "./types.ts";

/** The snapshot fields the timeline reads. */
export interface Timeline {
  phase: Phase;
  positionMs: number;
  atMs: number;
  rate: number;
}

/** The room position at relay time `t` (SPEC.md §12.2), capped at `durationMs`. */
export function roomPosition(room: Timeline, durationMs: number, t: number): number {
  if (room.phase !== "playing") return room.positionMs;
  const moved = Math.max(0, t - room.atMs) * room.rate;
  return Math.min(Math.floor(room.positionMs + moved), durationMs);
}

/** Whether the room's position moves at relay time `t`. */
export function isRunning(room: Timeline, t: number): boolean {
  return room.phase === "playing" && t >= room.atMs;
}
