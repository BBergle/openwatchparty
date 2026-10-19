import { MAX_DURATION_MS } from "./media.ts";
import type { Media, MediaFile } from "./types.ts";

/** The v1 `ids` namespaces. IDs in any other namespace don't count for matching. */
const NAMESPACES = ["imdb", "tmdb", "tvdb", "plex", "anilist", "anidb", "kitsu", "mal"];

/** The largest duration difference that is still `matched`. */
export const MATCH_TOLERANCE_MS = 1500;
const MAX_PROBES = 4;
const PROBE_BUDGET_MS = 60_000;

/** File hints a candidate carries for ranking. */
export interface Hints {
  durationMs?: number | null | undefined;
  sizeBytes?: number | null | undefined;
  oshash?: string | null | undefined;
  name?: string | null | undefined;
  probe?: unknown;
}

function sharesId(a: Record<string, string> | undefined, b: Record<string, string> | undefined): boolean {
  if (!a || !b) return false;
  return NAMESPACES.some((ns) => typeof a[ns] === "string" && a[ns] !== "" && a[ns] === b[ns]);
}

// Some v1 namespace holds different non-empty values on the two sides.
function differsId(a: Record<string, string> | undefined, b: Record<string, string> | undefined): boolean {
  if (!a || !b) return false;
  return NAMESPACES.some(
    (ns) => typeof a[ns] === "string" && a[ns] !== "" && typeof b[ns] === "string" && b[ns] !== "" && a[ns] !== b[ns],
  );
}

// Both non-empty and equal after mapping ASCII A–Z to a–z.
function foldEqual(a: string | null | undefined, b: string | null | undefined): boolean {
  return !!a && !!b && a.replace(/[A-Z]/g, (c) => c.toLowerCase()) === b.replace(/[A-Z]/g, (c) => c.toLowerCase());
}

function sameSize(a: number | null | undefined, b: number | null | undefined): boolean {
  return typeof a === "number" && a === b;
}

// The `video` rule of sameTitle: equal oshash, or equal size and name.
function sameFile(a: MediaFile | undefined, b: MediaFile | undefined): boolean {
  if (!a || !b) return false;
  return foldEqual(a.oshash, b.oshash) || (sameSize(a.sizeBytes, b.sizeBytes) && foldEqual(a.name, b.name));
}

/**
 * Whether a candidate's `durationMs` or a probe's answer is a duration (SDK.md §9.2): an integer from 1 to 604800000.
 * Any other value is no duration.
 */
export function durationCounts(d: unknown): d is number {
  return Number.isSafeInteger(d) && (d as number) >= 1 && (d as number) <= MAX_DURATION_MS;
}

/** Whether two descriptors name the same title (SDK.md §9.3). */
export function sameTitle(a: Media, b: Media): boolean {
  if (a.kind !== b.kind) return false;
  switch (a.kind) {
    case "movie":
      return sharesId(a.ids, b.ids);
    case "episode":
      if (sharesId(a.ids, b.ids)) return true;
      // Differing episode IDs beat equal numbers: the two sides may number the series differently.
      return (
        !differsId(a.ids, b.ids) &&
        a.series !== undefined &&
        b.series !== undefined &&
        sharesId(a.series.ids, b.series.ids) &&
        typeof a.season === "number" &&
        a.season === b.season &&
        typeof a.episode === "number" &&
        a.episode === b.episode
      );
    case "video":
      return sameFile(a.file, b.file);
    default:
      return false;
  }
}

/**
 * Orders candidates for every kind (SDK.md §9.4): equal oshash, equal size, equal name, known duration by smallest
 * difference, unknown duration. Ties keep the resolver's order. The resolver decides identity; the SDK drops none.
 */
export function rankCandidates<C extends Hints>(media: Media, candidates: readonly C[]): C[] {
  const file: MediaFile = media.file ?? {};
  const key = (c: C): number[] => {
    const d = c.durationMs;
    const known = durationCounts(d);
    return [
      foldEqual(c.oshash, file.oshash) ? 0 : 1,
      sameSize(c.sizeBytes, file.sizeBytes) ? 0 : 1,
      foldEqual(c.name, file.name) ? 0 : 1,
      known ? 0 : 1,
      known ? Math.abs(d - media.durationMs) : 0,
    ];
  };
  const keyed = candidates.map((c) => ({ c, k: key(c) }));
  // Array.prototype.sort is stable.
  keyed.sort((x, y) => {
    for (let i = 0; i < x.k.length; i++) {
      const d = (x.k[i] ?? 0) - (y.k[i] ?? 0);
      if (d !== 0) return d;
    }
    return 0;
  });
  return keyed.map((e) => e.c);
}

/**
 * A probe's outcome: what it answered (null when the file didn't open) and the time taken, or a missed deadline. An
 * answer that isn't a duration (`durationCounts`) is a failed probe.
 */
export type ProbeOutcome = { durationMs: number | null; tookMs: number } | "missed";

/** Runs one probe with a deadline of `deadlineMs`. */
export type ProbeRunner<C> = (candidate: C, deadlineMs: number) => Promise<ProbeOutcome>;

export interface WalkResult<C> {
  /** Probes that finished within their deadline. */
  probed: C[];
  result: "matched" | "mismatch" | "unavailable";
  choice: C | null;
  deltaMs: number | null;
}

/**
 * The matching walk over ranked candidates (SDK.md §9.5). `learned` hears each candidate's delta as it becomes known,
 * by index in `ranked`.
 */
export async function walkCandidates<C extends Hints>(
  media: Media,
  ranked: readonly C[],
  runProbe: ProbeRunner<C>,
  learned?: (index: number, deltaMs: number) => void,
): Promise<WalkResult<C>> {
  const out: WalkResult<C> = { probed: [], result: "unavailable", choice: null, deltaMs: null };
  let probes = 0;
  let spent = 0;
  let budgetGone = false;
  for (const [index, c] of ranked.entries()) {
    let duration: number | null = null;
    if (durationCounts(c.durationMs)) {
      duration = c.durationMs;
    } else if (c.probe !== undefined && c.probe !== null) {
      if (probes === MAX_PROBES || budgetGone) continue;
      const outcome = await runProbe(c, PROBE_BUDGET_MS - spent);
      if (outcome === "missed") {
        budgetGone = true;
        continue;
      }
      spent += outcome.tookMs;
      probes++;
      out.probed.push(c);
      duration = durationCounts(outcome.durationMs) ? outcome.durationMs : null;
    }
    if (duration === null) continue;
    const delta = duration - media.durationMs;
    learned?.(index, delta);
    if (Math.abs(delta) <= MATCH_TOLERANCE_MS) {
      return { ...out, result: "matched", choice: c, deltaMs: delta };
    }
    if (out.deltaMs === null || Math.abs(delta) < Math.abs(out.deltaMs)) {
      out.result = "mismatch";
      out.choice = c;
      out.deltaMs = delta;
    }
  }
  return out;
}
