package main

// Party traces of choosing a relay for create (spec/SDK.md §6) and of routing on the public list (§3, §4). Several
// relays come only from the public list, so these traces start on Relays.public() and refresh it first; each
// connection is addressed by its URL.

const (
	urlA = "wss://a.relay.test/v1/ws"
	urlB = "wss://b.relay.test/v1/ws"
	urlC = "wss://c.relay.test/v1/ws"
)

// codeOn is a code the relay at url creates: its first symbol is the relay's.
var codeOn = map[string]string{urlA: "A7MQ2X", urlB: "K7MQ2X", urlC: "M7MQ2X"}

// pickList is the public list of these traces: three relays, each owning a third of the code symbols.
func pickList() obj {
	return o("relays", objs(
		o("id", "a", "name", "A", "url", urlA, "prefixes", "23456789A"),
		o("id", "b", "name", "B", "url", urlB, "prefixes", "BCDEFGHJK"),
		o("id", "c", "name", "C", "url", urlC, "prefixes", "MNPQRSTUVWXYZ"),
	))
}

// pickSetup is setup() on the public source, every GET of which answers pickList().
func pickSetup() partySetup {
	s := setup()
	s.Base = nil
	s.List = pickList()
	return s
}

// pickRefreshed calls refresh(): the list becomes the GET's answer (SDK.md §3.3), and with that success no joined of
// the trace starts a background refresh.
func (t *trace) pickRefreshed() {
	t.do(call("refresh", "id", "r0"), out{Net: objs(got()), Done: objs(done("r0", "value", true))})
}

func rttOn(url string, ms any) obj        { return o("rtt", ms, "on", url) }
func recvOn(url string, msg any) obj      { return o("recv", msg, "on", url) }
func dropOn(url string, code any) obj     { return o("drop", code, "on", url) }
func sendOn(url string, msg obj) obj      { return o("on", url, "msg", msg) }
func openedOn(url string) obj             { return o("open", url) }
func closedOn(url string, code int64) obj { return o("close", url, "code", code) }
func refusal(code, message, re string) obj {
	return o("type", "error", "code", code, "message", message, "re", re)
}
func createMsg() obj { return o("type", "create", "name", trName, "client", trClient) }
func doneRefused(id, code, message string) obj {
	return done(id, "error", code, "re", "create", "message", message)
}

// pickCreate sets each relay's round trip (nil: it never answers) and calls create: every relay of the list is
// connected at once, each pinging from its open (SDK.md §5, §6).
func (t *trace) pickCreate(rttA, rttB, rttC any) {
	t.do(rttOn(urlA, rttA), out{})
	t.do(rttOn(urlB, rttB), out{})
	t.do(rttOn(urlC, rttC), out{})
	t.do(call("create", "id", "c1"), out{
		Net:   objs(openedOn(urlA), openedOn(urlB), openedOn(urlC)),
		State: state("connection", "connecting"),
	})
}

// pickJoined answers the create sent to url with a new room on that relay: create resolves with its code and status
// goes out on that connection with its round trip, nil before its first sample (SDK.md §6, §7.3, §10.1).
func (t *trace) pickJoined(url string, rtt any) {
	host := ann
	host.Host = true
	room := newRoom(1, t.relayNow(), host)
	room.Code = codeOn[url]
	status := o("type", "status", "epoch", 0, "player", "idle", "match", nil, "offsetMs", 0)
	if rtt != nil {
		status = append(status, kv{"rttMs", rtt})
	}
	t.do(recvOn(url, joinedMsg("ann", room, nil, t.relayNow())), out{
		Done:  objs(done("c1", "value", room.Code)),
		Send:  objs(sendOn(url, status)),
		State: state("connection", "connected", "room", 1, "isHost", true),
	})
}

