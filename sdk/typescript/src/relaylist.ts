import { ALPHABET } from "./code.ts";
import { frozen } from "./freeze.ts";
import { checkText } from "./text.ts";
import type { Relay } from "./types.ts";

/**
 * Reads the `relays` of a public relay list (the embedded copy or a refresh's answer) and checks them against SPEC.md
 * §5.2 and SDK.md §3.2. Returns the list frozen, or null when it is invalid; an invalid list is rejected whole. Unknown
 * members are ignored.
 */
export function parseRelayList(doc: unknown): Relay[] | null {
  if (!isObject(doc)) return null;
  const items = doc["relays"];
  if (!Array.isArray(items) || items.length === 0) return null;
  const ids = new Set<string>();
  const owned = new Set<string>();
  const out: Relay[] = [];
  for (const item of items) {
    if (!isObject(item)) return null;
    const { id, name, url, prefixes } = item;
    // 1–32 characters of a–z, 0–9 and -, not starting with -.
    if (typeof id !== "string" || !/^[a-z0-9][a-z0-9-]{0,31}$/.test(id) || ids.has(id)) return null;
    ids.add(id);
    if (checkText(name, 64) !== null) return null;
    if (typeof url !== "string" || !validUrl(url, false)) return null;
    if (typeof prefixes !== "string" || prefixes === "") return null;
    for (const c of prefixes) {
      if (c.length !== 1 || !ALPHABET.includes(c) || owned.has(c)) return null;
      owned.add(c);
    }
    out.push({ id, name: name as string, url, prefixes });
  }
  return frozen(out);
}

/**
 * @internal A relay `url` by SDK.md §3.2: `wss://` (or `ws://` when allowed), a non-empty host, a path ending in
 * `/v1/ws`, and only printable ASCII other than `?` and `#`.
 */
export function validUrl(url: string, allowWs: boolean): boolean {
  const rest = url.startsWith("wss://") ? url.slice(6) : allowWs && url.startsWith("ws://") ? url.slice(5) : null;
  if (rest === null || !/^[\x21-\x7e]*$/.test(rest) || /[?#]/.test(rest)) return false;
  return rest.endsWith("/v1/ws") && rest.indexOf("/") > 0;
}

/** @internal */
export function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}
