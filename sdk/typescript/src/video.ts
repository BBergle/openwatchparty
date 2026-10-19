import type { WatchParty } from "./party.ts";
import type { TimerHandle } from "./runtime.ts";
import type { PlayerAdapter } from "./types.ts";

const HAVE_CURRENT_DATA = 2;
const HAVE_FUTURE_DATA = 3;
const SEEK_GUARD_MS = 10_000;
// A seek event within this distance of the SDK's own seek target is the SDK's.
const SEEK_SLACK_MS = 50;
// While the user drags the seek bar, one seek per this many ms goes to the room (the README's "Player and resolver").
const SEEK_SPACING_MS = 250;
const NUDGE_EVENTS = ["loadeddata", "canplay", "playing", "waiting", "seeked", "durationchange", "ended", "emptied"];

/** What the SDK last commanded on an element; an element event that disagrees came from the user. */
interface Commanded {
  paused: boolean;
  rate: number;
  seekTarget: number | null;
}

/** The adapter of `video` and the record of what it was last commanded. */
function adapterFor(video: HTMLMediaElement): { adapter: PlayerAdapter; commanded: Commanded } {
  const commanded: Commanded = { paused: video.paused, rate: video.playbackRate, seekTarget: null };
  const adapter: PlayerAdapter = {
    get positionMs() {
      return Math.floor(video.currentTime * 1000);
    },
    get durationMs() {
      return Number.isFinite(video.duration) ? Math.round(video.duration * 1000) : null;
    },
    get paused() {
      return video.paused;
    },
    get buffering() {
      return !video.paused && !video.ended && video.readyState < HAVE_FUTURE_DATA;
    },
    get firstFrameShown() {
      return video.readyState >= HAVE_CURRENT_DATA;
    },
    play() {
      commanded.paused = false;
      if (!video.paused) return undefined;
      const started = video.play();
      // An element allowed to play stops being paused at once (HTML): resolve then, not once playback runs, so a later
      // pause isn't queued behind buffering. One that stays paused refused to start, such as under the autoplay policy:
      // its promise rejects with the reason (NotAllowedError), and the SDK reports the player blocked.
      if (video.paused) return started;
      started.catch(() => {
        // Interrupted by a later pause.
      });
      return undefined;
    },
    pause() {
      commanded.paused = true;
      if (!video.paused) video.pause();
    },
    seek(positionMs: number) {
      commanded.seekTarget = positionMs;
      if (Math.abs(video.currentTime * 1000 - positionMs) >= 1) video.currentTime = positionMs / 1000;
      if (!video.seeking) return;
      const { promise, resolve } = Promise.withResolvers<void>();
      const done = () => {
        clearTimeout(guard);
        video.removeEventListener("seeked", done);
        video.removeEventListener("emptied", done);
        resolve();
      };
      const guard = setTimeout(done, SEEK_GUARD_MS);
      video.addEventListener("seeked", done);
      video.addEventListener("emptied", done);
      return promise;
    },
    setRate(rate: number) {
      commanded.rate = rate;
      if (video.playbackRate !== rate) video.playbackRate = rate;
    },
  };
  return { adapter, commanded };
}

/**
 * Wraps an `HTMLMediaElement` (`<video>` or `<audio>`) in a `PlayerAdapter`, and nothing else: the element's own
 * controls aren't routed to a party. For pages the app doesn't own, such as an extension's; pass the adapter to
 * `attachPlayer`. An app's own page uses `attachVideo`. Its `play` throws when the element refuses to start, such as
 * under the browser's autoplay policy; the party then emits `playBlocked`.
 */
export function videoAdapter(video: HTMLMediaElement): PlayerAdapter {
  return adapterFor(video).adapter;
}

/**
 * Wraps an `HTMLMediaElement` (`<video>` or `<audio>`) in a `PlayerAdapter` (`videoAdapter`), attaches it to the party
 * with `ref`, the `ref` of the file the element has open, and routes the element's own controls to the party: a press
 * on the native play, pause, seek or speed control becomes the party command, and the SDK's own commands are not
 * mistaken for presses. While the user drags the seek bar, at most one `seek` per 250 ms goes to the room, with the
 * latest position, and the final position follows within 250 ms. Call it once per file, once the element has the file
 * open.
 *
 * Returns a cleanup that removes the listeners and detaches this adapter only, never a newer one, and resolves with
 * `detachPlayer`'s completion: await it before disposing the element. After `close()`, it resolves at once; await
 * `close()` instead.
 */
export function attachVideo<T>(party: WatchParty<T>, video: HTMLMediaElement, ref: T): () => Promise<void> {
  const { adapter, commanded } = adapterFor(video);

  const command = (run: () => void) => {
    try {
      run();
    } catch {
      // Not a member, no media or not a controller: the next step puts the player back.
    }
  };
  const onPlay = () => {
    if (!commanded.paused) return;
    // The room starts everyone together, so the element waits for the room.
    video.pause();
    command(() => party.play());
  };
  const onPause = () => {
    if (commanded.paused) return;
    // Paused either way, so the element's next play is a press. At its end the element pauses itself, and the room
    // pauses there too (SPEC.md §12.11): that isn't a press.
    commanded.paused = true;
    if (!video.ended) command(() => party.pause());
  };
  // A drag on the seek bar fires seeking for every position it passes: the first goes to the room at once, then at most
  // one per 250 ms, the latest, so the position where the drag stops goes out once its 250 ms are up.
  let seekTimer: TimerHandle | undefined;
  let draggedTo: number | null = null;
  const sendSeek = (at: number) => {
    commanded.seekTarget = at;
    command(() => party.seek(at));
    seekTimer = setTimeout(() => {
      seekTimer = undefined;
      const latest = draggedTo;
      draggedTo = null;
      if (latest !== null) sendSeek(latest);
    }, SEEK_SPACING_MS);
  };
  const onSeeking = () => {
    const at = Math.floor(video.currentTime * 1000);
    const target = commanded.seekTarget;
    if (target !== null && Math.abs(at - target) <= SEEK_SLACK_MS) return;
    if (seekTimer === undefined) sendSeek(at);
    else draggedTo = at;
  };
  const onRate = () => {
    const rate = video.playbackRate;
    if (rate === commanded.rate) return;
    const before = commanded.rate;
    // Recorded before the party applies it: the SDK then sets the rate on a bound element and records that, so the
    // ratechange it causes isn't taken for the user's.
    commanded.rate = rate;
    try {
      party.setRate(rate);
    } catch {
      commanded.rate = before;
      video.playbackRate = before;
    }
  };
  const nudge = () => command(() => party.playerChanged());

  const listeners: [string, () => void][] = [
    ["play", onPlay],
    ["pause", onPause],
    ["seeking", onSeeking],
    ["ratechange", onRate],
    ...NUDGE_EVENTS.map((type): [string, () => void] => [type, nudge]),
  ];
  for (const [type, fn] of listeners) video.addEventListener(type, fn);
  party.attachPlayer(adapter, ref);

  return () => {
    for (const [type, fn] of listeners) video.removeEventListener(type, fn);
    clearTimeout(seekTimer);
    try {
      return party.detachPlayer(adapter);
    } catch {
      // The party is closed: close() released the adapter and returned the completion.
      return Promise.resolve();
    }
  };
}
