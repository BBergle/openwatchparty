package main

// Party traces of reconnect and resume (spec/SDK.md §14): drop detection, the backoff, the window, failed attempts,
// the switch to the new connection, the end during an attempt and the participant diff of a reconnect's joined. As
// in cases_party.go, every output is written by hand from SDK.md, and each trace's comment names the sections it
// pins. Helpers here carry the prefix rs.

import (
	"fmt"
	"slices"
	"strings"
)

const (
	rsPublicURL = "wss://relay.test/v1/ws"
	// rsHigh is a draw whose backoff factor 0.5 + 0.5u = 0.9990234375 is exact in binary, so every product with it is
	// exact too: 500 × it = 499.51171875 waits 499 ms.
	rsHigh = 0.998046875
)

// rsResumeMsg is the resume a reconnect attempt sends for peerID (SDK.md §14).
func rsResumeMsg(peerID string) obj {
	return o("type", "resume", "code", trCode, "peerId", peerID, "token", token(peerID))
}

// rsError is a relay error with any re, null included (SPEC.md §8).
func rsError(code string, re any) obj {
	return o("type", "error", "code", code, "message", "refused", "re", re)
}

// rsStatus is a status with the given offset and round trip; rttMs < 0 leaves rttMs out, as before the connection's
// first clock sample (SDK.md §10.1).
func rsStatus(epoch int64, player string, match obj, offsetMs, rttMs int64) obj {
	s := o("type", "status", "epoch", epoch, "player", player, "match", match, "offsetMs", offsetMs)
	if rttMs >= 0 {
		s = append(s, kv{"rttMs", rttMs})
	}
	return s
}

func rsRtt(ms any) obj { return o("rtt", ms) }

func rsMalformed(msg any) obj { return o("recv", msg, "malformed", true) }

func rsReconnecting() out { return out{State: state("connection", "reconnecting")} }

func rsEnded(reason string, err any) out {
	return out{
		Event: objs(evEnded(reason, err)),
		State: state("connection", "ended", "endReason", reason),
	}
}

func rsRonin() *Media {
	return &Media{Kind: "movie", Title: "Ronin", Year: new(1998), Ids: map[string]string{"imdb": "tt0122690"},
		DurationMs: 7_320_000}
}

// rsAttempt advances waitMs to the start of a reconnect attempt, pinned to the millisecond: it connects to url and
// sends resume as ann (SDK.md §14).
func (t *trace) rsAttempt(waitMs int64, url string) {
	if waitMs > 1 {
		t.do(advance(waitMs-1), out{})
	}
	t.do(advance(1), out{Net: objs(o("open", url)), Send: objs(rsResumeMsg("ann"))})
}

// rsResumed answers the attempt under way with room and no media: the party is connected again and sends status
// right after joined, with offsetMs and the new connection's round trip (SDK.md §7.3, §10.3, §14).
func (t *trace) rsResumed(room snap, offsetMs int64) {
	t.do(recv(joinedMsg("ann", room, nil, t.relayNow())), out{
		Send:  objs(rsStatus(room.Epoch, "idle", nil, offsetMs, t.setup.RttMs)),
		State: state("connection", "connected", "room", room.Rev),
	})
}

// rsCreated creates a room on Relays.at's one relay as hostJoined does, for a setup whose round trip delays the first
// clock sample: the status right after joined has no rttMs yet (SDK.md §10.1).
func (t *trace) rsCreated() snap {
	host := ann
	host.Host = true
	room := newRoom(1, t.relayNow(), host)
	t.do(call("create", "id", "c1"), out{
		Net:   objs(opened()),
		Send:  objs(createMsg()),
		State: state("connection", "connecting"),
	})
	t.do(recv(joinedMsg("ann", room, nil, t.relayNow())), out{
		Done:  objs(done("c1", "value", trCode)),
		Send:  objs(rsStatus(0, "idle", nil, 0, -1)),
		State: state("connection", "connected", "room", 1),
	})
	return room
}

