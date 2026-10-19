import { Clock } from "./clock.ts";
import { PartyError } from "./error.ts";
import { isObject } from "./relaylist.ts";
import { CONNECT_TIMEOUT_MS, later, type Env, type TimerHandle } from "./runtime.ts";

const SILENCE_MS = 15_000;
const BURST = 3;
const BURST_SPACING_MS = 500;
const PING_INTERVAL_MS = 5000;

/**
 * @internal One WebSocket to a relay with its own clock (SDK.md §5): opened within the connect timeout, pings at open,
 * +500 and +1000, then every 5000 ms from open; closed with status 1000 after 15 s without any message (SDK.md §14).
 * `onClose` runs once when the link fails, the relay closes it or it falls silent; `close()` ends it without calling
 * `onClose`.
 */
export class Link {
  readonly url: string;
  readonly clock = new Clock();
  /** Resolves when the socket opens; rejects with `unreachable` when it fails or takes longer than the connect timeout. */
  readonly opened: Promise<void>;
  onMessage: (msg: Record<string, unknown>) => void = () => {};
  onClose: (code: number | null) => void = () => {};
  onPong: () => void = () => {};

  readonly #env: Env;
  #ws: WebSocket | null = null;
  #openedAt = 0;
  #pings = 0;
  #done = false;
  #pingTimer: TimerHandle | undefined;
  #silenceTimer: TimerHandle | undefined;
  #open!: () => void;
  #fail!: (e: PartyError) => void;

  constructor(url: string, env: Env) {
    this.url = url;
    this.#env = env;
    const opened = Promise.withResolvers<void>();
    this.opened = opened.promise;
    this.#open = opened.resolve;
    this.#fail = opened.reject;
    this.opened.catch(() => {});
    const openTimer = setTimeout(() => this.#finish(null), CONNECT_TIMEOUT_MS);
    let ws: WebSocket;
    try {
      ws = env.connect(url);
    } catch {
      clearTimeout(openTimer);
      queueMicrotask(() => this.#finish(null));
      return;
    }
    this.#ws = ws;
    ws.onopen = () => {
      clearTimeout(openTimer);
      if (this.#done) return;
      this.#openedAt = env.nowMs();
      this.#armSilence();
      this.#ping();
      this.#open();
    };
    ws.onmessage = (ev: MessageEvent) => this.#receive(ev.data);
    ws.onclose = (ev: CloseEvent) => {
      clearTimeout(openTimer);
      this.#finish(ev.code);
    };
    ws.onerror = () => {
      if (ws.readyState !== WebSocket.OPEN) {
        clearTimeout(openTimer);
        this.#finish(null);
      }
    };
  }

  get isOpen(): boolean {
    return !this.#done && this.#ws !== null && this.#ws.readyState === WebSocket.OPEN;
  }

  /** Sends one message; false when the socket isn't open. */
  send(msg: Record<string, unknown>): boolean {
    if (!this.isOpen) return false;
    this.#ws!.send(JSON.stringify(msg));
    return true;
  }

  /** Closes the socket without calling `onClose`. */
  close(code = 1000): void {
    if (this.#done) return;
    this.#stop();
    try {
      this.#ws?.close(code);
    } catch {
      // Closing a socket that never opened may throw; it is gone either way.
    }
  }

  #stop(): void {
    this.#done = true;
    clearTimeout(this.#pingTimer);
    clearTimeout(this.#silenceTimer);
    this.#fail(new PartyError("unreachable", `could not connect to ${this.url}`));
  }

  #finish(code: number | null): void {
    if (this.#done) return;
    this.#stop();
    try {
      this.#ws?.close(1000);
    } catch {
      // Already closing.
    }
    this.onClose(code);
  }

  #armSilence(): void {
    clearTimeout(this.#silenceTimer);
    this.#silenceTimer = setTimeout(() => this.#finish(null), SILENCE_MS);
  }

  #ping(): void {
    if (this.#done) return;
    const id = ++this.#pings;
    this.clock.ping(id, this.#env.nowMs());
    this.send({ type: "ping", id });
    const n = this.#pings;
    const due = n < BURST ? n * BURST_SPACING_MS : (n - BURST + 1) * PING_INTERVAL_MS;
    this.#pingTimer = later(() => this.#ping(), this.#openedAt + due - this.#env.nowMs());
  }

  #receive(data: unknown): void {
    if (this.#done || typeof data !== "string") return;
    this.#armSilence();
    let msg: unknown;
    try {
      msg = JSON.parse(data);
    } catch {
      return;
    }
    if (!isObject(msg) || typeof msg["type"] !== "string") return;
    if (msg["type"] === "pong") {
      const { id, serverTime } = msg;
      if ((typeof id !== "number" && typeof id !== "string") || typeof serverTime !== "number") return;
      if (!Number.isSafeInteger(serverTime) || serverTime < 0) return;
      this.clock.pong(id, serverTime, this.#env.nowMs());
      this.onPong();
      return;
    }
    this.onMessage(msg);
  }
}
