# Web example

This is a one-page app on the TypeScript SDK. Two browser windows each pick their own copy of a video file and watch it together in one room. Read it alongside [the integration guide](../../docs/integration.md).

## Run it on a local relay

You need Node 22 or later, Go 1.26 and any static file server. The page must be served over HTTP. Opened from `file://`, it stays inert.

Run these steps from the repository root:

1. Build the SDK once:

   ```sh
   (cd sdk/typescript && npm ci && npm run build)
   ```

2. Start a relay. It prints `listening 127.0.0.1:8080` when it is ready.

   ```sh
   (cd relay && go run ./cmd/owp-relay --addr 127.0.0.1:8080 --admin-addr "")
   ```

3. In another terminal, serve the repository root:

   ```sh
   python3 -m http.server 8000 --bind 127.0.0.1
   ```

4. Open `http://127.0.0.1:8000/examples/web/?relay=http://127.0.0.1:8080` in two browser windows.

The `relay` parameter fills the Relay field. It takes the relay's base URL, not a `ws://` URL.

To use the public relays instead, open `http://127.0.0.1:8000/examples/web/` and leave the Relay field empty. You don't need a local relay then.

A local relay applies its per-IP limits, which are 10 connections and 3 live rooms by default. Every window on one machine shares one IP. For more windows or rooms, raise the limits:

```sh
(cd relay && go run ./cmd/owp-relay --addr 127.0.0.1:8080 --admin-addr "" --max-connections-per-ip 100 --max-rooms-per-ip 100)
```

## Try it

1. In the first window, pick a video file the browser can play, and choose Create room. The page shows the room code.
2. In the second window, pick a file, type the code and choose Join.

The videos play together, and the host's native video controls drive the room for both.

A browser that blocks autoplay keeps a window's video paused. The page then asks the user to press play on the video, and the video joins the room.

### Which files match

Without a TMDB ID, only the same file matches, by name and size. A different file leaves the guest `unavailable`.

With the same TMDB ID in both windows, the descriptor is a `movie`, and the guest's file counts as the same title through the shared catalogue ID. A file a few seconds longer then prompts the guest to play it anyway. The offset field can then align the different cut.

### Changing the file

Changing the file, the Title or the TMDB ID has a different effect for each role:

- As host, the page shares the media again.
- As guest with no file chosen, it searches again.
- As guest with a file chosen, it plays the new file through `choose`, if the resolver takes it.

## Files

| File | What it holds |
|---|---|
| `index.html` | The markup, the styles and the import map that maps `openwatchparty` to `../../sdk/typescript/dist/index.js`. |
| `app.js` | The whole app, with no bundler. |

See the [TypeScript SDK README](../../sdk/typescript/README.md) for the API and the [relay README](../../relay/README.md) for relay flags.
