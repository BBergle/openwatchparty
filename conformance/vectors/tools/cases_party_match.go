package main

// Party traces of matching (spec/SDK.md §9: own media, the resolver and its cancellation, the walk's probes, choose,
// the mismatch prompt, resolveAgain, setMedia's descriptor checks) and of the offset (§13). As in cases_party.go,
// every output is written by hand from SDK.md, and the comment above each trace names the sections it pins. Helpers
// stay local to partyMatchTraces so the package gains no names.

import "strings"

func partyMatchTraces() []Case {
	var cases []Case
	add := func(tr *trace) { cases = append(cases, tr.cas()) }

	ronin := func() *Media {
		return &Media{Kind: "movie", Title: "Ronin", Year: new(1998), Ids: map[string]string{"imdb": "tt0122690"},
			DurationMs: 7_320_000}
	}
	annHost, benGuest := ann, ben
	annHost.Host, benGuest.Host = true, false

	// heatShared is the room a guest joins at local 0 in these traces: Ben shared Heat (epoch 1) and paused it at 0.
	heatShared := func() snap {
		r := newRoom(3, 990_000, ben.reported("ready", matchOf("matched", 0)), ann)
		r.Epoch, r.Phase = 1, "paused"
		return r
	}
	// mediaRoom is room as the `room` message that starts its epoch with m, set by `by`: waiting at 0 on every
	// participant (SPEC.md §7.10), or for nil idle at 0 (§7.11); it carries the media (§8.3).
	mediaRoom := func(room snap, m *Media, by string) snap {
		room.Phase, room.PositionMs, room.WaitingOn = "idle", 0, []string{}
		if m != nil {
			room.Phase = "waiting"
			for _, p := range room.Participants {
				room.WaitingOn = append(room.WaitingOn, p.PeerID)
			}
		}
		return room.withMedia(m).acted("media", by).msg()
	}
	// statusOff is tr.status with an offset.
	statusOff := func(tr *trace, epoch int64, player string, match obj, offsetMs int64) obj {
		return o("type", "status", "epoch", epoch, "player", player, "match", match, "offsetMs", offsetMs,
			"rttMs", tr.setup.RttMs)
	}
	setMediaMsg := func(m *Media) obj { return o("type", "setMedia", "media", m, "positionMs", 0) }
	chose := func(id string, ok bool) obj { return done(id, "value", ok) }
	offer := func(id string, deltaMs int64) obj { return o("id", id, "deltaMs", deltaMs) }
	none := answer(objs()...)
	answerThrows := o("answerThrows", true)
	resolveFailed := func(timeout bool) obj { return o("type", "resolveFailed", "timeout", timeout) }
	heatPlayer := func(durationMs int64) obj {
		return player("p1", "positionMs", 0, "paused", true, "durationMs", durationMs, "canSetRate", true)
	}
	// benJoins answers the room hostJoined created with Ben joining it.
	benJoins := func(tr *trace, room snap) snap {
		room.Rev, room.Participants = 2, []peer{annHost, benGuest}
		tr.do(recv(room.msg()), out{Event: objs(evJoined("ben")), State: state("room", 2)})
		return room
	}
	// handOver makes Ben host: transferHost, then the relay's next snapshot, paused at 0, which the caller sends with
	// the host action.
	handOver := func(tr *trace, room snap) snap {
		tr.do(call("transferHost", "peerId", "ben"), out{Send: objs(o("type", "transferHost", "peerId", "ben"))})
		room.Rev, room.Phase, room.AtMs = room.Rev+1, "paused", tr.relayNow()
		room.Participants = []peer{ann, ben}
		return room
	}

	// SDK.md §9.1, §10.2, §10.3: own media (equal to the last setMedia) is matched with choice id "" and the setMedia
	// ref, binding an adapter attached with that ref, with no resolver call and no pause; resolveAgain does nothing in
	// an own-media epoch, also while another setMedia is pending (it unbinds); the next own media takes its own ref;
	// a null-media epoch clears match and choice and pauses the attached adapter once.
	{
		tr := newTrace("own media is chosen with id \"\" and its setMedia ref; resolveAgain leaves it alone; null media pauses once", setup())
		room := tr.hostJoined()
		tr.do(heatPlayer(heatMs), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{Send: objs(setMediaMsg(heat()))})
		room.Rev, room.Epoch = 2, 1
		tr.do(recv(mediaRoom(room, heat(), "ann")), out{
			Event:   objs(evAction("media", "ann", true, 2)),
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			State:   state("room", 2, "match", matchOf("matched", 0), "choice", choice("", "heat"), "candidates", objs()),
		})
		tr.do(call("resolveAgain"), out{State: state("match", matchOf("matched", 0), "choice", choice("", "heat"))})
		tr.do(call("setMedia", "media", ronin(), "ref", "ronin"), out{
			Send:  objs(setMediaMsg(ronin())),
			State: state("player", "loading"),
		})
		tr.do(call("resolveAgain"), out{State: state("match", matchOf("matched", 0), "choice", choice("", "heat"))})
		room.Rev, room.Epoch = 3, 2
		tr.do(recv(mediaRoom(room, ronin(), "ann")), out{
			Event: objs(evAction("media", "ann", true, 3)),
			Send:  objs(tr.status(2, "loading", matchOf("matched", 0))),
			State: state("room", 3, "media", "Ronin", "match", matchOf("matched", 0), "choice", choice("", "ronin"),
				"player", "loading"),
		})
		tr.do(call("attachPlayer", "player", "p1", "ref", "ronin"), out{Command: objs(cmd("p1", "setRate", "rate", 1))})
		tr.do(call("clearMedia"), out{Send: objs(o("type", "clearMedia"))})
		room.Rev, room.Epoch = 4, 3
		tr.do(recv(mediaRoom(room, nil, "ann")), out{
			Event:   objs(evAction("media", "ann", true, 4)),
			Command: objs(cmd("p1", "pause")),
			Send:    objs(tr.status(3, "idle", nil)),
			State:   state("room", 4, "media", nil, "match", nil, "choice", nil, "player", "idle"),
		})
		tr.do(advance(1000), out{})
		add(tr)
	}

	// SDK.md §9.1: own media is decided by value against the last setMedia: another participant's media clears the own
	// choice and starts a search; the same descriptor shared by the new host is own media again, which cancels that
	// search.
	{
		tr := newTrace("another participant's media clears the own choice; the same descriptor from the new host is own again", setup())
		room := benJoins(tr, tr.hostJoined())
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{Send: objs(setMediaMsg(heat()))})
		room.Rev, room.Epoch = 3, 1
		tr.do(recv(mediaRoom(room, heat(), "ann")), out{
			Event: objs(evAction("media", "ann", true, 3)),
			Send:  objs(tr.status(1, "loading", matchOf("matched", 0))),
			State: state("room", 3, "choice", choice("", "heat"), "match", matchOf("matched", 0)),
		})
		room = handOver(tr, room)
		tr.do(recv(room.acted("host", "ann").msg()), out{
			Event: objs(evAction("host", "ann", true, 4)),
			State: state("room", 4, "isHost", false),
		})
		room.Rev, room.Epoch = 5, 2
		tr.do(recv(mediaRoom(room, ronin(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 5)),
			Resolver: objs(o("resolve", "Ronin")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
			State:    state("choice", nil, "match", matchOf("searching")),
		})
		room.Rev, room.Epoch = 6, 3
		tr.do(recv(mediaRoom(room, heat(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 6)),
			Resolver: objs(o("resolveCancelled", "Ronin")),
			Send:     objs(tr.status(3, "loading", matchOf("matched", 0))),
			State:    state("choice", choice("", "heat"), "match", matchOf("matched", 0), "candidates", objs()),
		})
		add(tr)
	}

	// SDK.md §9.1, §7.4, §15: "last sent" is recorded at the setMedia call and kept when the relay refuses it (an Error
	// event), so the new host's identical descriptor is own media; the party's end resets it, so after joining again
	// the same media is searched for.
	{
		tr := newTrace("own media survives a refused setMedia and resets at the party's end", setup())
		room := benJoins(tr, tr.hostJoined())
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{Send: objs(setMediaMsg(heat()))})
		tr.do(recv(o("type", "error", "code", "rate_limited", "message", "Too many messages", "re", "setMedia")), out{
			Event: objs(o("type", "error", "code", "rate_limited", "re", "setMedia")),
			State: state("media", nil),
		})
		room = handOver(tr, room)
		room.Phase = "idle"
		tr.do(recv(room.acted("host", "ann").msg()), out{Event: objs(evAction("host", "ann", true, 3)), State: state("isHost", false)})
		room.Rev, room.Epoch = 4, 1
		tr.do(recv(mediaRoom(room, heat(), "ben")), out{
			Event: objs(evAction("media", "ben", false, 4)),
			Send:  objs(tr.status(1, "loading", matchOf("matched", 0))),
			State: state("room", 4, "choice", choice("", "heat"), "match", matchOf("matched", 0)),
		})
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended", "choice", nil),
		})
		again := ann
		again.PeerID = "ann2"
		room.Rev, room.Epoch, room.Phase, room.AtMs = 5, 1, "paused", tr.relayNow()
		room.Participants = []peer{ben, again}
		tr.do(call("join", "id", "c2", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann2", room, heat(), tr.relayNow())), out{
			Done:     objs(done("c2")),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(1, "loading", matchOf("searching"))),
			State:    state("connection", "connected", "choice", nil, "match", matchOf("searching")),
		})
		tr.do(none, out{State: state("match", matchOf("unavailable"), "player", "idle")})
		add(tr)
	}

	// SDK.md §9.1, §10.2, §11.1: a former host's own epoch keeps its choice after transferHost and detachPlayer (player
	// idle, away); attachPlayer with the setMedia ref binds it again.
	{
		tr := newTrace("after transferHost and detachPlayer a former host's choice is still its own file; attachPlayer binds it", setup())
		room := benJoins(tr, tr.hostJoined())
		tr.do(heatPlayer(heatMs), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{Send: objs(setMediaMsg(heat()))})
		room.Rev, room.Epoch = 3, 1
		tr.do(recv(mediaRoom(room, heat(), "ann")), out{
			Event:   objs(evAction("media", "ann", true, 3)),
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
		})
		room = handOver(tr, room)
		tr.do(recv(room.acted("host", "ann").msg()), out{
			Event: objs(evAction("host", "ann", true, 4)),
			State: state("isHost", false, "player", "ready"),
		})
		tr.do(call("detachPlayer", "id", "c2"), out{
			Done:  objs(done("c2")),
			State: state("player", "idle", "choice", choice("", "heat"), "match", matchOf("matched", 0)),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "idle", matchOf("matched", 0)))})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "ready", matchOf("matched", 0)))})
		add(tr)
	}

	// SDK.md §9.1, §9.2, §9.5: each new epoch first cancels the matching in flight, the probe under way included, then
	// searches for its own media with candidates empty.
	{
		tr := newTrace("new media cancels the resolver call and the probe under way, then searches again", setup())
		room := heatShared()
		tr.guestJoined(trCode, room, heat())
		tr.do(answer(candidate("a", "probe", true)), out{
			Resolver: objs(o("probe", "a")),
			State:    state("candidates", objs(ranked("a", nil)), "match", matchOf("searching")),
		})
		room.Rev, room.Epoch = 4, 2
		tr.do(recv(mediaRoom(room, ronin(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 4)),
			Resolver: objs(o("probeCancelled", "a"), o("resolve", "Ronin")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
			State:    state("room", 4, "media", "Ronin", "candidates", objs(), "match", matchOf("searching")),
		})
		room.Rev, room.Epoch = 5, 3
		tr.do(recv(mediaRoom(room, heat(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 5)),
			Resolver: objs(o("resolveCancelled", "Ronin"), o("resolve", "Heat")),
			Send:     objs(tr.status(3, "loading", matchOf("searching"))),
			State:    state("room", 5, "media", "Heat", "match", matchOf("searching")),
		})
		tr.do(answer(candidate("b", "durationMs", heatMs)), out{
			State: state("match", matchOf("matched", 0), "choice", choice("b", "b"), "candidates", objs(ranked("b", 0))),
		})
		add(tr)
	}

	// SDK.md §8, §9.1, §10.2, §11.1, §12: new media unbinds the adapter and queues one pause behind the commands
	// already queued, which still run: the held pause, then the seek the SDK queued for seek(), then the epoch's pause.
	// A queued seek makes the player loading. The new epoch's room carries an action, so it also ends the hold.
	{
		tr := newTrace("new media unbinds and queues one pause behind the commands already queued", setup())
		room := tr.guestFollowing("everyone", false)
		tr.do(hold("p1"), out{})
		tr.do(call("pause"), out{
			Command: objs(cmd("p1", "pause")),
			Send:    objs(o("type", "pause", "positionMs", 610_500)),
		})
		tr.do(call("seek", "ms", 700_000), out{
			Send:  objs(o("type", "seek", "positionMs", 700_000)),
			State: state("player", "loading"),
		})
		room.Rev, room.Epoch = 8, 3
		tr.do(recv(mediaRoom(room, ronin(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 8)),
			Resolver: objs(o("resolve", "Ronin")),
			Send:     objs(tr.status(3, "loading", matchOf("searching"))),
			State:    state("room", 8, "choice", nil, "match", matchOf("searching"), "player", "loading"),
		})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "seek", "positionMs", 700_000), cmd("p1", "pause"))})
		tr.do(none, out{State: state("match", matchOf("unavailable"), "player", "idle")})
		tr.do(advance(250), out{Send: objs(tr.status(3, "idle", matchOf("unavailable")))})
		add(tr)
	}

	// SDK.md §9.1, §9.5, §9.6, §10.2, §13: resolveAgain without a choice reruns the search (searching, mismatch null,
	// candidates empty until the answer, then the walk) and keeps the offset and the attached adapter, with no adapter
	// action; the match binds it at the room rate; the held player is ready within 500 ms of the offset position.
	{
		tr := newTrace("resolveAgain without a choice searches again and keeps the offset and the attached player", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		tr.do(answer(candidate("a", "durationMs", heatMs+4000)), out{
			State: state("match", matchOf("searching", 4000), "mismatch", offer("a", 4000), "candidates", objs(ranked("a", 4000))),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("searching", 4000)))})
		tr.do(heatPlayer(heatMs+100), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "b"), out{State: state("player", "loading")})
		tr.do(call("setOffset", "ms", 400), out{State: state("offsetMs", 400)})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 1, "loading", matchOf("searching", 4000), 400))})
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("resolve", "Heat")),
			State: state("match", matchOf("searching"), "mismatch", nil, "candidates", objs(), "offsetMs", 400,
				"player", "loading"),
		})
		tr.do(answer(candidate("b", "durationMs", heatMs+100)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State: state("match", matchOf("matched", 100), "choice", choice("b", "b"), "candidates", objs(ranked("b", 100)),
				"offsetMs", 400, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 1, "ready", matchOf("matched", 100), 400))})
		add(tr)
	}

	// SDK.md §9.1, §9.2, §9.4: resolveAgain with a choice runs only the resolver and the ranking: candidates stay until
	// the answer replaces them, ranked, deltaMs from counting durations only, no probe; choice, match and the bound
	// adapter stay. A throw then emits ResolveFailed and leaves candidates empty, nothing else changing.
	{
		tr := newTrace("resolveAgain with a choice replaces only the candidates, ranked and unprobed; a throw leaves none", setup())
		tr.guestFollowing("host", false)
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("resolve", "Heat")),
			State: state("candidates", objs(ranked("heat-a", 500)), "choice", choice("heat-a", "heat-a"),
				"match", matchOf("matched", 500), "player", "ready"),
		})
		tr.do(answer(candidate("x", "probe", true), candidate("y", "durationMs", heatMs+2000),
			candidate("heat-a", "durationMs", heatMs+500)), out{
			State: state("candidates", objs(ranked("heat-a", 500), ranked("y", 2000), ranked("x", nil)),
				"choice", choice("heat-a", "heat-a"), "match", matchOf("matched", 500), "mismatch", nil, "player", "ready"),
		})
		tr.do(call("resolveAgain"), out{Resolver: objs(o("resolve", "Heat"))})
		tr.do(answerThrows, out{
			Event: objs(resolveFailed(false)),
			State: state("candidates", objs(), "choice", choice("heat-a", "heat-a"), "match", matchOf("matched", 500),
				"player", "ready"),
		})
		tr.do(advance(250), out{})
		add(tr)
	}

	// SDK.md §9.2, §9.6, §10.2: a throwing resolver means no candidates: ResolveFailed (not a timeout), match
	// unavailable, player idle; resolveAgain (no choice) searches again and its answer matches.
	{
		tr := newTrace("a throwing resolver emits ResolveFailed and leaves the match unavailable; resolveAgain searches again", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		tr.do(answerThrows, out{
			Event: objs(resolveFailed(false)),
			State: state("match", matchOf("unavailable"), "candidates", objs(), "player", "idle"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "idle", matchOf("unavailable")))})
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("resolve", "Heat")),
			State:    state("match", matchOf("searching"), "player", "loading"),
		})
		tr.do(answer(candidate("a", "durationMs", heatMs-700)), out{
			State: state("match", matchOf("matched", -700), "choice", choice("a", "a"), "candidates", objs(ranked("a", -700))),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("matched", -700)))})
		add(tr)
	}

	// SDK.md §2, §9.2, §10.3: the default resolve timeout is 30000 ms: searching until then; at 30000 the SDK cancels
	// the call, emits ResolveFailed (the timeout) and the match is unavailable. The offset set at 1000 moves the
	// heartbeat off 30000.
	{
		tr := newTrace("a resolver still running at the default 30000 ms is cancelled with ResolveFailed and unavailable", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		tr.do(advance(1000), out{})
		tr.do(call("setOffset", "ms", 100), out{Send: objs(statusOff(tr, 1, "loading", matchOf("searching"), 100))})
		tr.do(advance(28_999), out{
			Send:  objs(statusOff(tr, 1, "loading", matchOf("searching"), 100)),
			State: state("match", matchOf("searching")),
		})
		tr.do(advance(1), out{
			Resolver: objs(o("resolveCancelled", "Heat")),
			Event:    objs(resolveFailed(true)),
			Send:     objs(statusOff(tr, 1, "idle", matchOf("unavailable"), 100)),
			State:    state("match", matchOf("unavailable"), "candidates", objs(), "player", "idle"),
		})
		add(tr)
	}

	// SDK.md §9.2, §9.6, §10.2: without a resolver another participant's media has no candidates: unavailable and
	// idle at once, in every epoch, with no ResolveFailed.
	{
		s := setup()
		s.Resolver = false
		tr := newTrace("without a resolver another participant's media is unavailable, without ResolveFailed", s)
		room := heatShared()
		tr.do(call("join", "id", "c1", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Done:  objs(done("c1")),
			Send:  objs(tr.status(1, "idle", matchOf("unavailable"))),
			State: state("connection", "connected", "match", matchOf("unavailable"), "candidates", objs(), "player", "idle"),
		})
		room.Rev, room.Epoch = 4, 2
		tr.do(recv(mediaRoom(room, ronin(), "ben")), out{
			Event: objs(evAction("media", "ben", false, 4)),
			Send:  objs(tr.status(2, "idle", matchOf("unavailable"))),
			State: state("room", 4, "media", "Ronin", "match", matchOf("unavailable"), "player", "idle"),
		})
		add(tr)
	}

	// SDK.md §9.5: probes share 60000 ms; a probe's deadline is the budget left at its start, inclusive: b starts at
	// 20000 with 40000 left and its answer at exactly 60000 is in time (no cancel at 60001); 1500 ms off matches.
	{
		tr := newTrace("a probe answering exactly when the budget left has passed is in time", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		searching := tr.status(1, "loading", matchOf("searching"))
		tr.do(answer(candidate("a", "probe", true), candidate("b", "probe", true)), out{
			Resolver: objs(o("probe", "a")),
			State:    state("candidates", objs(ranked("a", nil), ranked("b", nil))),
		})
		tr.do(advance(20_000), out{Send: objs(searching)})
		tr.do(probeAnswer("a", nil), out{Resolver: objs(o("probe", "b"))})
		tr.do(advance(40_000), out{Send: objs(searching, searching, searching)})
		tr.do(probeAnswer("b", heatMs+1500), out{
			State: state("match", matchOf("matched", 1500), "choice", choice("b", "b"),
				"candidates", objs(ranked("a", nil), ranked("b", 1500))),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("matched", 1500)))})
		add(tr)
	}

	// SDK.md §9.4, §9.5, §9.6: a counting durationMs ranks first and is the closest so far; the probe still running
	// 1 ms after the budget left is cancelled and no probe starts after it; the closest is offered as a mismatch.
	{
		tr := newTrace("a probe still running 1 ms after the budget left is cancelled and no probe starts after it", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		searching := tr.status(1, "loading", matchOf("searching"))
		tr.do(answer(candidate("a", "probe", true), candidate("b", "probe", true), candidate("c", "durationMs", heatMs+3000)), out{
			Resolver: objs(o("probe", "a")),
			State:    state("candidates", objs(ranked("c", 3000), ranked("a", nil), ranked("b", nil)), "match", matchOf("searching")),
		})
		tr.do(advance(60_000), out{Send: objs(searching, searching, searching, searching)})
		tr.do(advance(1), out{
			Resolver: objs(o("probeCancelled", "a")),
			State: state("match", matchOf("searching", 3000), "mismatch", offer("c", 3000), "choice", nil,
				"candidates", objs(ranked("c", 3000), ranked("a", nil), ranked("b", nil))),
		})
		tr.do(advance(249), out{Send: objs(tr.status(1, "loading", matchOf("searching", 3000)))})
		add(tr)
	}

	// SDK.md §9.2, §9.6: choose takes a durationMs only from 1 to 604800000 ms, else probes, with no deadline; a probe
	// answer that isn't a duration returns false and changes nothing; |delta| ≤ 1500 is matched, else mismatched.
	{
		tr := newTrace("choose takes a durationMs only from 1 to 604800000 ms, else probes; a probe answer that isn't one chooses nothing", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		tr.do(none, out{State: state("match", matchOf("unavailable"), "player", "idle")})
		unavailable := tr.status(1, "idle", matchOf("unavailable"))
		tr.do(advance(250), out{Send: objs(unavailable)})
		tr.do(call("choose", "id", "c2", "candidate", candidate("x", "durationMs", 0, "probe", true)), out{
			Resolver: objs(o("probe", "x")),
		})
		tr.do(probeAnswer("x", 604_800_001), out{
			Done:  objs(chose("c2", false)),
			State: state("match", matchOf("unavailable"), "choice", nil, "player", "idle"),
		})
		tr.do(call("choose", "id", "c3", "candidate", candidate("y", "durationMs", 604_800_001, "probe", true)), out{
			Resolver: objs(o("probe", "y")),
		})
		tr.do(advance(61_000), out{Send: objs(unavailable, unavailable, unavailable, unavailable)})
		tr.do(probeAnswer("y", heatMs+1501), out{
			Done:  objs(chose("c3", true)),
			Send:  objs(tr.status(1, "loading", matchOf("mismatched", 1501))),
			State: state("match", matchOf("mismatched", 1501), "choice", choice("y", "y"), "candidates", objs(), "player", "loading"),
		})
		tr.do(call("choose", "id", "c4", "candidate", candidate("z", "durationMs", 604_800_000)), out{
			Done:  objs(chose("c4", true)),
			State: state("match", matchOf("mismatched", 604_800_000-heatMs), "choice", choice("z", "z")),
		})
		tr.do(call("choose", "id", "c5", "candidate", candidate("u", "durationMs", 1)), out{
			Done:  objs(chose("c5", true)),
			State: state("match", matchOf("mismatched", 1-heatMs), "choice", choice("u", "u")),
		})
		tr.do(call("choose", "id", "c6", "candidate", candidate("w", "durationMs", heatMs-1500)), out{
			Done:  objs(chose("c6", true)),
			State: state("match", matchOf("matched", -1500), "choice", choice("w", "w")),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("matched", -1500)))})
		add(tr)
	}

	// SDK.md §9.1, §9.6: without media choose returns false, idle or in a room, without probing; resolveAgain does
	// nothing.
	{
		tr := newTrace("choose without media is false and changes nothing; resolveAgain without media does nothing", setup())
		tr.do(call("choose", "id", "c0", "candidate", candidate("a", "durationMs", heatMs)), out{Done: objs(chose("c0", false))})
		tr.hostJoined()
		tr.do(call("choose", "id", "c2", "candidate", candidate("a", "durationMs", heatMs)), out{
			Done:  objs(chose("c2", false)),
			State: state("choice", nil, "match", nil),
		})
		tr.do(call("choose", "id", "c3", "candidate", candidate("b", "probe", true)), out{Done: objs(chose("c3", false))})
		tr.do(call("resolveAgain"), out{State: state("match", nil, "candidates", objs())})
		add(tr)
	}

	// SDK.md §9.6: the latest choose wins: it cancels an earlier one still probing, which returns false and changes
	// nothing; the latest one's probe answer is its duration.
	{
		tr := newTrace("a later choose cancels an earlier one still probing, which returns false", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		tr.do(none, out{State: state("match", matchOf("unavailable"))})
		tr.do(call("choose", "id", "c2", "candidate", candidate("a", "probe", true)), out{Resolver: objs(o("probe", "a"))})
		tr.do(call("choose", "id", "c3", "candidate", candidate("b", "probe", true)), out{
			Resolver: objs(o("probeCancelled", "a"), o("probe", "b")),
			Done:     objs(chose("c2", false)),
			State:    state("match", matchOf("unavailable"), "choice", nil),
		})
		tr.do(probeAnswer("b", heatMs), out{
			Done:  objs(chose("c3", true)),
			State: state("match", matchOf("matched", 0), "choice", choice("b", "b")),
		})
		add(tr)
	}

	// SDK.md §7.4, §9.1, §9.6: cancelling matching cancels a manual probe and its choose returns false: resolveAgain
	// (before its own search), a new epoch (before its search) and the party's end.
	{
		tr := newTrace("resolveAgain, a new epoch and the party's end cancel a manual probe; its choose returns false", setup())
		room := heatShared()
		tr.guestJoined(trCode, room, heat())
		tr.do(none, out{State: state("match", matchOf("unavailable"))})
		tr.do(call("choose", "id", "c2", "candidate", candidate("a", "probe", true)), out{Resolver: objs(o("probe", "a"))})
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("probeCancelled", "a"), o("resolve", "Heat")),
			Done:     objs(chose("c2", false)),
			State:    state("match", matchOf("searching"), "choice", nil),
		})
		tr.do(none, out{State: state("match", matchOf("unavailable"))})
		tr.do(call("choose", "id", "c3", "candidate", candidate("b", "probe", true)), out{Resolver: objs(o("probe", "b"))})
		room.Rev, room.Epoch = 4, 2
		tr.do(recv(mediaRoom(room, ronin(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 4)),
			Resolver: objs(o("probeCancelled", "b"), o("resolve", "Ronin")),
			Done:     objs(chose("c3", false)),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
			State:    state("room", 4, "media", "Ronin", "match", matchOf("searching"), "choice", nil),
		})
		tr.do(none, out{State: state("match", matchOf("unavailable"))})
		tr.do(call("choose", "id", "c4", "candidate", candidate("c", "probe", true)), out{Resolver: objs(o("probe", "c"))})
		tr.do(call("leave"), out{
			Resolver: objs(o("probeCancelled", "c")),
			Done:     objs(chose("c4", false)),
			Send:     objs(o("type", "leave")),
			Net:      objs(closed(1000)),
			Event:    objs(evEnded("left", nil)),
			State:    state("connection", "ended", "choice", nil),
		})
		add(tr)
	}

	// SDK.md §9.6, §10.2, §11.1: choosing another file unbinds the adapter without a pause or any command (loading)
	// until it is attached with that file's ref, which binds it; choosing the same ref again keeps it bound and
	// changes only the match.
	{
		tr := newTrace("choosing another file unbinds without a command until attached with its ref; the same ref again changes only the match", setup())
		tr.guestFollowing("host", false)
		tr.do(call("choose", "id", "c2", "candidate", candidate("heat-b", "durationMs", heatMs+100)), out{
			Done:  objs(chose("c2", true)),
			State: state("choice", choice("heat-b", "heat-b"), "match", matchOf("matched", 100), "player", "loading"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "loading", matchOf("matched", 100)))})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-b"), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(call("choose", "id", "c3", "candidate", candidate("heat-b", "durationMs", heatMs+2000)), out{
			Done:  objs(chose("c3", true)),
			State: state("choice", choice("heat-b", "heat-b"), "match", matchOf("mismatched", 2000), "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", matchOf("mismatched", 2000)))})
		add(tr)
	}

	// SDK.md §9.6, §10.2: a choose with the current choice's id replaces the choice and its ref: the adapter, attached
	// with the old ref, unbinds and stays unbound when attached with the old ref again; the new ref binds it. The
	// status change inside the spacing is undone by then, so none goes out.
	{
		tr := newTrace("choose with the current choice's id replaces the choice and its ref; the adapter follows only the new ref", setup())
		tr.guestFollowing("host", false)
		tr.do(call("choose", "id", "c2", "candidate", candidate("heat-a", "ref", "heat-a-2", "durationMs", heatMs+500)), out{
			Done:  objs(chose("c2", true)),
			State: state("choice", choice("heat-a", "heat-a-2"), "match", matchOf("matched", 500), "player", "loading"),
		})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{State: state("player", "loading")})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a-2"), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{})
		add(tr)
	}

	// SDK.md §9.4, §9.5, §9.6, §10.2, §13: none within 1500 ms offers the closest (searching with its delta);
	// declineMismatch makes it unavailable with the delta and clears both; accept and decline do nothing without a
	// pending mismatch; resolveAgain offers it again; acceptMismatch chooses it (mismatched with the delta), keeps the
	// offset and binds the adapter attached with the offered ref.
	{
		tr := newTrace("a mismatch offers the closest; decline and accept it; both are no-ops without one; accept binds the offered ref", setup())
		tr.guestJoined(trCode, heatShared(), heat())
		both := []obj{candidate("a", "durationMs", heatMs+3000), candidate("b", "durationMs", heatMs-2000)}
		rankedBoth := objs(ranked("b", -2000), ranked("a", 3000))
		tr.do(answer(both...), out{
			State: state("match", matchOf("searching", -2000), "mismatch", offer("b", -2000), "choice", nil,
				"candidates", rankedBoth),
		})
		declined := state("match", matchOf("unavailable", -2000), "mismatch", nil, "choice", nil, "candidates", rankedBoth,
			"player", "idle")
		tr.do(call("declineMismatch"), out{State: declined})
		tr.do(call("acceptMismatch"), out{State: declined})
		tr.do(call("declineMismatch"), out{State: declined})
		tr.do(advance(250), out{Send: objs(tr.status(1, "idle", matchOf("unavailable", -2000)))})
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("resolve", "Heat")),
			State:    state("match", matchOf("searching"), "mismatch", nil, "candidates", objs(), "player", "loading"),
		})
		tr.do(answer(both...), out{State: state("match", matchOf("searching", -2000), "mismatch", offer("b", -2000))})
		tr.do(heatPlayer(heatMs-2000), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "b"), out{State: state("player", "loading")})
		tr.do(call("setOffset", "ms", -2000), out{State: state("offsetMs", -2000)})
		tr.do(call("acceptMismatch"), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State: state("choice", choice("b", "b"), "match", matchOf("mismatched", -2000), "mismatch", nil,
				"candidates", rankedBoth, "offsetMs", -2000, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 1, "ready", matchOf("mismatched", -2000), -2000))})
		add(tr)
	}

	// SDK.md §9.5, §9.6, §13: a choose with a duration cancels the walk under way (the running probe) and, once the
	// walk is done, the mismatch prompt; candidates and the offset stay. A new epoch resets the offset.
	{
		tr := newTrace("choose cancels the walk under way and the mismatch prompt, and keeps the offset", setup())
		room := heatShared()
		tr.guestJoined(trCode, room, heat())
		tr.do(answer(candidate("a", "durationMs", heatMs+3000), candidate("b", "probe", true)), out{
			Resolver: objs(o("probe", "b")),
			State:    state("candidates", objs(ranked("a", 3000), ranked("b", nil)), "match", matchOf("searching"), "mismatch", nil),
		})
		tr.do(call("setOffset", "ms", 700), out{State: state("offsetMs", 700)})
		tr.do(call("choose", "id", "c2", "candidate", candidate("c", "durationMs", heatMs+200)), out{
			Resolver: objs(o("probeCancelled", "b")),
			Done:     objs(chose("c2", true)),
			State: state("choice", choice("c", "c"), "match", matchOf("matched", 200), "mismatch", nil, "offsetMs", 700,
				"candidates", objs(ranked("a", 3000), ranked("b", nil))),
		})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 1, "loading", matchOf("matched", 200), 700))})
		room.Rev, room.Epoch = 4, 2
		tr.do(recv(mediaRoom(room, heat(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 4)),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
			State:    state("room", 4, "offsetMs", 0, "choice", nil, "candidates", objs()),
		})
		tr.do(answer(candidate("a", "durationMs", heatMs+3000)), out{
			State: state("match", matchOf("searching", 3000), "mismatch", offer("a", 3000)),
		})
		tr.do(call("setOffset", "ms", -300), out{State: state("offsetMs", -300)})
		tr.do(call("choose", "id", "c3", "candidate", candidate("c", "durationMs", heatMs+200)), out{
			Done: objs(chose("c3", true)),
			State: state("choice", choice("c", "c"), "match", matchOf("matched", 200), "mismatch", nil, "offsetMs", -300,
				"candidates", objs(ranked("a", 3000))),
		})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 2, "loading", matchOf("matched", 200), -300))})
		add(tr)
	}

	// SDK.md §9.7, §12: setMedia checks the descriptor it sends: over 4096 bytes, or an ext over 1024, throws
	// invalid_message and sends nothing; a passing one goes out.
	{
		tr := newTrace("setMedia refuses a descriptor over 4096 bytes or an ext over 1024 with invalid_message and sends nothing", setup())
		tr.hostJoined()
		long := o("kind", "movie", "title", strings.Repeat("Heat", 1025), "durationMs", heatMs)
		bigExt := o("kind", "movie", "title", "Heat", "durationMs", heatMs, "ext", o("com.example.note", strings.Repeat("x", 1100)))
		tr.do(call("setMedia", "id", "c2", "media", long, "ref", "heat"), out{
			Done:  objs(done("c2", "error", "invalid_message")),
			State: state("media", nil),
		})
		tr.do(call("setMedia", "id", "c3", "media", bigExt, "ref", "heat"), out{
			Done:  objs(done("c3", "error", "invalid_message")),
			State: state("media", nil),
		})
		tr.do(call("setMedia", "id", "c4", "media", heat(), "ref", "heat"), out{
			Done: objs(done("c4")),
			Send: objs(setMediaMsg(heat())),
		})
		add(tr)
	}

	// SDK.md §13, §9.1, §10.1, §14: setOffset takes −604800000 to 604800000, else invalid_message with nothing
	// changed; it works while idle and is reset by the next joined; resolveAgain keeps it; it works while reconnecting
	// and a same-epoch resume keeps it (status carries it); a new epoch resets it.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("the offset ±604800000 works anywhere; joined and new epochs reset it; resolveAgain and a same-epoch resume keep it", s)
		tr.do(call("setOffset", "id", "c1", "ms", 604_800_001), out{
			Done:  objs(done("c1", "error", "invalid_message")),
			State: state("offsetMs", 0),
		})
		tr.do(call("setOffset", "id", "c2", "ms", -604_800_000), out{
			Done:  objs(done("c2")),
			State: state("offsetMs", -604_800_000),
		})
		room := heatShared()
		tr.do(call("join", "id", "c3", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Done:     objs(done("c3")),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(1, "loading", matchOf("searching"))),
			State:    state("offsetMs", 0),
		})
		tr.do(call("setOffset", "ms", -1500), out{State: state("offsetMs", -1500)})
		tr.do(answer(candidate("a", "durationMs", heatMs+1000)), out{State: state("match", matchOf("matched", 1000))})
		tr.do(advance(250), out{Send: objs(statusOff(tr, 1, "loading", matchOf("matched", 1000), -1500))})
		tr.do(call("resolveAgain"), out{
			Resolver: objs(o("resolve", "Heat")),
			State:    state("offsetMs", -1500),
		})
		tr.do(answer(candidate("a", "durationMs", heatMs+1000)), out{
			State: state("candidates", objs(ranked("a", 1000)), "offsetMs", -1500),
		})
		tr.do(drop(1006), out{State: state("connection", "reconnecting")})
		tr.do(call("setOffset", "ms", 250), out{State: state("offsetMs", 250)})
		tr.do(advance(250), out{
			Net:  objs(opened()),
			Send: objs(o("type", "resume", "code", trCode, "peerId", "ann", "token", token("ann"))),
		})
		room.Rev = 4
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Send:  objs(statusOff(tr, 1, "loading", matchOf("matched", 1000), 250)),
			State: state("connection", "connected", "match", matchOf("matched", 1000), "offsetMs", 250),
		})
		room.Rev, room.Epoch = 5, 2
		tr.do(recv(mediaRoom(room, heat(), "ben")), out{
			Event:    objs(evAction("media", "ben", false, 5)),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
			State:    state("room", 5, "offsetMs", 0),
		})
		tr.do(none, out{State: state("match", matchOf("unavailable"))})
		add(tr)
	}

	return cases
}
