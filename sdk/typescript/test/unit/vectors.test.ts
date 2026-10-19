// Runs every table in conformance/vectors/ (SDK.md §17) against the SDK's own functions.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { describe, mock, test } from "node:test";
import { setImmediate as turn } from "node:timers/promises";
import { Clock } from "../../src/clock.ts";
import { normalizeCode, routeCode } from "../../src/code.ts";
import { PartyError } from "../../src/error.ts";
import { sameTitle } from "../../src/matching.ts";
import { checkMedia, receivedMedia } from "../../src/media.ts";
import { parseRoomMessage } from "../../src/messages.ts";
import { reconcile } from "../../src/reconcile.ts";
import { WatchParty } from "../../src/party.ts";
import { Relays } from "../../src/relays.ts";
import { isObject, parseRelayList } from "../../src/relaylist.ts";
import { isRunning, roomPosition } from "../../src/timeline.ts";
import type { Candidate, Resolver } from "../../src/types.ts";
import { VECTORS } from "../paths.ts";

const relays = Relays.public();

type Json = any;
interface Case {
  name: string;
  input: Json;
  expect: Json;
}

/** A party whose relay answers `join` with a room of `media` at epoch 1, and ignores everything else. */
function scriptedParty(media: Json, resolver: Resolver<string>): WatchParty<string> {
  const participant = { peerId: "p1", name: "Ann", client: "Test/1", host: true, connected: true, player: "loading", match: null, offsetMs: 0 };
  const room = {
    rev: 1, code: "K7MQ2X", epoch: 1, phase: "paused", positionMs: 0, atMs: 0, rate: 1, control: "host", locked: false,
    waitingOn: [], participants: [participant],
  };
  const joined = { type: "joined", code: "K7MQ2X", you: { peerId: "p1", token: "t".repeat(43) }, room, media, serverTime: 0 };
  const socket = {
    readyState: 0,
    onopen: null as ((ev: unknown) => void) | null,
    onmessage: null as ((ev: { data: string }) => void) | null,
    onclose: null,
    onerror: null,
    send(text: string) {
      if (JSON.parse(text).type === "join") queueMicrotask(() => socket.onmessage?.({ data: JSON.stringify(joined) }));
    },
    close() {
      socket.readyState = 3;
    },
  };
  return new WatchParty({
    relays: Relays.at("http://relay.test"),
    client: "Test/1",
    name: "Ann",
    resolver,
    nowMs: () => Date.now(),
    connect: () => {
      queueMicrotask(() => {
        socket.readyState = 1;
        socket.onopen?.({});
      });
      return socket as unknown as WebSocket;
    },
  });
}

