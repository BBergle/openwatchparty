import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer, type ServerResponse } from "node:http";
import { mock, test } from "node:test";
import { setImmediate as turn } from "node:timers/promises";
import { PUBLIC_RELAYS } from "../../src/public-relays.ts";
import { fetchText, Relays } from "../../src/relays.ts";
import { REPO } from "../paths.ts";

const SOURCE = "https://openwatch.party/relays.json";
const ALL = "23456789ABCDEFGHJKMNPQRSTUVWXYZ";
// A public list holds wss:// URLs only.
const PUB = { relays: [{ id: "x", name: "X", url: "wss://x.example/v1/ws", prefixes: ALL }] };
const WS = { relays: [{ id: "home", name: "Home", url: "ws://192.168.1.5:8080/owp/v1/ws", prefixes: ALL }] };

test("the embedded relay list equals relays.json at the repository root", () => {
  const canonical = JSON.parse(readFileSync(`${REPO}relays.json`, "utf8"));
  assert.deepStrictEqual(PUBLIC_RELAYS, canonical);
  assert.deepStrictEqual(Relays.public().list, canonical.relays);
});

test("relayFor normalizes and routes; null for an invalid code or a symbol no relay owns", () => {
  const relays = Relays.public();
  assert.equal(relays.relayFor("3 4 5 6 7 8")?.id, "na");
  assert.equal(relays.relayFor("9abcde")?.id, "eu");
  assert.equal(relays.relayFor("k7m-q2x")?.id, "asia");
  assert.equal(relays.relayFor("q2x\u20137mk"), null);
  assert.equal(relays.relayFor("K7MQ2"), null);
  assert.equal(relays.relayFor("K7MQ2I"), null);
});

test("Relays.at is one relay for every code; it fetches nothing, and its refresh does nothing and returns true", async () => {
  const fetched: unknown[] = [];
  const realFetch = globalThis.fetch;
  globalThis.fetch = (async (input: unknown) => {
    fetched.push(input);
    throw new Error("no network in this test");
  }) as typeof fetch;
  try {
    for (const [base, name, url] of [
      ["https://relay.example.com/", "https://relay.example.com", "wss://relay.example.com/v1/ws"],
      ["http://192.168.1.5:8080/owp//", "http://192.168.1.5:8080/owp", "ws://192.168.1.5:8080/owp/v1/ws"],
    ] as const) {
      const relays = Relays.at(base);
      const relay = { id: "custom", name, url, prefixes: ALL };
      assert.deepStrictEqual(relays.list, [relay], base);
      for (const code of ["2aa-aaa", "QRS-TUV", "k7m q2x"]) assert.deepStrictEqual(relays.relayFor(code), relay, code);
      assert.equal(relays.relayFor("K7M-Q2"), null);
      assert.equal(await relays.refresh(), true);
      relays.connected();
      await turn();
      assert.deepStrictEqual(relays.list, [relay]);
    }
    assert.deepEqual(fetched, []);
  } finally {
    globalThis.fetch = realFetch;
  }
});

test("Relays.at takes https:// or http:// in lower case followed by a host, else throws invalid_message", () => {
  for (const base of [
    "wss://relay.example.com/v1/ws",
    "HTTPS://relay.example.com",
    "Http://relay.example.com",
    "https://",
    "https:///owp",
    "https://?x",
    "https://#x",
    "http://relay.example.com?x",
    "https://relay.example.com/a b",
    "https://caf\u00e9.example",
    "relay.example.com",
    "",
  ]) {
    assert.throws(() => Relays.at(base), { code: "invalid_message" }, base);
  }
});

test("the public list refreshes from openwatch.party/relays.json only, once per process as parties connect", async () => {
  const asked: string[] = [];
  const relays = Relays.public({
    httpGet: async (url) => {
      asked.push(url);
      return JSON.stringify(PUB);
    },
  });
  relays.connected();
  relays.connected();
  await turn();
  assert.deepEqual(asked, [SOURCE]);
  assert.deepEqual(relays.list, PUB.relays);
  relays.connected();
  await turn();
  assert.deepEqual(asked, [SOURCE], "the next background refresh is due after 24 hours");
  assert.equal(await relays.refresh(), true);
  assert.deepEqual(asked, [SOURCE, SOURCE]);
});

