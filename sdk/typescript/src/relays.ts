import { ALPHABET, normalizeCode, routeCode } from "./code.ts";
import { PartyError } from "./error.ts";
import { frozen } from "./freeze.ts";
import { PUBLIC_RELAYS } from "./public-relays.ts";
import { parseRelayList, validUrl } from "./relaylist.ts";
import type { Relay } from "./types.ts";

export interface RelaysOptions {
  /**
   * Fetches a URL and returns the body (SDK.md §3.4). It must stop when `signal` aborts (5 s after the start), must
   * not follow redirects and must fail on any status other than 200. Defaults to `fetch` with `redirect: "error"`.
   */
  httpGet?: (url: string, signal: AbortSignal) => Promise<string>;
}

type HttpGet = (url: string, signal: AbortSignal) => Promise<string>;

// The only source of the public list (SDK.md §3.1).
const PUBLIC_SOURCE = "https://openwatch.party/relays.json";
const HTTP_TIMEOUT_MS = 5000;
const MAX_BODY_BYTES = 65_536;
const REFRESH_AFTER_MS = 24 * 60 * 60 * 1000;
const encoder = new TextEncoder();

/**
 * One relay list and its refresh schedule (SDK.md §3). Keep one per app and pass it to every party:
 * `Relays.public()` for the public relays, `Relays.at(baseUrl)` for one self-hosted relay.
 */
export class Relays {
  /** Null for `Relays.at`, which never fetches. */
  readonly #httpGet: HttpGet | null;
  #list: readonly Relay[];
  #lastRefresh: number | null = null;
  #background = false;

  private constructor(list: readonly Relay[], httpGet: HttpGet | null) {
    this.#list = list;
    this.#httpGet = httpGet;
  }

  /** The public relays: the list compiled into the SDK, refreshed from `https://openwatch.party/relays.json` only. */
  static public(options: RelaysOptions = {}): Relays {
    return new Relays(parseRelayList(PUBLIC_RELAYS)!, options.httpGet ?? fetchText);
  }

  /**
   * One self-hosted relay by its base URL (`https://relay.example.com`, `http://192.168.1.5:8080/owp`), for everything:
   * no list is ever fetched. Throws `invalid_message` for a base URL that can't be one.
   */
  static at(baseUrl: string): Relays {
    const base = baseUrl.replace(/\/+$/, "");
    const secure = base.startsWith("https://");
    if (!/^https?:\/\/[^/?#]/.test(base)) throw new PartyError("invalid_message", `not an http(s) base URL: ${baseUrl}`);
    const url = (secure ? "wss://" + base.slice(8) : "ws://" + base.slice(7)) + "/v1/ws";
    if (!validUrl(url, !secure)) throw new PartyError("invalid_message", `not a relay base URL: ${baseUrl}`);
    return new Relays(frozen([{ id: "custom", name: base, url, prefixes: ALPHABET }]), null);
  }

  /** The list in use, frozen. */
  get list(): readonly Relay[] {
    return this.#list;
  }

  /** The relay that owns a code; null for an invalid code or a symbol nobody owns. */
  relayFor(code: string): Relay | null {
    try {
      return routeCode(normalizeCode(code), this.#list);
    } catch {
      return null;
    }
  }

  /**
   * A forced refresh: one GET of the public source. False when it gave no valid list; the list then stays. On
   * `Relays.at` it does nothing and returns true.
   */
  async refresh(): Promise<boolean> {
    const httpGet = this.#httpGet;
    if (httpGet === null) return true;
    let list: Relay[] | null;
    try {
      list = parseRelayList(JSON.parse(await get(httpGet, PUBLIC_SOURCE)));
    } catch {
      return false;
    }
    if (list === null) return false;
    this.#list = list;
    this.#lastRefresh = performance.now();
    return true;
  }

  /** @internal A party connected: refresh in the background once per process, then daily. */
  connected(): void {
    const due = this.#lastRefresh === null || performance.now() - this.#lastRefresh >= REFRESH_AFTER_MS;
    if (this.#httpGet === null || !due || this.#background) return;
    this.#background = true;
    void this.refresh().finally(() => (this.#background = false));
  }
}

/** One GET by SDK.md §3.4: aborted after 5 s, at most 65536 bytes of body. Throws on any failure. */
async function get(httpGet: HttpGet, url: string): Promise<string> {
  const abort = new AbortController();
  const reason = new Error(`no answer from ${url} in ${HTTP_TIMEOUT_MS} ms`);
  const timer = setTimeout(() => abort.abort(reason), HTTP_TIMEOUT_MS);
  const { promise: aborted, reject } = Promise.withResolvers<never>();
  abort.signal.addEventListener("abort", () => reject(abort.signal.reason), { once: true });
  try {
    // The race gives up at the deadline even when an app's fetcher ignores the signal.
    const body = await Promise.race([httpGet(url, abort.signal), aborted]);
    if (typeof body !== "string") throw new Error(`no body from ${url}`);
    if (body.length > MAX_BODY_BYTES || encoder.encode(body).length > MAX_BODY_BYTES) {
      throw new Error(`the answer from ${url} is over ${MAX_BODY_BYTES} bytes`);
    }
    return body;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * @internal The default fetcher: `fetch` without redirects, the body read as a stream up to the limit, decoded as
 * UTF-8.
 */
export async function fetchText(url: string, signal: AbortSignal): Promise<string> {
  const res = await fetch(url, { signal, redirect: "error" });
  if (res.status !== 200 || res.body === null) {
    await res.body?.cancel();
    throw new Error(`HTTP ${res.status} from ${url}`);
  }
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_BODY_BYTES) {
      await reader.cancel();
      throw new Error(`the answer from ${url} is over ${MAX_BODY_BYTES} bytes`);
    }
    chunks.push(value);
  }
  const body = new Uint8Array(size);
  let at = 0;
  for (const chunk of chunks) {
    body.set(chunk, at);
    at += chunk.byteLength;
  }
  return new TextDecoder("utf-8", { fatal: true }).decode(body);
}
