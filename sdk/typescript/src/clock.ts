/** What happened to a `pong` (SDK.md §5). */
export type PongResult = "accepted" | "stepped" | "ignored";

/** The best clock sample: relay time ≈ local time + `offsetMs`. */
export interface ClockSample {
  offsetMs: number;
  rttMs: number;
}

const WINDOW = 8;
const MAX_RTT_MS = 1000;
const STEP_SLACK_MS = 100;
const FORGET_MS = 10_000;

/** One connection's estimate of the relay's clock from ping/pong pairs (SDK.md §5). */
export class Clock {
  readonly #pending = new Map<number | string, number>();
  #samples: ClockSample[] = [];

  /** Records a ping sent at local time `atMs`, and forgets pings unanswered for 10 s. */
  ping(id: number | string, atMs: number): void {
    for (const [pid, sentAt] of this.#pending) if (atMs - sentAt > FORGET_MS) this.#pending.delete(pid);
    this.#pending.set(id, atMs);
  }

  /** Feeds a pong received at local time `atMs`. */
  pong(id: number | string, serverTime: number, atMs: number): PongResult {
    const sentAt = this.#pending.get(id);
    if (sentAt === undefined) return "ignored";
    this.#pending.delete(id);
    const rtt = atMs - sentAt;
    if (rtt < 0 || rtt > MAX_RTT_MS) return "ignored";
    const offset = serverTime - (sentAt + Math.floor(rtt / 2));
    let result: PongResult = "accepted";
    const best = this.best;
    if (best !== null && Math.abs(offset - best.offsetMs) > Math.floor((rtt + best.rttMs) / 2) + STEP_SLACK_MS) {
      this.#samples = [];
      result = "stepped";
    }
    this.#samples.push({ offsetMs: offset, rttMs: rtt });
    if (this.#samples.length > WINDOW) this.#samples.shift();
    return result;
  }

  /** The sample with the lowest round trip (the oldest on a tie), or null before the first. */
  get best(): ClockSample | null {
    let best: ClockSample | null = null;
    for (const s of this.#samples) if (best === null || s.rttMs < best.rttMs) best = s;
    return best;
  }
}
