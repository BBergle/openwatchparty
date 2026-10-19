package main

// Party traces of status and player state (spec/SDK.md §10), following the room (§11) and local commands with their
// hold (§12). Every package-level name here starts with `follow`.

// followStatus is the status message with offsetMs and the clock's round trip rttMs; nil leaves rttMs out, as before
// the first clock sample (SDK.md §10.1).
func followStatus(epoch int64, player string, match obj, offsetMs int64, rttMs any) obj {
	s := o("type", "status", "epoch", epoch, "player", player, "match", match, "offsetMs", offsetMs)
	if rttMs != nil {
		s = append(s, kv{"rttMs", rttMs})
	}
	return s
}

// followAction is room after an action at relay time atMs: its rev, phase and position. The `room` message reporting
// it carries the action: recv(room.acted(kind, by).msg()).
func followAction(room snap, rev int64, phase string, positionMs, atMs int64) snap {
	room.Rev, room.Phase, room.PositionMs, room.AtMs = rev, phase, positionMs, atMs
	return room
}

// followAnnAs is room with Ann's entry showing player and match as her last status sent them.
func followAnnAs(room snap, player string, match obj) snap {
	for i, p := range room.Participants {
		if p.PeerID == "ann" {
			return room.withPeer(i, p.reported(player, match))
		}
	}
	panic("followAnnAs: the room has no entry for ann")
}

func followEvError(code, re string) obj { return o("type", "error", "code", code, "re", re) }

func followRonin() *Media {
	return &Media{Kind: "movie", Title: "Ronin", Year: new(1998), Ids: map[string]string{"imdb": "tt0122690"},
		DurationMs: 7_320_000}
}

// followNudge runs at local 250 after guestFollowing: p1 reads 600 ms ahead of the room, and playerChanged() nudges
// it at 0.96 (SDK.md §11.2).
func followNudge(tr *trace) {
	tr.do(player("p1", "positionMs", 610_850), out{})
	tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
}

// followHostWaiting is the host flow of SDK.md §8, §9.1, §10.2, §10.3: p1 is attached with ref "heat" while the room
// has no media; setMedia at local 1000 is pending (no media yet: idle, nothing sent); at 1250 the relay's `room` of
// the new epoch carries the media and waits on Ann: it binds p1 (setRate of the room's rate), p1 is ready, and that
// epoch's first status skips the spacing. It ends at local 1250 with the room waiting at 0 and returns that room.
func followHostWaiting(tr *trace) snap {
	room := tr.hostJoined()
	tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
	tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
	tr.do(advance(1000), out{})
	tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{
		Send: objs(o("type", "setMedia", "media", heat(), "positionMs", 0)),
	})
	tr.do(advance(250), out{})
	room.Rev, room.Epoch, room.Phase, room.AtMs = 2, 1, "waiting", tr.relayNow()
	room.WaitingOn = []string{"ann"}
	tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
		Command: objs(cmd("p1", "setRate", "rate", 1)),
		Event:   objs(evAction("media", "ann", true, 2)),
		Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
		State:   state("player", "ready"),
	})
	return room
}

// followHostPlaying continues followHostWaiting: the room plays from 0 at local 2000 and the play timer plays p1 then
// (SDK.md §11.2). It returns that room; p1 and the room are both at local time − 2000.
func followHostPlaying(tr *trace) snap {
	room := followHostWaiting(tr)
	room.Rev, room.Phase, room.AtMs = 3, "playing", tr.relayNow()+750
	room.WaitingOn = []string{}
	room = followAnnAs(room, "ready", matchOf("matched", 0))
	tr.do(recv(room.msg()), out{State: state("room", 3)})
	tr.do(advance(749), out{})
	tr.do(advance(1), out{Command: objs(cmd("p1", "play"))})
	return room
}

// followSeekBehindSetRate continues guestFollowing: p1 is nudged at 0.96 from local 250; at 2000, with the seek
// cooldown over, p1 reads 28000 ms ahead and holds its next command. The step ends the nudge with a setRate of the
// room's rate, which stays running, and queues the seek to the room position + 250 behind it; that seek in flight
// makes the player loading, sent at once (SDK.md §10.2 rule 5, §10.3, §11.1, §11.2).
func followSeekBehindSetRate(tr *trace) {
	tr.guestFollowing("host", false)
	followNudge(tr)
	tr.do(advance(1750), out{})
	tr.do(hold("p1"), out{})
	tr.do(player("p1", "positionMs", 640_000), out{})
	tr.do(call("playerChanged"), out{
		Command: objs(cmd("p1", "setRate", "rate", 1)),
		Send:    objs(tr.status(2, "loading", matchOf("matched", 500))),
		State:   state("player", "loading"),
	})
}