// rsHangUntilWindow drops the member connection now and lets every attempt to url hang while opening until the
// 300000 ms window passes (SDK.md §2, §14). Attempt n starts after the backoff for the draw 0,
// floor(min(8000, 500 × 2^(n−1)) × 0.5), from the drop or from attempt n−1's open timeout (the connect timeout, §7.1);
// each draw is appended to setup.random. A connection that never opened leaves no close. The party ends lost with
// unreachable at the window.
func (t *trace) rsHangUntilWindow(url string) {
	const timeout, window = 10_000, 300_000
	wait := func(n int) int64 { return min(8000, int64(500)<<(n-1)) / 2 }
	end := t.now + window
	t.setup.Random = append(t.setup.Random, 0)
	t.do(drop(1006), rsReconnecting())
	at := t.now + wait(1)
	for n := 2; at < end; n++ {
		t.do(nextOpen("hang"), out{})
		if at-t.now > 1 {
			t.do(advance(at-t.now-1), out{})
		}
		t.do(advance(1), out{Net: objs(o("open", url))})
		failsAt := at + timeout
		if failsAt == end {
			panic("rsHangUntilWindow: an attempt fails exactly when the window ends")
		}
		if failsAt > end {
			break
		}
		t.setup.Random = append(t.setup.Random, 0)
		at = failsAt + wait(n)
	}
	if end-1 > t.now {
		t.do(advance(end-1-t.now), out{})
	}
	t.do(advance(1), rsEnded("lost", "unreachable"))
}

