import { PartyError } from "./error.ts";
import { frozen, type Writable } from "./freeze.ts";
import { isObject } from "./relaylist.ts";
import { hasForbidden, wellFormed } from "./text.ts";
import type { Media, MediaFile, Series } from "./types.ts";

/** @internal The longest media: 7 days (SDK.md §9.7). */
export const MAX_DURATION_MS = 604_800_000;
const MAX_MEDIA_BYTES = 4096;
const MAX_EXT_BYTES = 1024;
const encoder = new TextEncoder();

/**
 * Null when the descriptor passes the send checks of SDK.md §9.7 (sizes measured on the SDK's compact JSON), else the
 * first rule it breaks. Apps trim `file.name` or `ext` and check again instead of getting `invalid_message`.
 */
export function checkMedia(media: unknown): string | null {
  const checked = sendable(media);
  return typeof checked === "string" ? checked : null;
}

/**
 * Decodes a descriptor with the receive checks of SDK.md §9.7, as the SDK checks one from the relay: the send-only size
 * limits don't apply. Returns a fresh frozen copy; unknown members outside `ext` are dropped. Throws `invalid_message`.
 */
export function parseMedia(value: unknown): Media {
  const checked = received(value);
  if (typeof checked === "string") throw new PartyError("invalid_message", `media: ${checked}`);
  return checked;
}

/** @internal The receive check of SDK.md §9.7 on a descriptor from the relay, and its frozen copy; null when it fails. */
export function receivedMedia(value: unknown): Media | null {
  const checked = received(value);
  return typeof checked === "string" ? null : checked;
}

/** @internal The send checks of SDK.md §9.7 on a descriptor for `setMedia`, and its frozen copy. Throws `invalid_message`. */
export function sendableMedia(value: unknown): Media {
  const checked = sendable(value);
  if (typeof checked === "string") throw new PartyError("invalid_message", `media: ${checked}`);
  return checked;
}

/** @internal Whether two descriptors are the same JSON value (member order doesn't matter). */
export function mediaEquals(a: Media, b: Media): boolean {
  try {
    return canonical(a) === canonical(b);
  } catch {
    // One is a received `ext` too deep to walk; the other was checked for sending, so it isn't that deep.
    return false;
  }
}

/**
 * The descriptor's frozen copy when it passes the receive checks, else the first rule it breaks. Sizes are skipped: the
 * relay measured them as received, and another encoding of the same value may differ. Never throws.
 */
function received(value: unknown): Media | string {
  try {
    return checkShape(value) ?? frozen(copyMedia(value as Record<string, unknown>));
  } catch {
    // Only an `ext` nested too deep to walk, or holding a cycle, gets here. It can't be the 1024 bytes a relay passes on.
    return "ext must be JSON that can be copied";
  }
}

/** The descriptor's frozen copy when it passes the send checks, else the first rule it breaks. */
function sendable(value: unknown): Media | string {
  try {
    const problem = checkShape(value);
    if (problem !== null) return problem;
    const media = copyMedia(value as Record<string, unknown>);
    return checkSize(media) ?? frozen(media);
  } catch {
    // Only an `ext` nested too deep to walk, or holding a cycle, gets here.
    return `ext must be at most ${MAX_EXT_BYTES} bytes of JSON`;
  }
}

/** Copies the known members of a descriptor that passed `checkShape`. Throws on an `ext` too deep to copy. */
function copyMedia(v: Record<string, unknown>): Media {
  const m: Writable<Media> = { kind: v["kind"] as Media["kind"], title: v["title"] as string, durationMs: v["durationMs"] as number };
  if (typeof v["year"] === "number") m.year = v["year"];
  if (isObject(v["ids"])) m.ids = copyIds(v["ids"]);
  if (isObject(v["series"])) {
    const s = v["series"];
    const series: Writable<Series> = { title: s["title"] as string };
    if (typeof s["year"] === "number") series.year = s["year"];
    if (isObject(s["ids"])) series.ids = copyIds(s["ids"]);
    m.series = series;
  }
  if (typeof v["season"] === "number") m.season = v["season"];
  if (typeof v["episode"] === "number") m.episode = v["episode"];
  if (isObject(v["file"])) {
    const f = v["file"];
    const file: Writable<MediaFile> = {};
    if (typeof f["sizeBytes"] === "number") file.sizeBytes = f["sizeBytes"];
    if (typeof f["oshash"] === "string") file.oshash = f["oshash"];
    if (typeof f["name"] === "string") file.name = f["name"];
    m.file = file;
  }
  if (isObject(v["ext"])) m.ext = JSON.parse(JSON.stringify(v["ext"])) as Record<string, unknown>;
  return m;
}

