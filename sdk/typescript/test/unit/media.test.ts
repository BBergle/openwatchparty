import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";
import { formatCode } from "../../src/code.ts";
import { PartyError } from "../../src/error.ts";
import { checkMedia, mediaEquals, parseMedia } from "../../src/media.ts";
import type { Media } from "../../src/types.ts";
import { REPO } from "../paths.ts";

const EXAMPLES = `${REPO}spec/examples/`;

// Every descriptor example of spec/examples/, alone or as the `media` of a setMedia. Examples that break a rule of the
// JSON text (duplicate keys, invalid UTF-8) are left out: a parsed object can't carry those faults.
function descriptors(dir: "valid" | "invalid"): [string, unknown][] {
  const out: [string, unknown][] = [];
  for (const f of readdirSync(EXAMPLES + dir)) {
    if (!/^(descriptor|setMedia)\./.test(f) || /duplicate|invalid-utf8/.test(f)) continue;
    const doc = JSON.parse(readFileSync(`${EXAMPLES}${dir}/${f}`, "utf8"));
    if (f.startsWith("descriptor.")) out.push([f, doc]);
    else if ("media" in doc) out.push([f, doc.media]);
  }
  return out;
}

for (const [file, media] of descriptors("valid")) {
  test(`checkMedia accepts ${file}`, () => assert.equal(checkMedia(media), null));
}

for (const [file, media] of descriptors("invalid")) {
  test(`checkMedia refuses ${file}`, () => assert.notEqual(checkMedia(media), null));
}

const base: Media = { kind: "video", title: "Clip", durationMs: 1000 };

test("ext is limited to 1024 bytes and the descriptor to 4096, measured as compact UTF-8 JSON", () => {
  // {"a.b":"…"} is 10 bytes around the value; é is 2 bytes in UTF-8.
  const ext = (n: number) => ({ "a.b": "é".repeat(n / 2) });
  assert.equal(checkMedia({ ...base, ext: ext(1014) }), null);
  assert.match(checkMedia({ ...base, ext: ext(1016) }) ?? "", /ext/);
  const titleFor = (bytes: number) => "x".repeat(bytes - JSON.stringify({ ...base, title: "" }).length);
  assert.equal(checkMedia({ ...base, title: titleFor(4096) }), null);
  assert.match(checkMedia({ ...base, title: titleFor(4097) }) ?? "", /4096/);
});

test("parseMedia keeps unknown ids namespaces and ext, drops other unknown members", () => {
  const parsed = parseMedia({
    ...base,
    ids: JSON.parse(`{"imdb": "tt1", "x-own": "42", "__proto__": "7"}`),
    ext: { "app.example": { deep: [1, { a: null }] } },
    rating: 5,
    file: { name: "clip.mp4", hash: "nope" },
  });
  assert.deepStrictEqual(parsed, {
    ...base,
    ids: JSON.parse(`{"imdb": "tt1", "x-own": "42", "__proto__": "7"}`),
    ext: { "app.example": { deep: [1, { a: null }] } },
    file: { name: "clip.mp4" },
  });
  assert.throws(() => parseMedia({ ...base, durationMs: 0 }), { code: "invalid_message" });
});

test("parseMedia applies the receive checks: sizes don't count, every other rule fails with invalid_message", () => {
  const big = { ...base, title: "x".repeat(5000), ext: { "a.b": { note: "y".repeat(2000) } } };
  assert.notEqual(checkMedia(big), null);
  const parsed = parseMedia(big);
  assert.deepStrictEqual(parsed, big);
  assert.ok(Object.isFrozen(parsed) && Object.isFrozen(parsed.ext) && Object.isFrozen(parsed.ext!["a.b"]));
  assert.ok(!Object.isFrozen(big.ext["a.b"]), "the input stays the app's");
  for (const bad of [null, [], { ...base, kind: "show" }, { ...base, title: "" }, { ...base, title: "\uD83D" }, { ...base, durationMs: 604_800_001 }]) {
    assert.throws(() => parseMedia(bad), (e: unknown) => e instanceof PartyError && e.code === "invalid_message", JSON.stringify(bad));
  }
});

test("descriptors compare as JSON values, member order ignored", () => {
  const a: Media = { kind: "movie", title: "Heat", year: 1995, ids: { imdb: "tt0113277", tmdb: "949" }, durationMs: 1 };
  const b: Media = { durationMs: 1, ids: { tmdb: "949", imdb: "tt0113277" }, year: 1995, title: "Heat", kind: "movie" };
  assert.ok(mediaEquals(a, b));
  assert.ok(!mediaEquals(a, { ...b, year: 1996 }));
});

test("formatCode shows a code in two groups", () => {
  assert.equal(formatCode(" k7mq2x "), "K7M-Q2X");
  assert.throws(() => formatCode("K7MQ2"), { code: "invalid_code" });
});

test("checkMedia refuses unpaired surrogates anywhere in the descriptor", () => {
  assert.match(checkMedia({ ...base, title: "Clip \uD83D" }) ?? "", /surrogate/);
  assert.match(checkMedia({ ...base, ext: { "app.example": { note: ["ok", "\uDE00"] } } }) ?? "", /surrogate/);
  assert.match(checkMedia({ ...base, ext: { "app.example": { "\uD800": 1 } } }) ?? "", /surrogate/);
  assert.equal(checkMedia({ ...base, title: "Clip \uD83D\uDE00" }), null);
});
