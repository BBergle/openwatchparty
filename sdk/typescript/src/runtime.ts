/**
 * Opens a WebSocket to a relay URL.
 *
 * Without one, the party uses the global `WebSocket`.
 */
export type Connect = (url: string) => WebSocket;

/** @internal Where sockets and local time come from; tests replace them. */
export interface Env {
  connect: Connect;
  /** Local monotonic time in integer milliseconds. */
  nowMs: () => number;
}

/** @internal The connect timeout of SDK.md §2: for opening a socket, and for the answer to create, join or resume. */
export const CONNECT_TIMEOUT_MS = 10_000;

/** @internal A `setTimeout` handle: a number in browsers, an object in Node. */
export type TimerHandle = ReturnType<typeof setTimeout>;

/** @internal The longest delay `setTimeout` honours (2^31 − 1 ms); a longer one fires at once (SDK.md §2). */
export const MAX_TIMER_MS = 2_147_483_647;

/**
 * @internal `setTimeout` with the delay clamped to [0, MAX_TIMER_MS] (SDK.md §2). A caller whose delay may be longer
 * works out what is left when it fires and starts again.
 */
export function later(run: () => void, delayMs: number): TimerHandle {
  return setTimeout(run, Math.min(Math.max(0, delayMs), MAX_TIMER_MS));
}