// Object.fromEntries defines own properties, so a `__proto__` namespace stays a member.
function copyIds(ids: Record<string, unknown>): Record<string, string> {
  return Object.fromEntries(Object.entries(ids).filter(([, id]) => typeof id === "string")) as Record<string, string>;
}

function canonical(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (isObject(v)) {
    const keys = Object.keys(v).filter((k) => v[k] !== undefined).sort();
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canonical(v[k])}`).join(",")}}`;
  }
  return JSON.stringify(v);
}

const int = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v);

function checkIds(ids: unknown, where: string): string | null {
  if (ids === undefined) return null;
  if (!isObject(ids)) return `${where} must be an object`;
  for (const [ns, id] of Object.entries(ids)) {
    if (typeof id !== "string" || id === "") return `${where}.${ns} must be a non-empty string`;
    if (!ns.isWellFormed() || !id.isWellFormed()) return `${where} must not hold unpaired surrogates`;
  }
  return null;
}

/**
 * Null when `v` is a non-empty string without forbidden characters (SPEC.md §2, §10) or unpaired surrogates (SDK.md
 * §7.1); else what is wrong with it.
 */
function checkLabel(v: unknown, where: string): string | null {
  if (typeof v !== "string" || v === "") return `${where} must be a non-empty string`;
  if (!v.isWellFormed()) return `${where} must not hold unpaired surrogates`;
  return hasForbidden(v) ? `${where} must not hold control or bidi control characters` : null;
}

/** The shape and value rules of SDK.md §9.7, on send and receipt alike. Throws on an `ext` too deep to walk. */
function checkShape(v: unknown): string | null {
  if (!isObject(v)) return "a descriptor is an object";
  const { kind, title, year, series, season, episode, durationMs, file, ext } = v;
  if (kind !== "movie" && kind !== "episode" && kind !== "video") return "kind must be movie, episode or video";
  const titleProblem = checkLabel(title, "title");
  if (titleProblem !== null) return titleProblem;
  if (year !== undefined && !int(year)) return "year must be an integer";
  const ids = checkIds(v["ids"], "ids");
  if (ids !== null) return ids;
  if (series !== undefined) {
    if (!isObject(series)) return "series must be an object";
    const seriesTitle = checkLabel(series["title"], "series.title");
    if (seriesTitle !== null) return seriesTitle;
    if (series["year"] !== undefined && !int(series["year"])) return "series.year must be an integer";
    const seriesIds = checkIds(series["ids"], "series.ids");
    if (seriesIds !== null) return seriesIds;
  } else if (kind === "episode") {
    return "an episode needs series";
  }
  if (season !== undefined && !(int(season) && season >= 0)) return "season must be an integer ≥ 0";
  if (episode !== undefined && !(int(episode) && episode >= 0)) return "episode must be an integer ≥ 0";
  if (!int(durationMs) || durationMs < 1 || durationMs > MAX_DURATION_MS) {
    return `durationMs must be an integer from 1 to ${MAX_DURATION_MS}`;
  }
  if (file !== undefined) {
    if (!isObject(file)) return "file must be an object";
    const { sizeBytes, oshash, name } = file;
    if (sizeBytes !== undefined && !(int(sizeBytes) && sizeBytes >= 0)) return "file.sizeBytes must be an integer ≥ 0";
    if (oshash !== undefined && !(typeof oshash === "string" && /^[0-9a-f]{16}$/.test(oshash))) {
      return "file.oshash must be 16 lower-case hex digits";
    }
    if (name !== undefined) {
      const nameProblem = checkLabel(name, "file.name");
      if (nameProblem !== null) return nameProblem;
      // A name is a matching hint, never a path (SPEC.md §10).
      if (/[/\\]/.test(name as string)) return "file.name must not hold / or \\";
      if (name === "." || name === "..") return "file.name must not be . or ..";
    }
  }
  if (ext !== undefined) {
    if (!isObject(ext)) return "ext must be an object";
    for (const key of Object.keys(ext)) {
      if (!/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(key)) return `ext member ${key} must be a reverse-DNS name`;
    }
    if (!wellFormed(ext)) return "ext must not hold unpaired surrogates";
  }
  return null;
}

function checkSize(m: Media): string | null {
  if (m.ext !== undefined && encoder.encode(JSON.stringify(m.ext)).length > MAX_EXT_BYTES) {
    return `ext must be at most ${MAX_EXT_BYTES} bytes`;
  }
  if (encoder.encode(JSON.stringify(m)).length > MAX_MEDIA_BYTES) return `a descriptor must be at most ${MAX_MEDIA_BYTES} bytes`;
  return null;
}
