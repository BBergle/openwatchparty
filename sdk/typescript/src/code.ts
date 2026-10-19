import { PartyError } from "./error.ts";
import type { Relay } from "./types.ts";

/** The 31 room-code symbols (SPEC.md §5.1). */
export const ALPHABET = "23456789ABCDEFGHJKMNPQRSTUVWXYZ";

// Spaces, line breaks, tabs, no-break spaces, hyphens, dashes and the minus sign (SDK.md §4.1).
const SEPARATORS = " \t\n\r\u00a0\u2007\u202f-\u2010\u2011\u2012\u2013\u2014\u2212";

/** Strips separators, upper-cases ASCII letters and checks for 6 symbols. Throws `invalid_code`. */
export function normalizeCode(input: string): string {
  let out = "";
  for (const ch of input) {
    if (SEPARATORS.includes(ch)) continue;
    const up = ch >= "a" && ch <= "z" ? ch.toUpperCase() : ch;
    if (up.length !== 1 || !ALPHABET.includes(up)) throw new PartyError("invalid_code", `not a room code: ${input}`);
    out += up;
  }
  if (out.length !== 6) throw new PartyError("invalid_code", `not a room code: ${input}`);
  return out;
}

/** A code in two groups for display: `K7M-Q2X`. Throws `invalid_code`. */
export function formatCode(code: string): string {
  const c = normalizeCode(code);
  return `${c.slice(0, 3)}-${c.slice(3)}`;
}

/** The relay whose prefixes hold the normalized code's first symbol, or null. */
export function routeCode<R extends Pick<Relay, "prefixes">>(code: string, relays: readonly R[]): R | null {
  const first = code[0];
  if (first === undefined) return null;
  return relays.find((r) => r.prefixes.includes(first)) ?? null;
}