func pickTraces(add func(*trace)) {
	// SDK.md §6, §15: with one relay (Relays.at) create is sent at once, no pong wait; its relay_full fails create at
	// once with that relay's error, the one connection closes with 1000, back to idle, no event.
	{
		tr := newTrace("one relay: create goes at once; relay_full fails it with the relay's error", setup())
		tr.do(call("create", "id", "c1"), out{
			Net:   objs(opened()),
			Send:  objs(createMsg()),
			State: state("connection", "connecting"),
		})
		tr.do(recv(refusal("relay_full", "this relay holds its maximum of rooms", "create")), out{
			Net:   objs(closed(1000)),
			Done:  objs(doneRefused("c1", "relay_full", "this relay holds its maximum of rooms")),
			State: state("connection", "idle", "room", nil),
		})
		add(tr)
	}

	// SDK.md §6, §7.3: with several relays the first whose connection yields an accepted clock sample wins (b, whose
	// pong comes first at 10 ms): create is sent there and every other connection closes with 1000 then; the
	// winner's clock is the room's, so status after joined carries its round trip.
	{
		tr := newTrace("several relays: create goes to the first relay with an accepted pong; the others close with 1000 then", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(40, 10, 70)
		tr.do(advance(9), out{})
		tr.do(advance(1), out{
			Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.pickJoined(urlB, 10)
		add(tr)
	}

	// SDK.md §5, §6: a connection refused at open is out of the race at once; a pong failing its check (serverTime
	// −1) is no sample, so b, whose valid pong comes at 30 ms, wins over c at 50.
	{
		tr := newTrace("several relays: a refused connection and a pong failing its check don't win the race", pickSetup())
		tr.pickRefreshed()
		tr.do(o("nextOpen", "fail", "on", urlA), out{})
		tr.pickCreate(nil, 30, 50)
		tr.do(advance(5), out{})
		tr.do(o("recv", o("type", "pong", "id", 1, "serverTime", -1), "malformed", true, "on", urlC), out{})
		tr.do(advance(24), out{})
		tr.do(advance(1), out{
			Net:  objs(closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.pickJoined(urlB, 30)
		add(tr)
	}

	// SDK.md §2, §5, §6: a round trip over 1000 ms is no sample; with no accepted pong within the connect timeout
	// (10000 ms) from t0, create fails unreachable (re null) and every connection closes with 1000; back to idle.
	{
		tr := newTrace("several relays: no accepted pong within 10000 ms fails create unreachable; a 1001 ms round trip is no sample", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(1001, nil, nil)
		tr.do(advance(9999), out{})
		tr.do(advance(1), out{
			Net:   objs(closedOn(urlA, 1000), closedOn(urlB, 1000), closedOn(urlC, 1000)),
			Done:  objs(done("c1", "error", "unreachable", "re", nil)),
			State: state("connection", "idle"),
		})
		add(tr)
	}

	// SDK.md §6: when every connection fails before any pong, create fails unreachable at once, back to idle.
	{
		tr := newTrace("several relays: every connection failing fails create unreachable at once", pickSetup())
		tr.pickRefreshed()
		for _, url := range []string{urlA, urlB, urlC} {
			tr.do(o("nextOpen", "fail", "on", url), out{})
		}
		tr.do(call("create", "id", "c1"), out{
			Net:   objs(openedOn(urlA), openedOn(urlB), openedOn(urlC)),
			Done:  objs(done("c1", "error", "unreachable", "re", nil)),
			State: state("connection", "idle"),
		})
		add(tr)
	}

	// SDK.md §6, §15: relay_full closes that connection with 1000 and tries the first untried relay in list order
	// (b won, so a, then c), connecting and sending create at once without a pong wait; when every relay has answered
	// relay_full, create fails with the last one's error, message and re included, back to idle, no event.
	{
		tr := newTrace("relay_full tries each untried relay in list order without a pong wait, then fails with the last relay's error", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		tr.do(advance(10), out{
			Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.do(recvOn(urlB, refusal("relay_full", "b is full", "create")), out{
			Net:  objs(closedOn(urlB, 1000), openedOn(urlA)),
			Send: objs(sendOn(urlA, createMsg())),
		})
		tr.do(recvOn(urlA, refusal("relay_full", "a is full", "create")), out{
			Net:  objs(closedOn(urlA, 1000), openedOn(urlC)),
			Send: objs(sendOn(urlC, createMsg())),
		})
		tr.do(recvOn(urlC, refusal("relay_full", "c is full", "create")), out{
			Net:   objs(closedOn(urlC, 1000)),
			Done:  objs(doneRefused("c1", "relay_full", "c is full")),
			State: state("connection", "idle"),
		})
		add(tr)
	}

	// SDK.md §6, §7.3, §10.1: the relay create moved to after relay_full can create the room; its clock starts at
	// that connection's open, so the status right after joined has no round trip yet.
	{
		tr := newTrace("relay_full moves create to the next relay, which joins; status there has no round trip yet", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		tr.do(advance(10), out{
			Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.do(recvOn(urlB, refusal("relay_full", "b is full", "create")), out{
			Net:  objs(closedOn(urlB, 1000), openedOn(urlA)),
			Send: objs(sendOn(urlA, createMsg())),
		})
		tr.pickJoined(urlA, nil)
		add(tr)
	}

	// SDK.md §6, §15: a refusal other than relay_full ends create with that code, no more tries, also after a
	// relay_full moved it; back to idle, nothing later.
	{
		tr := newTrace("a refusal other than relay_full ends create with no more tries", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		tr.do(advance(10), out{
			Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.do(recvOn(urlB, refusal("relay_full", "b is full", "create")), out{
			Net:  objs(closedOn(urlB, 1000), openedOn(urlA)),
			Send: objs(sendOn(urlA, createMsg())),
		})
		tr.do(recvOn(urlA, refusal("rate_limited", "too many rooms from this address", "create")), out{
			Net:   objs(closedOn(urlA, 1000)),
			Done:  objs(doneRefused("c1", "rate_limited", "too many rooms from this address")),
			State: state("connection", "idle"),
		})
		tr.do(advance(10_000), out{})
		add(tr)
	}

	// SDK.md §6, §7.1: after relay_full moved create, a lost connection fails it unreachable (re null) with no more
	// tries; so does no answer within the connect timeout (10000 ms) of sending create, which closes that connection
	// with 1000. Back to idle each time.
	{
		tr := newTrace("after relay_full, a lost connection or no answer within 10000 ms fails create unreachable with no more tries", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		toA := func() {
			tr.do(advance(10), out{
				Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
				Send: objs(sendOn(urlB, createMsg())),
			})
			tr.do(recvOn(urlB, refusal("relay_full", "b is full", "create")), out{
				Net:  objs(closedOn(urlB, 1000), openedOn(urlA)),
				Send: objs(sendOn(urlA, createMsg())),
			})
		}
		toA()
		tr.do(dropOn(urlA, 1006), out{
			Done:  objs(done("c1", "error", "unreachable", "re", nil)),
			State: state("connection", "idle"),
		})
		tr.do(call("create", "id", "c2"), out{
			Net:   objs(openedOn(urlA), openedOn(urlB), openedOn(urlC)),
			State: state("connection", "connecting"),
		})
		toA()
		tr.do(advance(9999), out{})
		tr.do(advance(1), out{
			Net:   objs(closedOn(urlA, 1000)),
			Done:  objs(done("c2", "error", "unreachable", "re", nil)),
			State: state("connection", "idle"),
		})
		tr.do(advance(10_000), out{})
		add(tr)
	}

	// SDK.md §6, §7.2: leave() after create was sent cancels it at once (cancelled, re null); the connection that sent
	// it stays for the answer, and its late relay_full only closes it: create moves to no other relay.
	{
		tr := newTrace("leave() after create was sent cancels it; a late relay_full only closes that connection", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		tr.do(advance(10), out{
			Net:  objs(closedOn(urlA, 1000), closedOn(urlC, 1000)),
			Send: objs(sendOn(urlB, createMsg())),
		})
		tr.do(call("leave"), out{
			Done:  objs(done("c1", "error", "cancelled", "re", nil)),
			State: state("connection", "idle"),
		})
		tr.do(recvOn(urlB, refusal("relay_full", "b is full", "create")), out{Net: objs(closedOn(urlB, 1000))})
		tr.do(advance(10_000), out{})
		add(tr)
	}

	// SDK.md §6, §7.2: leave() before any relay won cancels create (cancelled, re null) and closes every connection
	// with 1000 at once; nothing follows.
	{
		tr := newTrace("leave() before any relay answered a ping cancels create and closes every connection with 1000", pickSetup())
		tr.pickRefreshed()
		tr.pickCreate(30, 10, 20)
		tr.do(advance(5), out{})
		tr.do(call("leave"), out{
			Net:   objs(closedOn(urlA, 1000), closedOn(urlB, 1000), closedOn(urlC, 1000)),
			Done:  objs(done("c1", "error", "cancelled", "re", nil)),
			State: state("connection", "idle"),
		})
		tr.do(advance(10_000), out{})
		add(tr)
	}
}
