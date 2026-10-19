package main

// Party traces of cases the other cases_party*.go files leave out: a step, value or order of the SDK.md rules there
// that only a per-SDK test pinned. The ones some SDK's test pinned before it was dropped come first, then the rest.
// As in cases_party.go, every output is written by hand from SDK.md, and the comment above each trace names the
// sections it pins. Every package-level name here starts with `gap`.

// gapRoomAtRate continues guestFollowing at local 250: Ben sets the rate to 1.5 with p1 250 ms ahead, inside the
// nudge threshold, so the step sets the room rate on p1 (SDK.md §11.2, §15). It returns that room.
func gapRoomAtRate(tr *trace, room snap) snap {
	room = followAnnAs(followAction(room, 8, "playing", 610_250, tr.relayNow()), "ready", matchOf("matched", 500))
	room.Rate = 1.5
	tr.do(recv(room.acted("rate", "ben").msg()), out{
		Event:   objs(evAction("rate", "ben", false, 8)),
		Command: objs(cmd("p1", "setRate", "rate", 1.5)),
		State:   state("room", 8, "player", "ready"),
	})
	return room
}

// gapHeldNudge holds p1's next command and makes it read 600 ms ahead: playerChanged() nudges it at the room rate
// × 0.96, and that setRate stays running (SDK.md §11.2).
func gapHeldNudge(tr *trace, rate float64) {
	tr.do(hold("p1"), out{})
	tr.do(player("p1", "positionMs", 610_850), out{})
	tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", rate))})
}

// gapHeatShared is the room a guest joins at local 0: Ben shared Heat (epoch 1) and paused it at 0.
func gapHeatShared() snap {
	r := newRoom(3, 990_000, ben.reported("ready", matchOf("matched", 0)), ann)
	r.Epoch, r.Phase = 1, "paused"
	return r
}