func partyResumeTraces() []Case {
	var cases []Case
	add := func(tr *trace) { cases = append(cases, tr.cas()) }

	annHost := ann
	annHost.Host = true
	benGuest := ben
	benGuest.Host = false
	dan := newPeer("dan", "Dan", false)
	eve := newPeer("eve", "Eve", false)
	fay := newPeer("fay", "Fay", false)
	// SDK.md §7.3, §8, §10.3, §12, §13, §14, §15: on the drop, commands throw not_in_room and the heartbeat due at
	// 15000 stays off; attempt 1 starts 250 ms after the drop (u = 0), a refused connection fails it, attempt 2 starts
	// 500 ms later; setOffset works while reconnecting and the same-epoch joined keeps it, so its status carries it;
	// joined is the rev baseline at any rev, a later room at that rev drops, a higher one applies.
	{
		s := setup()
		s.Random = []float64{0, 0}
		tr := newTrace("a drop reconnects: commands throw not_in_room, no status meanwhile, an offset set meanwhile is kept, joined resets the rev baseline", s)
		tr.do(call("create", "id", "c1"), out{
			Net:   objs(opened()),
			Send:  objs(createMsg()),
			State: state("connection", "connecting"),
		})
		room := newRoom(1, tr.relayNow(), annHost)
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{
			Done:  objs(done("c1", "value", trCode)),
			Send:  objs(tr.status(0, "idle", nil)),
			State: state("connection", "connected"),
		})
		tr.do(advance(1000), out{})
		room.Rev = 5
		room.Participants = []peer{annHost, benGuest}
		tr.do(recv(room.msg()), out{Event: objs(evJoined("ben")), State: state("room", 5)})
		tr.do(advance(13_000), out{})
		tr.do(drop(1006), out{State: state("connection", "reconnecting")})
		tr.do(call("play", "id", "c2"), out{Done: objs(done("c2", "error", "not_in_room"))})
		tr.do(call("setOffset", "ms", 1500), out{State: state("offsetMs", 1500)})
		tr.do(nextOpen("fail"), out{})
		tr.do(advance(249), out{})
		tr.do(advance(1), out{Net: objs(opened())})
		tr.rsAttempt(500, trURL)
		tr.do(advance(1000), out{})
		room.Rev = 3
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{
			Send:  objs(rsStatus(0, "idle", nil, 1500, 0)),
			State: state("connection", "connected", "room", 3, "offsetMs", 1500),
		})
		room.Participants = []peer{annHost, benGuest, cat}
		tr.do(recv(room.msg()), out{State: state("room", 3)})
		room.Rev = 4
		self := annHost
		self.OffsetMs = 1500
		room.Participants = []peer{self, benGuest, cat}
		tr.do(recv(room.msg()), out{Event: objs(evJoined("cat")), State: state("room", 4)})
		tr.do(call("leave"), out{
			Send:  objs(o("type", "leave")),
			Net:   objs(closed(1000)),
			Event: objs(evEnded("left", nil)),
			State: state("connection", "ended"),
		})
		add(tr)
	}

	// SDK.md §2, §14: a wait is floor(min(8000, 500 × 2^(n−1)) × (0.5 + 0.5u)) whole ms: with u = 0.998046875 attempt
	// 1 waits floor(499.51171875) = 499 ms and attempt 2 floor(999.0234375) = 999 ms, neither rounded up.
	{
		s := setup()
		s.Random = []float64{rsHigh, rsHigh}
		tr := newTrace("a reconnect attempt waits the backoff times 0.5 + 0.5u rounded down to a whole millisecond", s)
		room := tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.do(nextOpen("fail"), out{})
		tr.do(advance(498), out{})
		tr.do(advance(1), out{Net: objs(opened())})
		tr.rsAttempt(999, trURL)
		room.Rev = 2
		tr.rsResumed(room, 0)
		add(tr)
	}

	// SDK.md §7.3, §8, §14, §15: a reconnect's joined diffs participants against the snapshot before the drop: every
	// ParticipantJoined in new-snapshot order, then every ParticipantLeft in old-snapshot order, and no Action;
	// a later room without an action fires none, one with an action fires Action.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("a reconnect's joined emits participant events against the snapshot before the drop and no Action", s)
		tr.guestJoined(trCode, newRoom(4, 990_000, ben, ann, cat, dan), nil)
		tr.do(advance(1000), out{})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		back := newRoom(2, tr.relayNow(), eve, ben, ann, fay)
		tr.do(recv(joinedMsg("ann", back, nil, tr.relayNow())), out{
			Event: objs(evJoined("eve"), evJoined("fay"), evLeft("cat"), evLeft("dan")),
			Send:  objs(tr.status(0, "idle", nil)),
			State: state("connection", "connected", "room", 2),
		})
		back.Rev = 3
		tr.do(recv(back.msg()), out{State: state("room", 3)})
		back.Rev, back.Locked = 4, true
		tr.do(recv(back.acted("lock", "ben").msg()), out{Event: objs(evAction("lock", "ben", false, 4)), State: state("room", 4)})
		add(tr)
	}

	// SDK.md §14: every close status but 4000 is a drop, 1001, 1007 and 1009 among them; each starts an attempt.
	{
		s := setup()
		s.Random = []float64{0, 0, 0}
		tr := newTrace("close statuses 1001, 1007 and 1009 are drops, each followed by a resume", s)
		room := tr.hostJoined()
		for _, code := range []int64{1001, 1007, 1009} {
			tr.do(drop(code), rsReconnecting())
			tr.rsAttempt(250, trURL)
			room.Rev++
			tr.rsResumed(room, 0)
		}
		add(tr)
	}

	// SDK.md §14: 15 s without any relay message, pongs included, closes the connection with 1000 and is a drop; the
	// room at local 1000 re-armed the timer, so the heartbeat goes out at 15000 and the close comes at 16000.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("15 s without a message from the relay closes the connection with 1000 and resumes", s)
		room := tr.hostJoined()
		tr.do(rsRtt(nil), out{})
		tr.do(advance(1000), out{})
		room.Rev = 2
		room.Locked = true
		tr.do(recv(room.msg()), out{State: state("room", 2)})
		tr.do(advance(13_999), out{})
		tr.do(advance(1), out{Send: objs(tr.status(0, "idle", nil))})
		tr.do(advance(999), out{})
		tr.do(advance(1), out{Net: objs(closed(1000)), State: state("connection", "reconnecting")})
		tr.do(rsRtt(0), out{})
		tr.rsAttempt(250, trURL)
		room.Rev = 3
		tr.rsResumed(room, 0)
		add(tr)
	}

	// SDK.md §7.4, §14: close status 4000 ends the party as replaced, with no attempt and no draw.
	{
		tr := newTrace("close status 4000 ends the party as replaced and never resumes", setup())
		tr.hostJoined()
		tr.do(advance(1000), out{})
		tr.do(drop(4000), rsEnded("replaced", nil))
		tr.do(advance(300_000), out{})
		add(tr)
	}

	// SDK.md §7.2, §7.4, §14: resume_failed or client_blocked answering an attempt ends the party lost with that
	// error: the attempt's connection closes with 1000, no attempt follows; an ended party may create again.
	{
		s := setup()
		s.Random = []float64{0, 0}
		tr := newTrace("resume_failed or client_blocked answering a reconnect attempt ends the party lost with that error, with no further attempt", s)
		tr.hostJoined()
		for i, code := range []string{"resume_failed", "client_blocked"} {
			if i > 0 {
				tr.created("c2", opened())
			}
			tr.do(drop(1006), rsReconnecting())
			tr.rsAttempt(250, trURL)
			res := rsEnded("lost", code)
			res.Net = objs(closed(1000))
			tr.do(recv(rsError(code, "resume")), res)
			tr.do(advance(300_000), out{})
		}
		add(tr)
	}

	// SDK.md §7.1, §14, §15: any other error answering resume, known or not, fails the attempt: its connection
	// closes with 1000 and the next attempt follows the backoff; an error whose re isn't resume is no answer and
	// produces no event; joined then reconnects with status and no event.
	{
		s := setup()
		s.Random = []float64{0, 0, 0}
		tr := newTrace("any other error answering a reconnect attempt fails it and the next one follows; errors for other requests are ignored", s)
		room := tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		tr.do(recv(rsError("rate_limited", "resume")), out{Net: objs(closed(1000)), State: state("connection", "reconnecting")})
		tr.rsAttempt(500, trURL)
		tr.do(recv(rsError("relay_restarting", "resume")), out{Net: objs(closed(1000)), State: state("connection", "reconnecting")})
		tr.rsAttempt(1000, trURL)
		tr.do(recv(rsError("rate_limited", nil)), out{})
		tr.do(recv(rsError("room_full", "join")), out{})
		room.Rev = 2
		tr.rsResumed(room, 0)
		add(tr)
	}

	// SDK.md §8, §14: a malformed joined answering resume (you.token of 42 characters) fails the attempt: closed with
	// 1000, no leave; the next attempt resumes.
	{
		s := setup()
		s.Random = []float64{0, 0}
		tr := newTrace("a malformed joined answering a reconnect attempt fails it, closed with 1000 without leave, and the next one resumes", s)
		room := tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		room.Rev = 2
		bad := o("type", "joined", "code", trCode, "you", o("peerId", "ann", "token", strings.Repeat("t", 42)),
			"room", room, "media", nil, "serverTime", tr.relayNow())
		tr.do(rsMalformed(bad), out{Net: objs(closed(1000)), State: state("connection", "reconnecting")})
		tr.rsAttempt(500, trURL)
		tr.rsResumed(room, 0)
		add(tr)
	}

	// SDK.md §7.1, §14: an attempt needs joined and a first clock sample within the connect timeout of sending
	// resume; with pings unanswered, the joined alone fails it at 10000 ms: closed with 1000, no leave; with pongs
	// back, the next attempt resumes.
	{
		s := setup()
		s.Random = []float64{0, 0}
		tr := newTrace("a reconnect attempt whose joined gets no clock sample within 10 s of sending resume fails, closed with 1000 without leave", s)
		room := tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.do(rsRtt(nil), out{})
		tr.rsAttempt(250, trURL)
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{State: state("connection", "reconnecting", "room", 1)})
		tr.do(advance(9999), out{})
		tr.do(advance(1), out{Net: objs(closed(1000)), State: state("connection", "reconnecting")})
		tr.do(rsRtt(0), out{})
		tr.rsAttempt(500, trURL)
		room.Rev = 3
		tr.rsResumed(room, 0)
		add(tr)
	}

	// SDK.md §7.1, §14: a connection closing before the switch fails its attempt, both before the attempt's joined
	// and after it while the first clock sample is still 1000 ms away; the next attempt follows the backoff and
	// switches once its sample comes.
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0, 0, 0}
		tr := newTrace("a reconnect attempt whose connection closes before the switch, before or after its joined, fails", s)
		room := tr.rsCreated()
		tr.do(advance(1000), out{})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		tr.do(drop(1011), rsReconnecting())
		tr.rsAttempt(500, trURL)
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), rsReconnecting())
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(1000, trURL)
		room.Rev = 3
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), rsReconnecting())
		tr.do(advance(999), out{})
		tr.do(advance(1), out{
			Send:  objs(rsStatus(0, "idle", nil, 0, 1000)),
			State: state("connection", "connected", "room", 3),
		})
		add(tr)
	}

	// SDK.md §7.3, §8, §14, §15: rooms that arrive on the attempt's connection after its joined, while the first
	// clock sample is still 1000 ms away, leave the old snapshot in place; at the switch they are handled in order
	// after joined's status: rev 3 brings Ben, rev 4 an Action.
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0}
		tr := newTrace("rooms that arrive after a reconnect attempt's joined but before its clock sample are handled in order after the switch", s)
		room := tr.rsCreated()
		tr.do(advance(1000), out{})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{State: state("connection", "reconnecting", "room", 1)})
		room.Rev = 3
		room.Participants = []peer{annHost, benGuest}
		tr.do(recv(room.msg()), out{State: state("room", 1)})
		room.Rev, room.Locked = 4, true
		tr.do(recv(room.acted("lock", "ben").msg()), out{State: state("room", 1)})
		tr.do(advance(999), out{})
		tr.do(advance(1), out{
			Send:  objs(rsStatus(0, "idle", nil, 0, 1000)),
			Event: objs(evJoined("ben"), evAction("lock", "ben", false, 4)),
			State: state("connection", "connected", "room", 4),
		})
		add(tr)
	}

	// SDK.md §7.1, §14: the default window is 300000 ms from the drop. Unanswered attempts fail at the connect timeout
	// and wait floor(min(8000, 500 × 2^(n−1)) × 0.9990234375) after it: 499, 999, 1998, 3996 ms, then 7992 ms, never
	// more than 8000. Attempt 18 starts at 289380 and fails at 299380; the next would start at 307372, so the party
	// ends lost with unreachable at 300000 with no attempt under way.
	{
		s := setup()
		s.Random = slices.Repeat([]float64{rsHigh}, 19)
		tr := newTrace("failed reconnect attempts back off to at most 8000 ms apart; 300000 ms after the drop the party ends lost unreachable", s)
		tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		waits := append([]int64{499, 999, 1998, 3996}, slices.Repeat([]int64{7992}, 14)...)
		for _, w := range waits {
			tr.rsAttempt(w, trURL)
			tr.do(advance(10_000), out{Net: objs(closed(1000))})
		}
		if tr.now != 299_380 {
			panic(fmt.Sprintf("the last attempt fails at %d, not 299380", tr.now))
		}
		tr.do(advance(619), out{})
		tr.do(advance(1), rsEnded("lost", "unreachable"))
		tr.do(advance(10_000), out{})
		add(tr)
	}

	// SDK.md §2, §3.1, §7.1, §14: on the public list a reconnect resumes at the URL joined on; an attempt whose
	// connection never opens fails at the connect timeout without a close, and the next one follows the backoff; the
	// window is 300000 ms from the drop: attempt 23 is still opening then, and the party ends lost with unreachable.
	// refresh() reads the list first, so join routes with it and joined starts no background refresh.
	{
		s := setup()
		s.Base = nil
		s.List = o("relays", objs(o("id", "t", "name", "Test", "url", rsPublicURL, "prefixes", Alphabet)))
		tr := newTrace("reconnect attempts whose connection never opens fail at the connect timeout without a close; the window ends lost at 300000 ms", s)
		tr.do(call("refresh", "id", "r1"), out{Net: objs(got()), Done: objs(done("r1", "value", true))})
		tr.do(call("join", "id", "c1", "code", trCode), out{
			Net:   objs(o("open", rsPublicURL)),
			Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
			State: state("connection", "connecting"),
		})
		tr.do(recv(joinedMsg("ann", newRoom(4, 990_000, ben, ann), nil, tr.relayNow())), out{
			Done:  objs(done("c1")),
			Send:  objs(tr.status(0, "idle", nil)),
			State: state("connection", "connected"),
		})
		tr.rsHangUntilWindow(rsPublicURL)
		add(tr)
	}

	// SDK.md §7.2, §7.4, §14: leave() during an attempt ends the party left at once, sending nothing; the connection
	// that sent resume is kept for its answer, and a late joined gets leave and a close with 1000 when it arrives,
	// not when the clock sample it would have needed comes (round trip 1000 ms).
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0}
		tr := newTrace("leave() during a reconnect attempt ends left at once; the connection that sent resume answers a late joined with leave at once", s)
		room := tr.rsCreated()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		tr.do(call("leave"), rsEnded("left", nil))
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{Send: objs(o("type", "leave")), Net: objs(closed(1000))})
		tr.do(advance(20_000), out{})
		add(tr)
	}

	// SDK.md §7.2, §7.4, §11.2, §14: close() during an attempt ends the party left and settles at once, with no
	// adapter to wait for; the connection that sent resume waits for its answer, and an error answer just closes it.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("close() during a reconnect attempt settles at once; the connection that sent resume waits for the answer, and an error just closes it", s)
		tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		res := rsEnded("left", nil)
		res.Done = objs(done("c2"))
		tr.do(call("close", "id", "c2"), res)
		tr.do(recv(rsError("resume_failed", "resume")), out{Net: objs(closed(1000))})
		tr.do(advance(20_000), out{})
		add(tr)
	}

	// SDK.md §7.1, §7.2, §14: after the end, an attempt that sent resume and got no answer is closed with 1000 at the
	// connect timeout of sending, 10000 ms, without leave.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("after the end, a reconnect attempt without an answer is closed with 1000 at its connect timeout, without leave", s)
		tr.hostJoined()
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		tr.do(advance(3000), out{})
		tr.do(call("leave"), rsEnded("left", nil))
		tr.do(advance(6999), out{})
		tr.do(advance(1), out{Net: objs(closed(1000))})
		add(tr)
	}

	// SDK.md §7.2, §14: the attempt's joined waits for the first clock sample (round trip 1000 ms), so the party is
	// still reconnecting on the old snapshot; ending then answers that joined with leave and closes it at once.
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0}
		tr := newTrace("the end after a reconnect attempt got joined but no clock sample sends leave on it and closes it at once", s)
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
		add(tr)
	}

	// SDK.md §7.3, §9.1, §10.2, §10.3, §14, §15: a setMedia still pending when a reconnect's joined has the epoch of
	// before the drop never reached the room; the joined lists Ben as host, so it stops being pending without being
	// sent again: the own choice binds again (one setRate of the room rate) and the status right after joined says
	// ready; Ben is new against the snapshot before the drop.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("a setMedia lost on a drop stops being pending when the same-epoch joined lists another host: it rebinds and reports ready", s)
		room := tr.hostJoined()
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{
			Send: objs(o("type", "setMedia", "media", heat(), "positionMs", 0)),
		})
		room.Rev, room.Epoch, room.Phase, room.WaitingOn = 2, 1, "waiting", []string{"ann"}
		tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
			Event:   objs(evAction("media", "ann", true, 2)),
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			State:   state("room", 2, "match", matchOf("matched", 0), "choice", choice("", "heat"), "player", "ready"),
		})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", rsRonin(), "ref", "ronin"), out{
			Send:  objs(o("type", "setMedia", "media", rsRonin(), "positionMs", 0), tr.status(1, "loading", matchOf("matched", 0))),
			State: state("player", "loading"),
		})
		tr.do(drop(1006), rsReconnecting())
		tr.rsAttempt(250, trURL)
		back := newRoom(3, tr.relayNow(), ann.reported("loading", matchOf("matched", 0)), ben)
		back.Epoch, back.Phase = 1, "paused"
		tr.do(recv(joinedMsg("ann", back, heat(), tr.relayNow())), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			Event:   objs(evJoined("ben")),
			State: state("connection", "connected", "room", 3, "isHost", false, "media", "Heat",
				"choice", choice("", "heat"), "player", "ready"),
		})
		add(tr)
	}

	// SDK.md §8, §9.1, §10.2, §13, §14: a reconnect's joined whose epoch differs runs a new epoch: the offset set
	// while reconnecting goes back to 0, the choice and candidates clear, the bound adapter unbinds and pauses once,
	// and the other participant's media starts a search; the status right after joined reports it.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("a reconnect's joined with another epoch runs a new epoch: offset 0, choice cleared, the player paused once, a new search", s)
		tr.guestFollowing("host", false)
		tr.do(drop(1006), rsReconnecting())
		tr.do(call("setOffset", "ms", 100), out{State: state("offsetMs", 100)})
		tr.rsAttempt(250, trURL)
		back := newRoom(9, tr.relayNow(), ben.reported("loading", matchOf("matched", 0)),
			ann.reported("ready", matchOf("matched", 500)))
		back.Epoch, back.Phase = 3, "paused"
		tr.do(recv(joinedMsg("ann", back, rsRonin(), tr.relayNow())), out{
			Command:  objs(cmd("p1", "pause")),
			Resolver: objs(o("resolve", "Ronin")),
			Send:     objs(tr.status(3, "loading", matchOf("searching"))),
			State: state("connection", "connected", "room", 9, "media", "Ronin", "offsetMs", 0,
				"match", matchOf("searching"), "choice", nil, "candidates", objs(), "player", "loading"),
		})
		tr.do(o("answer", objs()), out{State: state("match", matchOf("unavailable"), "player", "idle")})
		tr.do(advance(250), out{Send: objs(tr.status(3, "idle", matchOf("unavailable")))})
		add(tr)
	}

	// SDK.md §5, §7.3, §10.2, §10.3, §11.1, §11.2, §14: with a round trip of 1000 ms, relay time is known from local
	// 1000. While reconnecting the tick and playerChanged() still step on the last snapshot and the old clock (a
	// 600 ms drift nudges at 0.96; at 1500 and 2000 the drift stays above 150 ms). The attempt's joined (the room
	// paused at 400) changes nothing until its first clock sample at 2500; then the party is connected, its status
	// reports loading (held 1650 ms away), and the step on the new snapshot pauses, restores rate 1 and seeks to 400;
	// ready follows after the 250 ms spacing.
	{
		s := setup()
		s.RttMs = 1000
		s.Random = []float64{0}
		tr := newTrace("while reconnecting the party keeps stepping on the old snapshot and clock, and switches to the new ones once the attempt has joined and a clock sample", s)
		room := tr.rsCreated()
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{
			Send: objs(o("type", "setMedia", "media", heat(), "positionMs", 0)),
		})
		room.Rev, room.Epoch, room.Phase, room.WaitingOn = 2, 1, "waiting", []string{"ann"}
		tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
			Event:   objs(evAction("media", "ann", true, 2)),
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(rsStatus(1, "ready", matchOf("matched", 0), 0, 1000)),
			State:   state("room", 2, "player", "ready"),
		})
		room.Rev, room.Phase, room.AtMs, room.WaitingOn = 3, "playing", tr.relayNow(), []string{}
		tr.do(recv(room.acted("play", "ann").msg()), out{
			Event:   objs(evAction("play", "ann", true, 3)),
			Command: objs(cmd("p1", "play")),
			State:   state("room", 3, "player", "ready"),
		})
		tr.do(advance(250), out{})
		tr.do(drop(1006), rsReconnecting())
		tr.do(player("p1", "positionMs", 850), out{})
		tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
		tr.rsAttempt(250, trURL)
		back := room
		back.Rev, back.Phase, back.PositionMs, back.AtMs = 4, "paused", 400, tr.relayNow()
		back = back.withPeer(0, annHost.reported("ready", matchOf("matched", 0)))
		tr.do(recv(joinedMsg("ann", back, heat(), tr.relayNow())), out{State: state("connection", "reconnecting", "room", 3)})
		tr.do(advance(500), out{State: state("room", 3)})
		tr.do(advance(500), out{
			Command: objs(cmd("p1", "pause"), cmd("p1", "setRate", "rate", 1), cmd("p1", "seek", "positionMs", 400)),
			Send:    objs(rsStatus(1, "loading", matchOf("matched", 0), 0, 1000)),
			State:   state("connection", "connected", "room", 4, "player", "ready"),
		})
		tr.do(advance(250), out{Send: objs(rsStatus(1, "ready", matchOf("matched", 0), 0, 1000))})
		add(tr)
	}

	return cases
}
