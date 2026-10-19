// A real relay built from relay/ and a simulated player, for the integration tests. These run in real time: the relay
// is a separate process with its own clock, so virtual time can't drive it.
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { setTimeout as delay } from "node:timers/promises";
import type { WatchParty } from "../../src/party.ts";
import type { PartyState, PlayerAdapter } from "../../src/types.ts";
import { RELAY_DIR } from "../paths.ts";

// SdkTs owns ports 18200–18249 on shared machines; CI may move the range.
const PORT_BASE = Number(process.env["OWP_TEST_PORT_BASE"] ?? 18200);
const PORT_COUNT = 50;
let nextPort = 0;
let binary: string | null = null;

/** Builds the relay once per test process. */
export function relayBinary(): string {
  if (binary === null) {
    const out = join(mkdtempSync(join(tmpdir(), "owp-sdkts-")), "owp-relay");
    execFileSync("go", ["build", "-o", out, "./cmd/owp-relay"], { cwd: RELAY_DIR, stdio: "inherit" });
    binary = out;
  }
  return binary;
}

function portFree(port: number): Promise<boolean> {
  const { promise, resolve } = Promise.withResolvers<boolean>();
  const server = createServer();
  server.once("error", () => resolve(false));
  server.listen(port, "127.0.0.1", () => server.close(() => resolve(true)));
  return promise;
}

/** A free port of the range on 127.0.0.1. */
export async function freePort(): Promise<number> {
  for (let i = 0; i < PORT_COUNT; i++) {
    const port = PORT_BASE + ((nextPort++) % PORT_COUNT);
    if (await portFree(port)) return port;
  }
  throw new Error(`no free port in ${PORT_BASE}–${PORT_BASE + PORT_COUNT - 1}`);
}

export interface RelayOptions {
  port?: number;
  stateDir?: string;
}

/** One relay process on 127.0.0.1 with timers at 0.1 and per-IP limits raised for one-machine testing. */
export class TestRelay {
  readonly port: number;
  readonly base: string;
  readonly #proc: ChildProcess;
  readonly #exited: Promise<number | null>;

  private constructor(port: number, proc: ChildProcess) {
    this.port = port;
    this.base = `http://127.0.0.1:${port}`;
    this.#proc = proc;
    const exited = Promise.withResolvers<number | null>();
    proc.once("exit", (code) => exited.resolve(code));
    this.#exited = exited.promise;
  }

  static async start(options: RelayOptions = {}): Promise<TestRelay> {
    const port = options.port ?? (await freePort());
    const args = [
      `--addr=127.0.0.1:${port}`,
      "--admin-addr=",
      "--timer-scale=0.1",
      "--max-connections-per-ip=1000",
      "--max-rooms-per-ip=1000",
      "--join-burst=1000",
      "--joins-per-second=1000",
    ];
    if (options.stateDir !== undefined) args.push(`--state-dir=${options.stateDir}`);
    const proc = spawn(relayBinary(), args, { stdio: ["ignore", "pipe", "pipe"] });
    const relay = new TestRelay(port, proc);
    const ready = Promise.withResolvers<void>();
    const timer = setTimeout(() => ready.reject(new Error("the relay did not start in 10 s")), 10_000);
    createInterface({ input: proc.stdout! }).on("line", (line) => {
      if (line.startsWith("listening ")) ready.resolve();
    });
    let stderr = "";
    proc.stderr!.on("data", (chunk: Buffer) => (stderr += chunk.toString()));
    proc.once("exit", (code) => ready.reject(new Error(`the relay exited with ${code}: ${stderr}`)));
    try {
      await ready.promise;
    } finally {
      clearTimeout(timer);
    }
    return relay;
  }

  /** SIGTERM: the relay saves its rooms, closes every connection with 1001 and exits. */
  async stop(): Promise<void> {
    if (this.#proc.exitCode === null && this.#proc.signalCode === null) this.#proc.kill("SIGTERM");
    await this.#exited;
  }
}

/** A player whose position runs on the local clock; commands land at once. */
export class SimPlayer implements PlayerAdapter {
  readonly durationMs: number | null;
  buffering = false;
  firstFrameShown = true;
  paused = true;
  rate = 1;
  #anchor = 0;
  #since = performance.now();

  constructor(durationMs: number | null) {
    this.durationMs = durationMs;
  }

  get positionMs(): number {
    const moved = this.paused || this.buffering ? 0 : (performance.now() - this.#since) * this.rate;
    const at = this.#anchor + moved;
    return Math.floor(this.durationMs === null ? at : Math.min(at, this.durationMs));
  }

  #rebase(): void {
    this.#anchor = this.positionMs;
    this.#since = performance.now();
  }

  play(): void {
    this.#rebase();
    this.paused = false;
  }
  pause(): void {
    this.#rebase();
    this.paused = true;
  }
  seek(positionMs: number): void {
    this.#anchor = positionMs;
    this.#since = performance.now();
  }
  setRate(rate: number): void {
    this.#rebase();
    this.rate = rate;
  }
}

/** Resolves once `ok` holds for the party's state; checks at once and on every change. */
export function stateWhere<T>(party: WatchParty<T>, what: string, ok: (s: PartyState<T>) => boolean, ms = 10_000): Promise<PartyState<T>> {
  const { promise, resolve, reject } = Promise.withResolvers<PartyState<T>>();
  if (ok(party.state)) return Promise.resolve(party.state);
  const timer = setTimeout(() => {
    off();
    reject(new Error(`timed out waiting for ${what}; state: ${JSON.stringify(summary(party.state))}`));
  }, ms);
  const off = party.on("state", (s) => {
    if (!ok(s)) return;
    clearTimeout(timer);
    off();
    resolve(s);
  });
  return promise;
}

/** Retries `check` every 50 ms until it stops throwing; for player positions, which change without a state change. */
export async function eventually(check: () => void, ms = 10_000): Promise<void> {
  const end = performance.now() + ms;
  for (;;) {
    try {
      check();
      return;
    } catch (e) {
      if (performance.now() > end) throw e;
    }
    await delay(50);
  }
}

export const sleep = (ms: number): Promise<void> => delay(ms);

function summary<T>(s: PartyState<T>) {
  return {
    connection: s.connection,
    phase: s.room?.phase,
    positionMs: s.room?.positionMs,
    waitingOn: s.room?.waitingOn,
    match: s.match,
    player: s.player,
    participants: s.room?.participants.map((p) => `${p.peerId}:${p.player}:${p.match?.state ?? "-"}`),
  };
}