func partyGapTraces() []Case {
	var cases []Case
	add := func(tr *trace) { cases = append(cases, tr.cas()) }
	annHost := ann
	annHost.Host = true
	m500 := matchOf("matched", 500)
	chose := func(id string, ok bool) obj { return done(id, "value", ok) }

	// SDK.md §9.6, §10.2, §11.1, §11.2: the adapter attached before the match with the ref of the file it has open
	// binds when the walk chooses the candidate holding that ref, whose id differs from it: the bind's setRate, then
	// the step's seek 250 ms ahead of the room and play.
	{
		tr := newTrace("a matching outcome binds an adapter attached with the chosen candidate's ref, which differs from its id", setup())
		tr.guestJoined(trCode, playingRoom("host"), heat())
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat.mkv"), out{State: state("player", "loading")})
		tr.do(answer(candidate("heat-a", "ref", "heat.mkv", "durationMs", heatMs+500)), out{
			Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "seek", "positionMs", 610_250), cmd("p1", "play")),
			State: state("match", m500, "choice", choice("heat-a", "heat.mkv"), "candidates", objs(ranked("heat-a", 500)),
				"player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", m500))})
		add(tr)
	}

	// SDK.md §7.4, §11.2, §12: the room plays at rate 1.5 and p1 is nudged at 1.44, that setRate still running; a
	// local setRate(1.25) queues its setRate behind it. close() leaves and ends the party, which releases p1: the
	// queued setRate is dropped and, once the nudge finishes, one setRate of the room rate 1.5 (not the requested
	// 1.25) runs on its lane. close()'s completion settles only after that restore has finished too, and nothing more
	// reaches p1.
	{
		tr := newTrace("close() while nudging at room rate 1.5 drops a queued local setRate and restores 1.5 once; its completion waits for the restore", setup())
		gapRoomAtRate(tr, tr.guestFollowing("everyone", false))
		gapHeldNudge(tr, 1.44)
		tr.do(call("setRate", "rate", 1.25), out{Send: objs(o("type", "rate", "rate", 1.25))})
		tr.do(call("close", "id", "x1"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended", "endReason", "left", "player", "idle"),
		})
		tr.do(hold("p1"), out{})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1.5))})
		tr.do(finish("p1"), out{Done: objs(done("x1"))})
		tr.do(advance(5000), out{})
		add(tr)
	}

	// SDK.md §7.2, §7.4, §14: close() during a reconnect attempt ends the party left and settles at once (no
	// adapter); the connection that sent resume is kept for its answer, and a joined then gets leave and a close
	// with 1000; nothing follows.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("close() during a reconnect attempt: a late joined on the connection that sent resume gets leave and a close with 1000", s)
		room := tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		res := rsEnded("left", nil)
		res.Done = objs(done("c2"))
		tr.do(call("close", "id", "c2"), res)
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{Send: objs(o("type", "leave")), Net: objs(closed(1000))})
		tr.do(advance(20_000), out{})
		add(tr)
	}

	// SDK.md §7.4, §14: the end stops every timer: after leave() answered a reconnect attempt's joined (still
	// waiting for its clock sample, round trip 1000 ms) with leave, no further attempt starts, nothing is drawn, and
	// the window passes without ending the party again.
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0}
		tr := newTrace("the end after a reconnect attempt's joined got leave: no further attempt and no window end", s)
		room := tr.rsCreated()
		tr.do(advance(2000), out{})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{State: state("connection", "reconnecting", "room", 1)})
		res := rsEnded("left", nil)
		res.Send = objs(o("type", "leave"))
		res.Net = objs(closed(1000))
		tr.do(call("leave"), res)
		tr.do(advance(300_000), out{})
		add(tr)
	}

	// SDK.md §9.6, §10.2, §11.1: choosing another candidate whose ref is the bound adapter's keeps it bound with no
	// command, changing the choice's id and the match only; choosing a file with another ref unbinds it, and the
	// unbound adapter gets no command when it drifts 89 s, over the ticks that follow.
	{
		tr := newTrace("choosing another id with the bound file's ref keeps the adapter bound; an unbound adapter gets no command when it drifts", setup())
		tr.guestFollowing("host", false)
		tr.do(call("choose", "id", "c2", "candidate", candidate("heat-b", "ref", "heat-a", "durationMs", heatMs+1000)), out{
			Done:  objs(chose("c2", true)),
			State: state("choice", choice("heat-b", "heat-a"), "match", matchOf("matched", 1000), "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(tr.status(2, "ready", matchOf("matched", 1000)))})
		tr.do(call("choose", "id", "c3", "candidate", candidate("heat-c", "durationMs", heatMs)), out{
			Done:  objs(chose("c3", true)),
			State: state("choice", choice("heat-c", "heat-c"), "match", matchOf("matched", 0), "player", "loading"),
		})
		tr.do(player("p1", "positionMs", 700_000), out{})
		tr.do(advance(1000), out{Send: objs(tr.status(2, "loading", matchOf("matched", 0)))})
		add(tr)
	}

	// SDK.md §9.2, §9.6: a duration counts only as an int from 1 to 604800000. choose without one and without a
	// probe (durationMs 0 or 604800001) is false; a probe answering 0 or −1 makes it false; each false choose
	// changes nothing, so the earlier choice and match stay. A probe answering 604800000 counts, also for a
	// candidate whose durationMs (−1) doesn't.
	{
		tr := newTrace("choose without a counting duration or probe, or with a probe answering 0 or -1, is false and keeps the earlier choice; a probe answer of 604800000 counts", setup())
		tr.guestJoined(trCode, gapHeatShared(), heat())
		tr.do(answer(objs()...), out{State: state("match", matchOf("unavailable"), "player", "idle")})
		tr.do(advance(250), out{Send: objs(tr.status(1, "idle", matchOf("unavailable")))})
		kept := state("choice", choice("a", "a"), "match", m500)
		tr.do(call("choose", "id", "c2", "candidate", candidate("a", "durationMs", heatMs+500)), out{
			Done:  objs(chose("c2", true)),
			State: state("choice", choice("a", "a"), "match", m500, "player", "loading"),
		})
		tr.do(call("choose", "id", "c3", "candidate", candidate("b", "durationMs", 0)), out{Done: objs(chose("c3", false)), State: kept})
		tr.do(call("choose", "id", "c4", "candidate", candidate("c", "durationMs", 604_800_001)), out{
			Done:  objs(chose("c4", false)),
			State: kept,
		})
		tr.do(call("choose", "id", "c5", "candidate", candidate("d", "probe", true)), out{Resolver: objs(o("probe", "d"))})
		tr.do(probeAnswer("d", 0), out{Done: objs(chose("c5", false)), State: kept})
		tr.do(call("choose", "id", "c6", "candidate", candidate("e", "probe", true)), out{Resolver: objs(o("probe", "e"))})
		tr.do(probeAnswer("e", -1), out{Done: objs(chose("c6", false)), State: kept})
		tr.do(call("choose", "id", "c7", "candidate", candidate("f", "durationMs", -1, "probe", true)), out{
			Resolver: objs(o("probe", "f")),
		})
		tr.do(probeAnswer("f", 604_800_000), out{
			Done:  objs(chose("c7", true)),
			State: state("choice", choice("f", "f"), "match", matchOf("mismatched", 604_800_000-heatMs)),
		})
		tr.do(advance(250), out{Send: objs(tr.status(1, "loading", matchOf("mismatched", 604_800_000-heatMs)))})
		add(tr)
	}

	// SDK.md §9.1, §10.2: resolveAgain with a choice replaces only the candidates; when the answer has the chosen id
	// under another ref, the choice keeps its ref, so the adapter bound by it stays bound with no command.
	{
		tr := newTrace("resolveAgain keeps the choice and its ref when the answer has the chosen id under another ref", setup())
		tr.guestFollowing("host", false)
		tr.do(call("resolveAgain"), out{Resolver: objs(o("resolve", "Heat"))})
		tr.do(answer(candidate("heat-a", "ref", "heat-a-2", "durationMs", heatMs+500)), out{
			State: state("candidates", objs(ranked("heat-a", 500)), "choice", choice("heat-a", "heat-a"), "match", m500,
				"player", "ready"),
		})
		tr.do(advance(250), out{})
		add(tr)
	}

	// SDK.md §7.4, §10.2, §11.2: detachPlayer releases p1, nudged at 1.44 at room rate 1.5 with that setRate still
	// running. The restore is the room rate captured at the release: a later room at rate 2 doesn't change it, and
	// the end (leave) doesn't drop it, since p1 is no longer the attached adapter. It runs once the nudge finishes,
	// and the completion settles only after the restore has finished too.
	{
		tr := newTrace("detachPlayer while nudging restores the room rate of the release after the running command, also past a new rate and the end; its completion waits for the restore", setup())
		room := gapRoomAtRate(tr, tr.guestFollowing("host", false))
		gapHeldNudge(tr, 1.44)
		tr.do(call("detachPlayer", "id", "d1"), out{State: state("player", "idle")})
		room = followAction(room, 9, "playing", 610_250, tr.relayNow())
		room.Rate = 2
		tr.do(recv(room.acted("rate", "ben").msg()), out{Event: objs(evAction("rate", "ben", false, 9)), State: state("room", 9)})
		tr.do(hold("p1"), out{})
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended"),
		})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1.5))})
		tr.do(finish("p1"), out{Done: objs(done("d1"))})
		tr.do(advance(5000), out{})
		add(tr)
	}

	// SDK.md §10.2, §11.2: attaching p2 releases p1 while its nudge runs; detachPlayer naming p1, no longer attached,
	// changes nothing (p2 stays bound) but returns the release completion, which waits for p1's running nudge and the
	// restore queued after it.
	{
		tr := newTrace("detachPlayer naming a released adapter changes nothing and settles once its running command and restore have run", setup())
		tr.guestFollowing("host", false)
		gapHeldNudge(tr, 0.96)
		tr.do(player("p2", "positionMs", 610_250, "paused", false, "durationMs", heatMs+500, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p2", "ref", "heat-a"), out{
			Command: objs(cmd("p2", "setRate", "rate", 1)),
			State:   state("player", "ready"),
		})
		tr.do(call("detachPlayer", "id", "d1", "player", "p1"), out{State: state("player", "ready")})
		tr.do(hold("p1"), out{})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1))})
		tr.do(finish("p1"), out{Done: objs(done("d1"))})
		add(tr)
	}

	// SDK.md §8, §9.1, §10.2, §12, §14: setMedia sends positionMs as given and unbinds the adapter of the old file
	// without a pause. Lost on a drop, it goes again with that positionMs after the same-epoch joined's status, as
	// that lists the party as host; still pending, the old file's adapter stays unbound and gets no command though it
	// drifts, also across a room of the same epoch (Cat joining). The room starting its epoch carries it as own media
	// under the new ref, which the adapter, attached with the old one, doesn't take.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("a setMedia lost on a drop goes again with its positionMs; while pending, the old file's adapter stays unbound", s)
		room := tr.hostJoined()
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{
			Send: objs(o("type", "setMedia", "media", heat(), "positionMs", 0)),
		})
		room.Rev, room.Epoch, room.Phase, room.AtMs, room.WaitingOn = 2, 1, "waiting", tr.relayNow(), []string{"ann"}
		tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Event:   objs(evAction("media", "ann", true, 2)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			State:   state("match", matchOf("matched", 0), "choice", choice("", "heat"), "player", "ready"),
		})
		// Inside the status spacing: the loading status would go out at 1250, but the drop at 1100 stops it.
		tr.do(advance(100), out{})
		lost := o("type", "setMedia", "media", ronin(), "positionMs", 5000)
		tr.do(call("setMedia", "media", ronin(), "ref", "ronin", "positionMs", 5000), out{
			Send:  objs(lost),
			State: state("player", "loading"),
		})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		back := newRoom(2, tr.relayNow(), annHost.reported("ready", matchOf("matched", 0)))
		back.Epoch, back.Phase = 1, "paused"
		tr.do(recv(joinedMsg("ann", back, heat(), tr.relayNow())), out{
			Send: objs(tr.status(1, "loading", matchOf("matched", 0)), lost),
			State: state("connection", "connected", "room", 2, "isHost", true, "choice", choice("", "heat"),
				"player", "loading"),
		})
		tr.do(player("p1", "positionMs", 5000), out{})
		back.Rev = 3
		back.Participants = append(back.Participants, cat)
		tr.do(recv(back.msg()), out{Event: objs(evJoined("cat")), State: state("room", 3, "player", "loading")})
		tr.do(advance(1000), out{})
		back.Rev, back.Epoch, back.Phase, back.PositionMs, back.AtMs = 4, 2, "waiting", 5000, tr.relayNow()
		back.WaitingOn = []string{"ann", "cat"}
		tr.do(recv(back.withMedia(ronin()).acted("media", "ann").msg()), out{
			Event: objs(evAction("media", "ann", true, 4)),
			Send:  objs(tr.status(2, "loading", matchOf("matched", 0))),
			State: state("media", "Ronin", "match", matchOf("matched", 0), "choice", choice("", "ronin"),
				"player", "loading"),
		})
		add(tr)
	}

	return cases
}
