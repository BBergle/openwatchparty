/** A character of general category Cc or a Bidi_Control character (SPEC.md §2). */
function forbidden(cp: number): boolean {
  return (
    cp <= 0x1f ||
    (cp >= 0x7f && cp <= 0x9f) ||
    cp === 0x061c ||
    cp === 0x200e ||
    cp === 0x200f ||
    (cp >= 0x202a && cp <= 0x202e) ||
    (cp >= 0x2066 && cp <= 0x2069)
  );
}

/** Whether `s` holds a forbidden character (SPEC.md §2). */
export function hasForbidden(s: string): boolean {
  for (const ch of s) if (forbidden(ch.codePointAt(0)!)) return true;
  return false;
}

/**
 * Null when `s` is a string of 1 to `max` code points without forbidden characters (SPEC.md §2) or unpaired surrogates
 * (SDK.md §7.1); else what is wrong with it.
 */
export function checkText(s: unknown, max: number): string | null {
  if (typeof s !== "string") return "must be a string";
  if (!s.isWellFormed()) return "must not hold unpaired surrogates";
  let n = 0;
  for (const ch of s) {
    if (forbidden(ch.codePointAt(0)!)) return "must not hold control or bidi control characters";
    n++;
  }
  return n >= 1 && n <= max ? null : `must be 1–${max} characters`;
}

/** Null when `client` is `<app>/<version>` by SPEC.md §6.3; else what is wrong with it. */
export function checkClient(client: unknown): string | null {
  const problem = checkText(client, 64);
  if (problem !== null) return problem;
  const slash = (client as string).indexOf("/");
  return slash >= 1 && slash < (client as string).length - 1 ? null : "must be <app>/<version>";
}

/** Whether every string in a JSON value, member names included, is well-formed UTF-16. */
export function wellFormed(v: unknown): boolean {
  if (typeof v === "string") return v.isWellFormed();
  if (Array.isArray(v)) return v.every(wellFormed);
  if (typeof v === "object" && v !== null) {
    return Object.entries(v).every(([k, x]) => k.isWellFormed() && wellFormed(x));
  }
  return true;
}