func partyFollowTraces() []Case {
	var cases []Case
	add := func(tr *trace) { cases = append(cases, tr.cas()) }
	m500 := matchOf("matched", 500)

	// SDK.md §10.3: at most one status per 250 ms; a change inside the spacing goes out when it is up with the values
	// of that moment, and not at all when they no longer differ; every send restarts the 15000 ms heartbeat.
	{
		tr := newTrace("status: a change inside the 250 ms spacing goes out with the latest values; one undone meanwhile sends nothing", setup())
		tr.hostJoined()
		tr.do(advance(100), out{})
		tr.do(call("setOffset", "ms", 1000), out{State: state("offsetMs", 1000)})
		tr.do(advance(100), out{})
		tr.do(call("setOffset", "ms", 2000), out{})
		tr.do(advance(49), out{})
		tr.do(advance(1), out{Send: objs(followStatus(0, "idle", nil, 2000, 0))})
		tr.do(call("setOffset", "ms", 3000), out{})
		tr.do(advance(50), out{})
		tr.do(call("setOffset", "ms", 2000), out{})
		tr.do(advance(14_949), out{})
		tr.do(advance(1), out{Send: objs(followStatus(0, "idle", nil, 2000, 0))})
		add(tr)
	}

	// SDK.md §10.1, §10.3: rttMs is absent before the clock's first sample; it is read at send time and never compared,
	// so the first sample alone sends nothing and the heartbeat carries it.
	{
		s := setup()
		s.RttMs = 100
		tr := newTrace("status leaves rttMs out before the first clock sample; a round trip alone sends no status", s)
		host := ann
		host.Host = true
		room := newRoom(1, tr.relayNow(), host)
		tr.do(call("create", "id", "c1"), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "create", "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{
			Done: objs(done("c1", "value", trCode)),
			Send: objs(followStatus(0, "idle", nil, 0, nil)),
		})
		tr.do(advance(14_999), out{})
		tr.do(advance(1), out{Send: objs(followStatus(0, "idle", nil, 0, 100))})
		add(tr)
	}

	// SDK.md §8, §10.3, §9.1: a status whose epoch differs from the last one sent skips the spacing; the next change of
	// the same epoch waits for it.
	{
		tr := newTrace("the first status of a new epoch skips the 250 ms spacing; the next change waits for it", setup())
		room := newRoom(4, 990_000, ben, ann)
		tr.guestJoined(trCode, room, nil)
		tr.do(advance(100), out{})
		room.Rev, room.Epoch, room.Phase, room.AtMs = 5, 1, "paused", tr.relayNow()
		tr.do(recv(room.withMedia(heat()).acted("media", "ben").msg()), out{
			Event:    objs(evAction("media", "ben", false, 5)),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(1, "loading", matchOf("searching"))),
			State:    state("room", 5, "player", "loading", "match", matchOf("searching")),
		})
		tr.do(advance(100), out{})
		tr.do(o("answer", objs()), out{State: state("match", matchOf("unavailable"), "player", "idle")})
		tr.do(advance(149), out{})
		tr.do(advance(1), out{Send: objs(tr.status(1, "idle", matchOf("unavailable")))})
		add(tr)
	}

	// SDK.md §10.2 rule 6, §11.1, §10.1: with pongs 1000 ms late, a player bound while the room plays is loading
	// until relay time is known, and no step runs; the first sample and the tick at 1000 come together, the step finds
	// p1 in place and the status reports ready with the round trip.
	{
		s := setup()
		s.RttMs = 1000
		tr := newTrace("a bound player is loading while the room plays and relay time is unknown, and nothing steps", s)
		room := playingRoom("host")
		tr.do(call("join", "id", "c1", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Done:     objs(done("c1")),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(followStatus(2, "loading", matchOf("searching"), 0, nil)),
			State:    state("connection", "connected", "player", "loading"),
		})
		tr.do(player("p1", "positionMs", 610_000, "paused", false, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("match", m500, "player", "loading"),
		})
		tr.do(advance(250), out{Send: objs(followStatus(2, "loading", m500, 0, nil))})
		tr.do(advance(749), out{})
		tr.do(advance(1), out{Send: objs(followStatus(2, "ready", m500, 0, 1000)), State: state("player", "ready")})
		add(tr)
	}

	// SDK.md §10.2 rule 5, §10.3: a bound player whose first frame isn't shown is loading; the tick reads the adapter
	// without playerChanged().
	{
		tr := newTrace("a player whose first frame isn't shown is loading; the tick reads it", setup())
		tr.guestFollowing("host", false)
		tr.do(player("p1", "firstFrameShown", false), out{})
		tr.do(advance(249), out{})
		tr.do(advance(1), out{Send: objs(tr.status(2, "loading", m500)), State: state("player", "loading")})
		tr.do(player("p1", "firstFrameShown", true), out{})
		tr.do(call("playerChanged"), out{State: state("player", "ready")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rule 9, §11.2: a buffering player is buffering while the room is running and not held, and the
	// step leaves it alone; a room pause holds it: the step pauses it in place and it is loading while it buffers.
	{
		tr := newTrace("a buffering player is buffering while the room runs and loading while the room holds", setup())
		room := tr.guestFollowing("host", false)
		tr.do(advance(750), out{})
		tr.do(player("p1", "buffering", true), out{})
		tr.do(call("playerChanged"), out{Send: objs(tr.status(2, "buffering", m500)), State: state("player", "buffering")})
		room = followAnnAs(followAction(room, 8, "paused", 611_250, tr.relayNow()), "buffering", m500)
		tr.do(recv(room.acted("pause", "ben").msg()), out{
			Event:   objs(evAction("pause", "ben", false, 8)),
			Command: objs(cmd("p1", "pause")),
			State:   state("player", "loading"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "loading", m500))})
		tr.do(player("p1", "buffering", false), out{})
		tr.do(call("playerChanged"), out{State: state("player", "ready")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rule 5, §10.3, §11.1, §11.2: an accepted snapshot runs a step at once; its seek makes the player
	// loading, sent at once, until the seek has landed; ready waits out the spacing.
	{
		tr := newTrace("a snapshot whose step seeks sends loading at once; ready follows once the seek has landed", setup())
		room := tr.guestFollowing("host", false)
		tr.do(advance(1750), out{})
		tr.do(hold("p1"), out{})
		room = followAnnAs(followAction(room, 8, "playing", 900_000, tr.relayNow()), "ready", m500)
		tr.do(recv(room.acted("seek", "ben").msg()), out{
			Event:   objs(evAction("seek", "ben", false, 8)),
			Command: objs(cmd("p1", "seek", "positionMs", 900_250)),
			Send:    objs(tr.status(2, "loading", m500)),
			State:   state("player", "loading"),
		})
		tr.do(finish("p1"), out{State: state("player", "ready")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rule 5, §11.1: a seek queued behind a running command counts as in flight: loading until it has
	// run; it runs once the setRate before it finishes.
	{
		tr := newTrace("a step's seek queued behind a running setRate counts as in flight: loading until it has run", setup())
		followSeekBehindSetRate(tr)
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "seek", "positionMs", 612_250)), State: state("player", "ready")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rule 5, §11.2: attaching another adapter drops the old one's queued seek, which never started, so
	// no seek is in flight: the new adapter binds in place and is ready; the old one's running setRate finishes and
	// nothing else reaches it (no nudge, so no restore).
	{
		tr := newTrace("a seek dropped by a release never ran: the next adapter is ready", setup())
		followSeekBehindSetRate(tr)
		tr.do(player("p2", "positionMs", 612_000, "paused", false, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p2", "ref", "heat-a"), out{
			Command: objs(cmd("p2", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(finish("p1"), out{})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rules 7 and 8, §15: blocked ends when the room's phase isn't playing: a room pause near p1 makes it
	// ready; once the room plays again the step's play is refused again, which blocks it again with another
	// PlayBlocked.
	{
		tr := newTrace("a room pause ends blocked; a play refused after the room plays again emits PlayBlocked again", setup())
		room := tr.guestFollowing("host", true)
		tr.do(advance(250), out{Command: objs(cmd("p1", "play"))})
		room = followAnnAs(followAction(room, 8, "paused", 610_500, tr.relayNow()), "loading", m500)
		tr.do(recv(room.acted("pause", "ben").msg()), out{
			Event: objs(evAction("pause", "ben", false, 8)),
			Send:  objs(tr.status(2, "ready", m500)),
			State: state("player", "ready"),
		})
		tr.do(advance(500), out{})
		room = followAnnAs(followAction(room, 9, "playing", 610_500, tr.relayNow()), "ready", m500)
		tr.do(recv(room.acted("play", "ben").msg()), out{
			Event:   objs(evAction("play", "ben", false, 9), o("type", "playBlocked")),
			Command: objs(cmd("p1", "play")),
			Send:    objs(tr.status(2, "loading", m500)),
			State:   state("player", "loading"),
		})
		add(tr)
	}

	// SDK.md §10.2, §11.2, §15: attaching another adapter releases and unbinds the blocked one, which ends blocked; the
	// new adapter binds, starts without a seek cooldown, and its refused play is reported with PlayBlocked again;
	// the tick's next play is refused without another event.
	{
		tr := newTrace("unbinding ends blocked: another adapter that refuses to play is reported again", setup())
		tr.guestFollowing("host", true)
		tr.do(player("p2", "durationMs", heatMs+500, "canSetRate", true, "playThrows", true), out{})
		tr.do(call("attachPlayer", "player", "p2", "ref", "heat-a"), out{
			Command: objs(cmd("p2", "setRate", "rate", 1), cmd("p2", "seek", "positionMs", 610_500), cmd("p2", "play")),
			Event:   objs(o("type", "playBlocked")),
			State:   state("player", "loading"),
		})
		tr.do(advance(250), out{Command: objs(cmd("p2", "play"))})
		add(tr)
	}

	// SDK.md §10.2 rule 3, §7.2: detachPlayer after the end sets away, but every attempt starts with away false, so
	// the next room's first status is loading (no adapter, not bound), not idle.
	{
		tr := newTrace("away is false from the start of every attempt: a detachPlayer after the end doesn't carry into the next room", setup())
		tr.hostJoined()
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended", "player", "idle"),
		})
		tr.do(call("detachPlayer", "id", "d1"), out{Done: objs(done("d1")), State: state("player", "idle")})
		room := newRoom(3, tr.relayNow(), ben, ann)
		room.Epoch, room.Phase = 1, "paused"
		tr.do(call("join", "id", "c2", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Done:     objs(done("c2")),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(1, "loading", matchOf("searching"))),
			State:    state("connection", "connected", "player", "loading"),
		})
		tr.do(o("answer", objs()), out{State: state("match", matchOf("unavailable"), "player", "idle")})
		tr.do(advance(250), out{Send: objs(tr.status(1, "idle", matchOf("unavailable")))})
		add(tr)
	}

	// SDK.md §10.2, §9.6: an adapter attached with the ref of a choice a later choose replaced stays unbound: no
	// command reaches it, also on ticks, and the player is loading.
	{
		tr := newTrace("an adapter attached with the ref of a superseded choice stays unbound and gets no command", setup())
		tr.guestJoined(trCode, playingRoom("host"), heat())
		tr.do(answer(candidate("a", "durationMs", heatMs)), out{
			State: state("match", matchOf("matched", 0), "choice", choice("a", "a"), "player", "loading"),
		})
		tr.do(call("choose", "id", "k1", "candidate", candidate("b", "durationMs", heatMs)), out{
			Done:  objs(done("k1", "value", true)),
			State: state("match", matchOf("matched", 0), "choice", choice("b", "b")),
		})
		tr.do(player("p1", "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "a"), out{State: state("player", "loading")})
		tr.do(advance(1000), out{Send: objs(tr.status(2, "loading", matchOf("matched", 0)))})
		add(tr)
	}

	// SDK.md §10.2, §11.1, §12, §15: setMedia unbinds without a pause and is pending; the relay's refusal (re
	// setMedia) is an Error event and ends it: the adapter binds again to the current own choice (setRate of the
	// room's rate), the status goes out at once and the step runs at once, playing p1, before the next tick. No
	// command hold follows setMedia.
	{
		tr := newTrace("a setMedia refused by the relay stops being pending: the adapter binds, status goes out and the step runs at once", setup())
		room := followHostWaiting(tr)
		ronin := o("type", "setMedia", "media", followRonin(), "positionMs", 0)
		tr.do(call("setMedia", "media", followRonin(), "ref", "ronin"), out{Send: objs(ronin), State: state("player", "loading")})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("matched", 0)))})
		room.Rev, room.Phase, room.AtMs = 3, "playing", tr.relayNow()
		room.WaitingOn = []string{}
		room = followAnnAs(room, "loading", matchOf("matched", 0))
		tr.do(recv(room.msg()), out{State: state("room", 3, "player", "loading")})
		tr.do(advance(250), out{})
		tr.do(recv(refusal("rate_limited", "slow down", "setMedia")), out{
			Event:   objs(followEvError("rate_limited", "setMedia")),
			Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "play")),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			State:   state("player", "ready"),
		})
		add(tr)
	}

	// SDK.md §10.2, §7.2: an adapter attached while idle stays attached through a refused join and a cancelled one;
	// the session a join starts follows it: the joined's other media pauses it once, the match binds it.
	{
		tr := newTrace("an adapter attached while idle outlives a refused and a cancelled join and is followed in the session", setup())
		tr.do(player("p1", "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{State: state("connection", "idle", "player", "idle")})
		joinMsg := o("type", "join", "code", trCode, "name", trName, "client", trClient)
		tr.do(call("join", "id", "c1", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(joinMsg),
			State: state("connection", "connecting"),
		})
		tr.do(recv(refusal("room_full", "the room is full", "join")), out{
			Done:  objs(done("c1", "error", "room_full")),
			Net:   objs(closed(1000)),
			State: state("connection", "idle"),
		})
		tr.do(nextOpen("hang"), out{})
		tr.do(call("join", "id", "c2", "code", trCode), out{Net: objs(opened()), State: state("connection", "connecting")})
		tr.do(call("leave"), out{Done: objs(done("c2", "error", "cancelled")), State: state("connection", "idle")})
		tr.do(call("join", "id", "c3", "code", trCode), out{Net: objs(opened()), Send: objs(joinMsg)})
		tr.do(recv(joinedMsg("ann", playingRoom("host"), heat(), tr.relayNow())), out{
			Done:     objs(done("c3")),
			Resolver: objs(o("resolve", "Heat")),
			Command:  objs(cmd("p1", "pause")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
		})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "seek", "positionMs", 610_250), cmd("p1", "play")),
			State:   state("match", m500, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §7.4, §10.2, §11.2: the end releases the adapter (no nudge, so nothing reaches it); the next session
	// doesn't follow it: no pause on the joined's other media, nothing on the match, loading (not away); attached
	// again, it binds and is followed.
	{
		tr := newTrace("the end releases the adapter: the next session follows it only once attached again, loading until then", setup())
		tr.guestFollowing("host", false)
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended", "player", "idle"),
		})
		tr.do(call("join", "id", "c2", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", playingRoom("host"), heat(), tr.relayNow())), out{
			Done:     objs(done("c2")),
			Resolver: objs(o("resolve", "Heat")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
		})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{State: state("match", m500, "player", "loading")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "loading", m500))})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §7.2, §10.2, §9.1: an adapter attached again after the end is followed in the session the next join
	// starts: the joined's other media pauses it once, and the match binds it and plays it from where it stopped.
	{
		tr := newTrace("an adapter attached again after the end is followed in the session the next join starts", setup())
		tr.guestFollowing("host", false)
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
		})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{State: state("connection", "ended", "player", "idle")})
		tr.do(call("join", "id", "c2", "code", trCode), out{
			Net:  objs(opened()),
			Send: objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
		})
		tr.do(recv(joinedMsg("ann", playingRoom("host"), heat(), tr.relayNow())), out{
			Done:     objs(done("c2")),
			Resolver: objs(o("resolve", "Heat")),
			Command:  objs(cmd("p1", "pause")),
			Send:     objs(tr.status(2, "loading", matchOf("searching"))),
		})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "play")),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §10.2 rule 7, §11.1, §11.2: a room pause far from p1 holds it: loading at once (held more than 500 ms
	// away), the step pauses and seeks it to the hold point, then ready; a room play from there plays it.
	{
		tr := newTrace("the bound adapter follows a room pause with pause and seek, and a room play with play", setup())
		room := tr.guestFollowing("host", false)
		tr.do(advance(1750), out{})
		room = followAnnAs(followAction(room, 8, "paused", 30_000, tr.relayNow()), "ready", m500)
		tr.do(recv(room.acted("pause", "ben").msg()), out{
			Event:   objs(evAction("pause", "ben", false, 8)),
			Command: objs(cmd("p1", "pause"), cmd("p1", "seek", "positionMs", 30_000)),
			Send:    objs(tr.status(2, "loading", m500)),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		tr.do(advance(250), out{})
		room = followAnnAs(followAction(room, 9, "playing", 30_000, tr.relayNow()), "ready", m500)
		tr.do(recv(room.acted("play", "ben").msg()), out{
			Event:   objs(evAction("play", "ben", false, 9)),
			Command: objs(cmd("p1", "play")),
			State:   state("player", "ready"),
		})
		add(tr)
	}

	// SDK.md §11.1, §11.2, §12: attaching another adapter releases the nudged one: its queued local seek is dropped,
	// its running pause keeps running, then one setRate restores the room's rate; the new adapter's lane doesn't wait
	// for it. detachPlayer's completion settles only once the released lanes are empty; away reports idle.
	{
		tr := newTrace("attachPlayer with another adapter drops the old one's queued commands; its hung command holds up only the release completion", setup())
		tr.guestFollowing("everyone", false)
		followNudge(tr)
		tr.do(hold("p1"), out{})
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_850))})
		tr.do(call("seek", "ms", 700_000), out{Send: objs(o("type", "seek", "positionMs", 700_000))})
		tr.do(player("p2", "positionMs", 610_250, "paused", false, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p2", "ref", "heat-a"), out{
			Command: objs(cmd("p2", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(call("detachPlayer", "id", "d1"), out{State: state("player", "idle")})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1)), Done: objs(done("d1"))})
		tr.do(advance(250), out{Send: objs(tr.status(2, "idle", m500))})
		add(tr)
	}

	// SDK.md §7.4, §11.2: the end (closed kicked) releases the nudged adapter: its queued seek is dropped and, after
	// its running pause, one setRate of the room's rate × the time scale (1) runs, also though close() came after;
	// close()'s completion settles after it, and nothing more reaches the adapter.
	{
		tr := newTrace("the end while nudging drops queued commands and restores the room rate once, also after close()", setup())
		tr.guestFollowing("everyone", false)
		followNudge(tr)
		tr.do(hold("p1"), out{})
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_850))})
		tr.do(call("seek", "ms", 700_000), out{Send: objs(o("type", "seek", "positionMs", 700_000))})
		tr.do(recv(o("type", "closed", "reason", "kicked")), out{
			Net:   objs(closed(1000)),
			Event: objs(evEnded("kicked", nil)),
			State: state("connection", "ended", "endReason", "kicked", "player", "idle"),
		})
		tr.do(call("close", "id", "x1"), out{})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1)), Done: objs(done("x1"))})
		tr.do(advance(5000), out{})
		add(tr)
	}

	// SDK.md §10.2, §11.2: with no released adapter running a command, detachPlayer's and close()'s completions
	// settle at once, here on an idle party.
	{
		tr := newTrace("with nothing running the release completions of detachPlayer and close settle at once", setup())
		tr.do(player("p1"), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(call("detachPlayer", "id", "d1"), out{Done: objs(done("d1")), State: state("player", "idle")})
		tr.do(call("detachPlayer", "id", "d2"), out{Done: objs(done("d2"))})
		tr.do(call("close", "id", "x1"), out{Done: objs(done("x1")), State: state("connection", "idle")})
		add(tr)
	}

	// SDK.md §10.2, §11.2: detachPlayer naming an adapter that isn't the attached one changes nothing: its completion
	// settles at once and the attached adapter stays bound and followed.
	{
		tr := newTrace("detachPlayer naming an adapter that isn't attached changes nothing", setup())
		tr.guestFollowing("host", false)
		tr.do(player("p2"), out{})
		tr.do(call("detachPlayer", "id", "d1", "player", "p2"), out{Done: objs(done("d1")), State: state("player", "ready")})
		followNudge(tr)
		add(tr)
	}

	// SDK.md §10.2, §11.2: attachPlayer with the adapter already attached keeps its lane and its seek cooldown: 500 ms
	// after its seek, a 89500 ms drift gets no seek; another adapter starts without a cooldown and is seeked at once.
	{
		tr := newTrace("re-attaching the same adapter keeps its seek cooldown; another adapter starts without one", setup())
		tr.guestFollowing("host", false)
		tr.do(advance(250), out{})
		tr.do(player("p1", "positionMs", 700_000), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{State: state("player", "ready")})
		tr.do(player("p2", "positionMs", 700_000, "paused", false, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p2", "ref", "heat-a"), out{
			Command: objs(cmd("p2", "setRate", "rate", 1), cmd("p2", "seek", "positionMs", 610_750)),
			Send:    objs(tr.status(2, "loading", m500)),
			State:   state("player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §11.2, §10.2 rule 7: seekAccuracyMs raises the seek thresholds: a 3000 ms drift with accuracy 5000 is
	// nudged, not seeked; held 3000 ms from the hold point the player is ready and isn't seeked. With accuracy 0 the
	// same distance makes it loading, and the hold's seek waits for the cooldown (2000 ms after the seek at 0).
	{
		tr := newTrace("seekAccuracyMs raises the seek and held-loading thresholds; the held seek waits for the cooldown", setup())
		room := tr.guestFollowing("host", false)
		tr.do(advance(750), out{})
		tr.do(player("p1", "positionMs", 614_000, "seekAccuracyMs", 5000), out{})
		tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
		room = followAnnAs(followAction(room, 8, "paused", 611_000, tr.relayNow()), "ready", m500)
		tr.do(recv(room.acted("pause", "ben").msg()), out{
			Event:   objs(evAction("pause", "ben", false, 8)),
			Command: objs(cmd("p1", "pause"), cmd("p1", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(player("p1", "seekAccuracyMs", 0), out{})
		tr.do(call("playerChanged"), out{Send: objs(tr.status(2, "loading", m500)), State: state("player", "loading")})
		tr.do(advance(999), out{})
		tr.do(advance(1), out{
			Command: objs(cmd("p1", "seek", "positionMs", 611_000)),
			Send:    objs(tr.status(2, "ready", m500)),
			State:   state("player", "ready"),
		})
		add(tr)
	}

	// SDK.md §11.2, §13: with an offset of −3100 the local file starts 3100 ms after the room's atMs: the step holds
	// p1 at 0 and arms the play timer, which plays it then, between two ticks.
	{
		tr := newTrace("a negative offset delays the local start: the play timer plays when the room reaches it", setup())
		room := newRoom(7, 1_000_000, ben.reported("ready", matchOf("matched", 0)), ann)
		room.Epoch, room.Phase = 2, "playing"
		tr.guestJoined(trCode, room, heat())
		tr.do(call("setOffset", "ms", -3100), out{State: state("offsetMs", -3100)})
		tr.do(player("p1", "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			State:   state("match", m500, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(followStatus(2, "ready", m500, -3100, 0))})
		tr.do(advance(2849), out{})
		tr.do(advance(1), out{Command: objs(cmd("p1", "play"))})
		add(tr)
	}

	// SDK.md §11.1, §11.2, §13, §10.3, §7.4: setOffset runs a step at once. Offset 500 puts p1 500 ms behind its
	// target: status goes out with the offset and p1 is nudged at 1.04. leave() releases the nudged adapter: one
	// setRate of the room's rate.
	{
		tr := newTrace("setOffset runs a step at once; the end then restores the room rate on the nudged adapter", setup())
		followHostPlaying(tr)
		tr.do(advance(1000), out{})
		tr.do(call("setOffset", "ms", 500), out{
			Command: objs(cmd("p1", "setRate", "rate", 1.04)),
			Send:    objs(followStatus(1, "ready", matchOf("matched", 0), 500, 0)),
		})
		tr.do(call("leave"), out{
			Send:    objs(o("type", "leave")),
			Net:     objs(closed(1000)),
			Event:   objs(evEnded("left", nil)),
			Command: objs(cmd("p1", "setRate", "rate", 1)),
		})
		add(tr)
	}

	// SDK.md §12: every command checks membership first (not_in_room, even for a bad value), then media, then
	// control (forbidden before the value), then the value; host commands check the host before the participant or
	// descriptor. A failed check sends nothing; setRate takes 0.5 to 2.0.
	{
		s := setup()
		s.Resolver = false
		tr := newTrace("commands check membership, then control, then the value; host commands the host first; a failed check sends nothing", s)
		room := playingRoom("host")
		tr.do(call("setRate", "id", "c1", "rate", 5), out{Done: objs(done("c1", "error", "not_in_room"))})
		tr.do(call("pause", "id", "c2"), out{Done: objs(done("c2", "error", "not_in_room"))})
		tr.do(call("clearMedia", "id", "c3"), out{Done: objs(done("c3", "error", "not_in_room"))})
		tr.do(call("join", "id", "c4", "code", trCode), out{
			Net:   objs(opened()),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", room, heat(), tr.relayNow())), out{
			Done:  objs(done("c4")),
			Send:  objs(tr.status(2, "idle", matchOf("unavailable"))),
			State: state("match", matchOf("unavailable"), "canControl", false),
		})
		tr.do(call("seek", "id", "c5", "ms", -1), out{Done: objs(done("c5", "error", "forbidden"))})
		tr.do(call("setRate", "id", "c6", "rate", 5), out{Done: objs(done("c6", "error", "forbidden"))})
		tr.do(call("transferHost", "id", "c7", "peerId", "zed"), out{Done: objs(done("c7", "error", "forbidden"))})
		tr.do(call("setMedia", "id", "c8", "media", heat(), "ref", "heat"), out{Done: objs(done("c8", "error", "forbidden"))})
		room.Rev, room.Control = 8, "everyone"
		room = followAnnAs(room, "idle", matchOf("unavailable"))
		tr.do(recv(room.msg()), out{State: state("room", 8, "canControl", true)})
		tr.do(call("seek", "id", "c9", "ms", -1), out{Done: objs(done("c9", "error", "invalid_message"))})
		tr.do(call("setRate", "id", "c10", "rate", 0.49), out{Done: objs(done("c10", "error", "invalid_message"))})
		tr.do(call("setRate", "id", "c11", "rate", 2.01), out{Done: objs(done("c11", "error", "invalid_message"))})
		tr.do(call("setRate", "rate", 0.5), out{Send: objs(o("type", "rate", "rate", 0.5))})
		tr.do(call("setRate", "rate", 2.0), out{Send: objs(o("type", "rate", "rate", 2.0))})
		add(tr)
	}

	// SDK.md §12: the host commands send their messages; transferHost and kick accept a disconnected participant and
	// the party itself; play and the host commands hold no step (playerChanged() nudges right after); setRate's
	// local effect sets the adapter's rate and holds the steps.
	{
		tr := newTrace("host commands send their messages and hold nothing; the participant check takes a disconnected entry and the party itself", setup())
		room := followHostPlaying(tr)
		away := newPeer("ben", "Ben", false)
		away.Connected = false
		room.Rev = 4
		room.Participants = []peer{room.Participants[0], away, cat}
		tr.do(recv(room.msg()), out{Event: objs(evJoined("ben"), evJoined("cat")), State: state("room", 4)})
		tr.do(call("transferHost", "id", "h1", "peerId", "zed"), out{Done: objs(done("h1", "error", "invalid_message"))})
		tr.do(call("kick", "id", "h2", "peerId", "zed"), out{Done: objs(done("h2", "error", "invalid_message"))})
		tr.do(call("transferHost", "peerId", "ben"), out{Send: objs(o("type", "transferHost", "peerId", "ben"))})
		tr.do(call("kick", "peerId", "ann"), out{Send: objs(o("type", "kick", "peerId", "ann"))})
		tr.do(call("setControl", "mode", "everyone"), out{Send: objs(o("type", "setControl", "mode", "everyone"))})
		tr.do(call("lock", "locked", true), out{Send: objs(o("type", "lock", "locked", true))})
		tr.do(call("play"), out{Send: objs(o("type", "play"))})
		tr.do(player("p1", "positionMs", 600), out{})
		tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
		tr.do(call("setRate", "rate", 1.5), out{
			Command: objs(cmd("p1", "setRate", "rate", 1.5)),
			Send:    objs(o("type", "rate", "rate", 1.5)),
		})
		tr.do(player("p1", "positionMs", 5000), out{})
		tr.do(call("playerChanged"), out{})
		tr.do(call("clearMedia"), out{Send: objs(o("type", "clearMedia"))})
		tr.do(call("end"), out{Send: objs(o("type", "end"))})
		add(tr)
	}

	// SDK.md §12, §10.2: pause() pauses the adapter and sends its position only while bound; attached with another
	// ref the adapter is unbound (loading) and pause() sends no position and doesn't touch it.
	{
		tr := newTrace("pause() pauses the adapter and sends positionMs only while it is bound", setup())
		tr.guestFollowing("everyone", false)
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_500))})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-b"), out{State: state("player", "loading")})
		tr.do(call("pause"), out{Send: objs(o("type", "pause"))})
		tr.do(advance(250), out{Send: objs(tr.status(2, "loading", m500))})
		add(tr)
	}

	// SDK.md §12, §11.2: a pause the relay never confirms holds the steps, ticks included, for 2000 ms; then a step
	// runs: 1750 ms behind the playing room, p1 is nudged at 1.04 and played.
	{
		tr := newTrace("a pause the relay never confirms holds the steps for 2000 ms", setup())
		tr.guestFollowing("everyone", false)
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_500))})
		tr.do(advance(1999), out{})
		tr.do(advance(1), out{Command: objs(cmd("p1", "setRate", "rate", 1.04), cmd("p1", "play"))})
		add(tr)
	}

	// SDK.md §12, §15: an error with re pause ends a pause's hold and runs a step; an error about another command is
	// only an Error event.
	{
		tr := newTrace("an error about the held command ends the hold and steps; an error about another command doesn't", setup())
		tr.guestFollowing("everyone", false)
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_500))})
		tr.do(advance(250), out{})
		tr.do(recv(refusal("rate_limited", "slow down", "seek")), out{Event: objs(followEvError("rate_limited", "seek"))})
		tr.do(advance(500), out{})
		tr.do(recv(refusal("rate_limited", "slow down", "pause")), out{
			Event:   objs(followEvError("rate_limited", "pause")),
			Command: objs(cmd("p1", "setRate", "rate", 1.04), cmd("p1", "play")),
		})
		add(tr)
	}

	// SDK.md §12, §11.2: a new command replaces the hold: setRate 250 ms after pause sets the bound adapter's rate
	// and holds until 2000 ms after itself, not after the pause; the step then nudges from the applied 1.5.
	{
		tr := newTrace("a new command replaces the hold: a setRate after a pause holds until 2000 ms after the setRate", setup())
		tr.guestFollowing("everyone", false)
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_500))})
		tr.do(advance(250), out{})
		tr.do(call("setRate", "rate", 1.5), out{
			Command: objs(cmd("p1", "setRate", "rate", 1.5)),
			Send:    objs(o("type", "rate", "rate", 1.5)),
		})
		tr.do(advance(1749), out{})
		tr.do(advance(1), out{})
		tr.do(advance(249), out{})
		tr.do(advance(1), out{Command: objs(cmd("p1", "setRate", "rate", 1.04), cmd("p1", "play"))})
		add(tr)
	}

	// SDK.md §12, §10.2 rule 5, §16.6, §11.2: seek(ms) seeks the bound adapter to ms and counts as an SDK seek: the
	// player state is loading from the call until the seek lands (published at once, sent when the spacing is up),
	// and the step's seek cooldown runs from the call. An error with re seek at 2000 ends the hold: 1750 ms after the
	// local seek a 9500 ms drift gets no seek; the tick at 2500 seeks.
	{
		tr := newTrace("a local seek counts as an SDK seek: loading until it lands, and the step's seek cooldown starts at the call", setup())
		tr.guestFollowing("everyone", false)
		tr.do(hold("p1"), out{})
		tr.do(call("seek", "ms", 620_000), out{
			Command: objs(cmd("p1", "seek", "positionMs", 620_000)),
			Send:    objs(o("type", "seek", "positionMs", 620_000)),
			State:   state("player", "loading"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "loading", m500))})
		tr.do(finish("p1"), out{State: state("player", "ready")})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		tr.do(advance(1250), out{})
		tr.do(recv(refusal("rate_limited", "slow down", "seek")), out{Event: objs(followEvError("rate_limited", "seek"))})
		tr.do(advance(500), out{
			Command: objs(cmd("p1", "seek", "positionMs", 612_750)),
			Send:    objs(tr.status(2, "loading", m500)),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §12, §13, §11.2: with offset 3000 the step targets the room position + 3000; pause sends the adapter's
	// position − offset and seek(ms) seeks the adapter to ms and sends ms − offset.
	{
		tr := newTrace("with an offset, the step targets room + offset; pause and seek send the local position − offset", setup())
		tr.guestJoined(trCode, playingRoom("everyone"), heat())
		tr.do(call("setOffset", "ms", 3000), out{State: state("offsetMs", 3000)})
		tr.do(player("p1", "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{})
		tr.do(answer(candidate("heat-a", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "seek", "positionMs", 613_250), cmd("p1", "play")),
			State:   state("match", m500, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(followStatus(2, "ready", m500, 3000, 0))})
		tr.do(call("pause"), out{Command: objs(cmd("p1", "pause")), Send: objs(o("type", "pause", "positionMs", 610_500))})
		tr.do(call("seek", "ms", 800_000), out{
			Command: objs(cmd("p1", "seek", "positionMs", 800_000)),
			Send:    objs(o("type", "seek", "positionMs", 797_000)),
		})
		add(tr)
	}

	return cases
}