test("a refresh that fails or gets an invalid list, ws:// URLs included, changes nothing", async () => {
  for (const answer of [JSON.stringify({ relays: [{ id: "x" }] }), JSON.stringify(WS), "{", "null"]) {
    const relays = Relays.public({ httpGet: async () => answer });
    assert.equal(await relays.refresh(), false, answer);
    assert.deepStrictEqual(relays.list, PUBLIC_RELAYS.relays);
  }
});

test("an app's fetcher gets a signal that aborts after 5 s; the refresh fails then even if it ignores it", async () => {
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    let signal: AbortSignal | undefined;
    const relays = Relays.public({
      httpGet: (_url, s) => {
        signal = s;
        return Promise.withResolvers<string>().promise;
      },
    });
    const refreshing = relays.refresh();
    await turn();
    mock.timers.tick(4999);
    assert.equal(signal?.aborted, false);
    mock.timers.tick(1);
    assert.equal(signal?.aborted, true);
    assert.equal(await refreshing, false);
  } finally {
    mock.timers.reset();
  }
});

test("an answer over 65536 bytes of UTF-8 fails", async () => {
  const text = JSON.stringify(PUB);
  const sized = (bytes: number) => `${text.slice(0, -1)}${" ".repeat(bytes - text.length)}}`;
  assert.equal(await Relays.public({ httpGet: async () => sized(65_536) }).refresh(), true);
  assert.equal(await Relays.public({ httpGet: async () => sized(65_537) }).refresh(), false);
  // Same length in UTF-16, one byte more in UTF-8.
  const wide = sized(65_536).replace('"name":"X"', '"name":"\u00e9"');
  assert.equal(await Relays.public({ httpGet: async () => wide }).refresh(), false);
});

test("the default fetcher follows no redirects, needs status 200 and UTF-8, and reads at most 65536 bytes", async () => {
  // SdkTs owns ports 18200–18249; the integration tests start from the bottom of the range.
  const port = Number(process.env["OWP_TEST_PORT_BASE"] ?? 18200) + 24;
  const url = `http://127.0.0.1:${port}/relays.json`;
  const list = JSON.stringify(PUB);
  let answer: (res: ServerResponse) => void = (res) => res.end(list);
  const server = createServer((req, res) => (req.url === "/list" ? res.end(list) : answer(res)));
  const listening = Promise.withResolvers<void>();
  server.listen(port, "127.0.0.1", listening.resolve);
  await listening.promise;
  const fetchWith = (handler: (res: ServerResponse) => void) => {
    answer = handler;
    return fetchText(url, new AbortController().signal);
  };
  try {
    assert.equal(await fetchWith((res) => res.end(list)), list);
    await assert.rejects(fetchWith((res) => res.writeHead(302, { location: "/list" }).end()), TypeError, "a redirect");
    await assert.rejects(fetchWith((res) => res.writeHead(203).end(list)), /HTTP 203/, "status 203");
    const bad = Buffer.from(list.replace('"X"', '"X_"'));
    bad[bad.indexOf(0x5f)] = 0xff;
    await assert.rejects(fetchWith((res) => res.end(bad)), TypeError, "invalid UTF-8");

    // An endless body: the read stops at the limit, long before the 5 s deadline, and the connection closes.
    const closed = Promise.withResolvers<void>();
    const started = performance.now();
    const endless = fetchWith((res) => {
      res.on("close", () => closed.resolve());
      res.on("error", () => {});
      res.write(list.slice(0, -1));
      const drip = () => {
        if (!res.destroyed && res.write(" ".repeat(16_384))) setImmediate(drip);
        else if (!res.destroyed) res.once("drain", drip);
      };
      drip();
    });
    await assert.rejects(endless, /over 65536 bytes/);
    await closed.promise;
    assert.ok(performance.now() - started < 4000, "stopped at the limit, not at the deadline");
  } finally {
    server.closeAllConnections();
    const stopped = Promise.withResolvers<void>();
    server.close(() => stopped.resolve());
    await stopped.promise;
  }
});