const runners: Record<string, (input: Json) => Json | Promise<Json>> = {
  // The constructor's checks: valid when it constructs, invalid when it throws invalid_message.
  names(input) {
    const name = input.field === "name" ? input.value : "Ann";
    const client = input.field === "client" ? input.value : "Test/1.0";
    try {
      new WatchParty({ relays, client, name, resolver: { resolve: () => [] } }).close();
      return { valid: true };
    } catch (e) {
      assert.ok(e instanceof PartyError && e.code === "invalid_message", String(e));
      return { valid: false };
    }
  },

  timeline(input) {
    return {
      positionMs: roomPosition(input.room, input.durationMs, input.serverNowMs),
      running: isRunning(input.room, input.serverNowMs),
    };
  },

  "clock-sync"(input) {
    const clock = new Clock();
    const pongs: Json[] = [];
    for (const e of input.events) {
      if ("ping" in e) {
        clock.ping(e.ping, e.atMs);
        continue;
      }
      const result = clock.pong(e.pong, e.serverTime, e.atMs);
      const best = clock.best;
      pongs.push({ result, offsetMs: best?.offsetMs ?? null, rttMs: best?.rttMs ?? null });
    }
    return { pongs };
  },

  // player.seekAccuracyMs is an optional input: absent means 0.
  reconcile(input) {
    const out = reconcile({
      room: input.room,
      durationMs: input.durationMs,
      serverNowMs: input.serverNowMs,
      offsetMs: input.offsetMs,
      player: { ...input.player, seekAccuracyMs: input.player.seekAccuracyMs ?? 0 },
      nudging: input.nudging,
      sinceSeekMs: input.sinceSeekMs,
    });
    return {
      actions: out.actions.map((a) => (a.do === "setRate" ? { do: "setRate", rate: round6(a.rate) } : a)),
      nudging: out.nudging,
      playInMs: out.playInMs,
    };
  },

  // The walk inside a party, in virtual time and through the party's real probe timeouts (SDK.md §9.5): a simulated
  // probe answers `takesMs` after it starts unless the SDK cancels it first.
  async matching(input) {
    // Every descriptor of the table passes the receive check; the SDK only ever matches such a descriptor.
    assert.ok(receivedMedia(input.media) !== null, "the descriptor passes the receive check");
    mock.timers.enable({ apis: ["setTimeout", "Date"], now: 0 });
    try {
      const answersAt: number[] = [];
      const probed: string[] = [];
      const candidates: Candidate<string>[] = input.candidates.map((c: Json) => {
        const { probe: sim, ...rest } = c;
        if (sim === undefined) return { ...rest, ref: c.id };
        const probe = (signal: AbortSignal) => {
          const { promise, resolve } = Promise.withResolvers<number | null>();
          const at = Date.now() + sim.takesMs;
          answersAt.push(at);
          const done = () => answersAt.splice(answersAt.indexOf(at), 1);
          const timer = setTimeout(() => {
            done();
            probed.push(c.id);
            resolve(sim.durationMs);
          }, sim.takesMs);
          signal.addEventListener("abort", () => (clearTimeout(timer), done()), { once: true });
          return promise;
        };
        return { ...rest, ref: c.id, probe };
      });
      const party = scriptedParty(input.media, { resolve: () => candidates });
      await party.join("K7MQ2X");
      // Each turn lets the walk run until it waits on a probe; then time jumps to the next answer.
      for (;;) {
        await turn();
        if (answersAt.length === 0) break;
        mock.timers.tick(Math.min(...answersAt) - Date.now());
      }
      const { match, choice, mismatch, candidates: published } = party.state;
      party.close();
      assert.ok(match?.state !== "searching" || mismatch !== null, "the walk finished");
      const result = match?.state === "matched" ? "matched" : mismatch !== null ? "mismatch" : "unavailable";
      return {
        order: published.map((r) => r.candidate.id),
        candidates: published.map((r) => ({ id: r.candidate.id, deltaMs: r.deltaMs })),
        probed,
        result,
        choice: result === "matched" ? choice!.id : (mismatch?.candidate.id ?? null),
        deltaMs: result === "matched" ? match!.deltaMs : (mismatch?.deltaMs ?? null),
      };
    } finally {
      mock.timers.reset();
    }
  },

  "same-title"(input) {
    const [a, b] = [receivedMedia(input.a), receivedMedia(input.b)];
    assert.ok(a !== null && b !== null, "both descriptors pass the receive check");
    const same = sameTitle(a, b);
    assert.equal(sameTitle(b, a), same, "sameTitle is symmetric");
    return { same };
  },

  "code-routing"(input) {
    let code: string;
    try {
      code = normalizeCode(input.code);
    } catch (e) {
      assert.ok(e instanceof PartyError);
      return { error: e.code };
    }
    const relays = input.relays as { id: string; prefixes: string }[];
    return { code, relay: routeCode(code, relays)?.id ?? null };
  },

  "relay-list"(input) {
    const list = parseRelayList(input.list);
    return list === null ? { error: "invalid_list" } : { relays: list };
  },

  // The check of a room message's members other than type and media, the snapshot and its action: an invalid room is
  // dropped. A room message is an object.
  "room-check"(input) {
    return { valid: isObject(input.room) && parseRoomMessage(input.room) !== null };
  },

  // receive: the check of a descriptor the relay sent. send: checkMedia on the decoded descriptor, sizes included.
  "media-check"(input) {
    return { valid: input.on === "send" ? checkMedia(input.media) === null : receivedMedia(input.media) !== null };
  },
};

function round6(r: number): number {
  return Math.round(r * 1e6) / 1e6;
}

// party.json holds traces, not input/output cases: party-traces.test.ts is its driver and runs it.
const TRACES = "party.json";
const tables = readdirSync(VECTORS).filter((f) => f.endsWith(".json"));
const files = tables.filter((f) => f !== TRACES);

test("every vector table has a runner", () => {
  assert.deepEqual(tables.map((f) => f.replace(/\.json$/, "")).sort(), [...Object.keys(runners), "party"].sort());
});

for (const file of files) {
  const table = JSON.parse(readFileSync(VECTORS + file, "utf8")) as { table: string; cases: Case[] };
  describe(`vectors: ${table.table}`, () => {
    const run = runners[table.table];
    for (const c of table.cases) {
      test(c.name, async () => {
        assert.ok(run, `no runner for ${table.table}`);
        assert.deepStrictEqual(await run(structuredClone(c.input)), c.expect);
      });
    }
  });
}
