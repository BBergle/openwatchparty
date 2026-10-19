# Web example

A one-page app on the TypeScript SDK: two browser windows pick their own copy of a video file and watch it together in one room. Read it alongside [the integration guide](../../docs/integration.md).

## Run it on a local relay

You need Node 22 or later, Go 1.26 and any static file server. The page must be served over HTTP; opening it from `file://` leaves it inert.

```sh
# 1. Build the SDK once.
(cd sdk/typescript && npm ci && npm run build)

# 2. Start a relay. It prints "listening 127.0.0.1:8080" when it is ready.
(cd relay && go run ./cmd/owp-relay --addr 127.0.0.1:8080 --admin-addr "")

# 3. In another terminal, serve the repository root.
python3 -m http.server 8000 --bind 127.0.0.1
```

Open `http://127.0.0.1:8000/examples/web/?relay=http://127.0.0.1:8080` in two browser windows. The `relay` parameter fills the Relay field with the relay's base URL, not a `ws://` URL. To use the public relays instead, open `http://127.0.0.1:8000/examples/web/` and leave the Relay field empty; no local relay is needed.

A local relay applies its per-IP limits (10 connections and 3 live rooms by default), and every window on one machine shares one IP. For more windows or rooms, raise them:

```sh
(cd relay && go run ./cmd/owp-relay --addr 127.0.0.1:8080 --admin-addr "" --max-connections-per-ip 100 --max-rooms-per-ip 100)
```

## Try it

In the first window, pick a video file the browser can play and choose Create room; the page shows the room code. In the second window, pick a file, type the code and choose Join. The videos play together, and the host's native video controls drive the room for both. A browser that blocks autoplay keeps a window's video paused; the page then asks the user to press play on the video, and the video joins the room.

Without a TMDB ID only the same file (name and size) matches; a different file leaves the guest `unavailable`. With the same TMDB ID in both windows the descriptor is a `movie`, taken by shared catalogue ID. A file a few seconds longer prompts the guest to play it anyway. The offset field then aligns a different cut. Changing the file, Title or TMDB ID shares again as host. As guest, it searches again with no file chosen. With one chosen, it plays the new file through `choose` if the resolver takes it.

## Files

`index.html` holds the markup, styles and the import map that maps `openwatchparty` to `../../sdk/typescript/dist/index.js`. `app.js` is the whole app, no bundler.

See the [TypeScript SDK README](../../sdk/typescript/README.md) for the API and the [relay README](../../relay/README.md) for relay flags.
