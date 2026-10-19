package main

import (
	"fmt"
	"math"
	"time"
)

// SPEC §12: the playback state machine.

// scNoMedia: in idle, play, pause, seek and rate get invalid_message, after the permission check (§7.6–§7.9, §12.1).
func scNoMedia(x *sc) error {
	cs, err := x.roomOf(2)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	for _, m := range []Msg{{"type": "play"}, {"type": "pause"}, {"type": "seek", "positionMs": 10}, {"type": "rate", "rate": 1.0}} {
		_ = a.send(m)
		if err := expectErr(a, "invalid_message", str(m, "type")); err != nil {
			return err
		}
		_ = b.send(m)
		if err := expectErr(b, "forbidden", str(m, "type")); err != nil {
			return fmt.Errorf("%s from a non-controller without media: %w", str(m, "type"), err)
		}
	}
	return nil
}

// scRttDefault: the start delay counts defaultRttMs when nobody is counted and for a counted participant that never
// reported rttMs, and the last reported rttMs for one whose later status and resume carried none (§7.3, §7.5, §12.6).
func scRttDefault(x *sc) error {
	def := x.pol("defaultRttMs")
	a, _, err := x.create("A")
	if err != nil {
		return err
	}
	clk, err := a.clock()
	if err != nil {
		return err
	}
	_ = a.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)})
	// A has sent no status, so it counts as loading (§12.3): the room that carries the media waits on A.
	if _, err := a.waitFor(reply, "the media, waiting on A", func(m Msg) bool { return hasMedia(m) && waitingOnIs(a.PeerID)(m) }); err != nil {
		return err
	}
	// A reports unavailable with rttMs 40: still in the player, but not counted, so nobody is counted and the room
	// releases at once with defaultRttMs.
	t0 := time.Now()
	_ = a.send(Msg{"type": "status", "epoch": 1, "player": "loading", "match": Msg{"state": "unavailable"}, "offsetMs": 0, "rttMs": 40})
	r, err := a.waitFor(reply, "room after A unavailable", isType("room"))
	if err != nil {
		return err
	}
	t1 := time.Now()
	if k, _ := actionOf(r); str(r, "phase") != "playing" || num(r, "positionMs") != 0 || k != "" ||
		len(strList(r, "waitingOn")) != 0 {
		return fmt.Errorf("the only member reported unavailable: %s; want playing at 0 at once", brief(r))
	}
	want := x.startDelay(def)
	if err := timeBetween(num(r, "atMs"), clk.early(t0)+want, clk.late(t1)+want,
		fmt.Sprintf("atMs with nobody counted (maxRtt %.0f)", def)); err != nil {
		return err
	}
	// Q never reports rttMs: it counts as defaultRttMs although P reports 40.
	cs, err := x.roomOf(2)
	if err != nil {
		return err
	}
	p, q := cs[0], cs[1]
	if clk, err = p.clock(); err != nil {
		return err
	}
	_ = status(p, 0, "loading", nil, 40)
	_ = status(q, 0, "loading", nil, -1)
	if err := waitPlayers(cs, "loading"); err != nil {
		return err
	}
	_ = p.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)})
	for _, c := range cs {
		if _, err := c.waitFor(reply, "waiting on both", waitingOnIs(p.PeerID, q.PeerID)); err != nil {
			return err
		}
	}
	_ = status(p, 1, "ready", matched(), 40)
	for _, c := range cs {
		if _, err := c.waitFor(reply, "waitingOn [Q]", waitingOnIs(q.PeerID)); err != nil {
			return err
		}
	}
	t0 = time.Now()
	_ = status(q, 1, "ready", matched(), -1)
	r, err = p.waitFor(reply, "playing", isPhase("playing"))
	if err != nil {
		return err
	}
	t1 = time.Now()
	if err := timeBetween(num(r, "atMs"), clk.early(t0)+want, clk.late(t1)+want,
		fmt.Sprintf("atMs with a participant that never reported rttMs (counts as %.0f)", def)); err != nil {
		return err
	}
	// R reports 100 once: a status without rttMs keeps it, and so does a resume (§7.3, §7.5).
	cs, err = x.roomOf(2)
	if err != nil {
		return err
	}
	s, rr := cs[0], cs[1]
	_ = status(s, 0, "loading", nil, 10)
	_ = status(rr, 0, "loading", nil, 100)
	if err := waitPlayers(cs, "loading"); err != nil {
		return err
	}
	_ = s.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)})
	if _, err := s.waitFor(reply, "waiting on both", waitingOnIs(s.PeerID, rr.PeerID)); err != nil {
		return err
	}
	rr.drop()
	if _, err := s.waitFor(reply, "R disconnected", connectedIs(rr.PeerID, false)); err != nil {
		return err
	}
	r2, _, err := x.resume(rr, "R2")
	if err != nil {
		return err
	}
	if _, err := s.waitFor(reply, "R connected", connectedIs(rr.PeerID, true)); err != nil {
		return err
	}
	_ = status(s, 1, "ready", matched(), 10)
	if _, err := s.waitFor(reply, "waitingOn [R]", waitingOnIs(rr.PeerID)); err != nil {
		return err
	}
	if clk, err = s.clock(); err != nil {
		return err
	}
	t0 = time.Now()
	_ = status(r2, 1, "ready", matched(), -1)
	r, err = s.waitFor(reply, "playing", isPhase("playing"))
	if err != nil {
		return err
	}
	t1 = time.Now()
	want = x.startDelay(100)
	return timeBetween(num(r, "atMs"), clk.early(t0)+want, clk.late(t1)+want,
		"atMs after a resume and a status without rttMs (the reported 100 kept)")
}

// scUnavailable: a participant that reported unavailable for the current epoch isn't counted, still keeps the room
// going while in the player, and is counted again for the next epoch (§7.5, §12.3, §12.10).
func scUnavailable(x *sc) error {
	cs, err := x.roomOf(2)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	_ = status(a, 0, "loading", nil, 10)
	_ = status(b, 0, "loading", nil, 10)
	if err := waitPlayers(cs, "loading"); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)})
	r, err := a.waitFor(reply, "waiting on both", waitingOnIs(a.PeerID, b.PeerID))
	if err != nil {
		return err
	}
	if num(r, "positionMs") != 0 {
		return fmt.Errorf("setMedia without positionMs: positionMs %v, want 0", r["positionMs"])
	}
	_ = status(b, 1, "loading", Msg{"state": "unavailable"}, 10)
	if _, err := a.waitFor(reply, "waitingOn [A]", waitingOnIs(a.PeerID)); err != nil {
		return err
	}
	_ = status(a, 1, "ready", matched(), 10)
	if _, err := a.waitFor(reply, "playing without B", isPhase("playing")); err != nil {
		return err
	}
	if _, err := b.waitFor(reply, "playing without B", isPhase("playing")); err != nil {
		return err
	}
	_ = status(a, 1, "idle", nil, 10)
	r, err = b.waitFor(reply, "A idle", playerIs(a.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(r, "phase") != "playing" {
		return fmt.Errorf("A left the player while B (unavailable, loading) is in it: %s; want playing", brief(r))
	}
	_ = status(a, 1, "ready", matched(), 10)
	if _, err := a.waitFor(reply, "A ready", playerIs(a.PeerID, "ready")); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "setMedia", "media": sampleMedia(3_600_000)})
	if _, err := a.waitFor(reply, "waiting on both for epoch 2", isRoom(func(m Msg) bool {
		return num(m, "epoch") == 2 && str(m, "phase") == "waiting" &&
			equalStrings(strList(m, "waitingOn"), []string{a.PeerID, b.PeerID})
	})); err != nil {
		return fmt.Errorf("unavailable reported for epoch 1 after setMedia: %w", err)
	}
	return nil
}

// recompute checks §12.5: after a disconnect, leave or kick of the only waited-on participant, waitingOn is
// recomputed and the room released at once, in the one snapshot the event publishes, long before the barrier
// timeout.
func recompute(how string) func(x *sc) error {
	return func(x *sc) error {
		cs, err := x.playing(3, 10, 7_200_000)
		if err != nil {
			return err
		}
		a, b, c := cs[0], cs[1], cs[2]
		_ = status(c, 1, "loading", matched(), 10)
		for _, o := range []*Client{a, b} {
			if _, err := o.waitFor(reply, "C loading", playerIs(c.PeerID, "loading")); err != nil {
				return err
			}
		}
		_ = a.send(Msg{"type": "seek", "positionMs": 40000})
		for _, o := range []*Client{a, b} {
			if _, err := o.waitFor(reply, "waitingOn [C]", waitingOnIs(c.PeerID)); err != nil {
				return err
			}
		}
		t0 := time.Now()
		switch how {
		case "disconnect":
			c.drop()
		case "leave":
			_ = c.send(Msg{"type": "leave"})
		case "kick":
			_ = a.send(Msg{"type": "kick", "peerId": c.PeerID})
		}
		for _, o := range []*Client{a, b} {
			r, err := o.waitFor(reply, "room after the "+how, isType("room"))
			if err != nil {
				return err
			}
			el := time.Since(t0)
			k, _ := actionOf(r)
			p := participant(r, c.PeerID)
			gone := p == nil
			if how == "disconnect" {
				gone = p != nil && p["connected"] == false
			}
			if str(r, "phase") != "playing" || len(strList(r, "waitingOn")) != 0 || num(r, "positionMs") != 40000 ||
				k != "" || !gone {
				return fmt.Errorf("%s: first room after the %s of the only waited-on participant: %s; want playing at "+
					"40000 without it, no action", o.name, how, brief(r))
			}
			if el > x.polD("waitMs")/3 {
				return fmt.Errorf("%s: released %v after the %s; want at once (barrier timeout %v)", o.name,
					el.Round(time.Millisecond), how, x.polD("waitMs"))
			}
		}
		return nil
	}
}

// scBarrierEvents: waitingOn is recomputed only by peer events: a participant that resumes while the room waits isn't
// added until the next status, which then waits for it (§7.3, §12.5).
func scBarrierEvents(x *sc) error {
	cs, err := x.playing(3, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b, c := cs[0], cs[1], cs[2]
	_ = status(b, 1, "loading", matched(), 10)
	if _, err := a.waitFor(reply, "B loading", playerIs(b.PeerID, "loading")); err != nil {
		return err
	}
	b.drop()
	for _, o := range []*Client{a, c} {
		if _, err := o.waitFor(reply, "B disconnected", connectedIs(b.PeerID, false)); err != nil {
			return err
		}
	}
	_ = status(c, 1, "loading", matched(), 10)
	if _, err := a.waitFor(reply, "C loading", playerIs(c.PeerID, "loading")); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "seek", "positionMs": 40000})
	for _, o := range []*Client{a, c} {
		if _, err := o.waitFor(reply, "waitingOn [C]", waitingOnIs(c.PeerID)); err != nil {
			return err
		}
	}
	b2, j, err := x.resume(b, "B2")
	if err != nil {
		return err
	}
	if r := obj(j, "room"); !equalStrings(strList(r, "waitingOn"), []string{c.PeerID}) {
		return fmt.Errorf("joined after a resume while waiting: waitingOn %v; want [C] unchanged", strList(r, "waitingOn"))
	}
	for _, o := range []*Client{a, c} {
		r, err := o.waitFor(reply, "room after B's resume", isType("room"))
		if err != nil {
			return err
		}
		if p := participant(r, b.PeerID); p["connected"] != true || !equalStrings(strList(r, "waitingOn"), []string{c.PeerID}) {
			return fmt.Errorf("%s: room after B (loading) resumed while waiting: %s; want B connected, waitingOn [C] unchanged",
				o.name, brief(r))
		}
	}
	_ = status(c, 1, "ready", matched(), 10)
	for _, o := range []*Client{a, b2} {
		r, err := o.waitFor(reply, "room after C ready", isType("room"))
		if err != nil {
			return err
		}
		if str(r, "phase") != "waiting" || !equalStrings(strList(r, "waitingOn"), []string{b.PeerID}) {
			return fmt.Errorf("%s: room after C ready: %s; want waiting on B (counted again since its resume)", o.name, brief(r))
		}
	}
	_ = status(b2, 1, "ready", matched(), 10)
	_, err = a.waitFor(reply, "playing after B ready", isPhase("playing"))
	return err
}

// scBarrierTimeout: when the barrier times out after waitMs, the participants it waited for are excluded for
// exclusionMs and the room releases with no action. An excluded participant holds no barrier and no stall,
// also after a disconnect and resume, and keeps the room going while in the player; after exclusionMs it counts
// again (§12.6, §12.7, §12.9, §12.10).
func scBarrierTimeout(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	wait, excl, grace := x.polD("waitMs"), x.polD("exclusionMs"), x.polD("stallGraceMs")
	_ = status(b, 1, "loading", matched(), 10)
	if _, err := a.waitFor(reply, "B loading", playerIs(b.PeerID, "loading")); err != nil {
		return err
	}
	t := time.Now()
	_ = a.send(Msg{"type": "seek", "positionMs": 40000})
	if _, err := a.waitFor(reply, "waiting on B", waitingOnIs(b.PeerID)); err != nil {
		return err
	}
	r, err := a.waitFor(wait+3*time.Second, "release by timeout", isPhase("playing"))
	if err != nil {
		return err
	}
	if err := within(time.Since(t), wait-slackD, wait+500*time.Millisecond, "barrier timeout"); err != nil {
		return err
	}
	if k, _ := actionOf(r); len(strList(r, "waitingOn")) != 0 || participant(r, b.PeerID) == nil ||
		num(r, "positionMs") != 40000 || k != "" {
		return fmt.Errorf("after the barrier timeout: %s; want playing at 40000, no action", brief(r))
	}
	// Excluded B holds no new barrier...
	seekReleases := func(pos float64, what string) error {
		_ = a.send(Msg{"type": "seek", "positionMs": pos})
		r, err := a.waitFor(reply, "room after seek", isType("room"))
		if err != nil {
			return err
		}
		if k, by := actionOf(r); str(r, "phase") != "playing" || num(r, "positionMs") != pos || k != "seek" || by != a.PeerID {
			return fmt.Errorf("seek %s: %s; want playing at %.0f at once (B excluded), seek by A", what, brief(r), pos)
		}
		return nil
	}
	if err := seekReleases(50000, "while B (loading) is excluded"); err != nil {
		return err
	}
	// ...also after a disconnect and resume...
	b.drop()
	if _, err := a.waitFor(reply, "B disconnected", connectedIs(b.PeerID, false)); err != nil {
		return err
	}
	b2, _, err := x.resume(b, "B2")
	if err != nil {
		return err
	}
	if _, err := a.waitFor(reply, "B connected", connectedIs(b.PeerID, true)); err != nil {
		return err
	}
	if err := seekReleases(60000, "after excluded B disconnected and resumed"); err != nil {
		return err
	}
	// ...and no stall.
	_ = status(b2, 1, "buffering", matched(), 10)
	if _, err := a.waitFor(reply, "B buffering", playerIs(b.PeerID, "buffering")); err != nil {
		return err
	}
	if err := a.expectNone(grace+400*time.Millisecond, "stall from an excluded participant", isPhase("waiting")); err != nil {
		return err
	}
	// Excluded B in the player keeps the room going when A leaves it.
	_ = status(a, 1, "idle", nil, 10)
	r, err = a.waitFor(reply, "A idle", playerIs(a.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(r, "phase") != "playing" {
		return fmt.Errorf("A left the player while excluded B is in it: %s; want playing", brief(r))
	}
	_ = status(a, 1, "ready", matched(), 10)
	if _, err := a.waitFor(reply, "A ready", playerIs(a.PeerID, "ready")); err != nil {
		return err
	}
	if left := time.Until(t.Add(wait + excl)); left < 500*time.Millisecond {
		return fmt.Errorf("suite too slow: the exclusion checks ended %v before the exclusion did", left)
	}
	time.Sleep(time.Until(t.Add(wait + excl + 500*time.Millisecond)))
	_ = status(b2, 1, "ready", matched(), 10)
	if _, err := a.waitFor(reply, "B ready", playerIs(b.PeerID, "ready")); err != nil {
		return err
	}
	_ = status(b2, 1, "buffering", matched(), 10)
	r, err = a.waitFor(grace+time.Second, "stall once the exclusion ended", isPhase("waiting"))
	if err != nil {
		return err
	}
	if k, by := actionOf(r); k != "stall" || by != b.PeerID {
		return fmt.Errorf("stall after the exclusion: %s; want action stall by B", brief(r))
	}
	return nil
}

// scSetMediaTimeout: setMedia clears exclusions and its barrier times out after mediaWaitMs (§7.10, §12.7).
func scSetMediaTimeout(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	_ = status(b, 1, "loading", matched(), 10)
	if _, err := a.waitFor(reply, "B loading", playerIs(b.PeerID, "loading")); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "seek", "positionMs": 1000})
	if _, err := a.waitFor(x.polD("waitMs")+3*time.Second, "release by timeout (B excluded)", isRoom(func(m Msg) bool {
		return str(m, "phase") == "playing" && num(m, "positionMs") == 1000
	})); err != nil {
		return err
	}
	t := time.Now()
	_ = a.send(Msg{"type": "setMedia", "media": sampleMedia(3_600_000)})
	if _, err := a.waitFor(reply, "waiting on both (exclusions cleared)", isRoom(func(m Msg) bool {
		return num(m, "epoch") == 2 && str(m, "phase") == "waiting" &&
			equalStrings(strList(m, "waitingOn"), []string{a.PeerID, b.PeerID})
	})); err != nil {
		return err
	}
	_ = status(a, 2, "ready", matched(), 10)
	mw := x.polD("mediaWaitMs")
	if _, err := a.waitFor(mw+3*time.Second, "release by the media timeout", isRoom(func(m Msg) bool {
		return num(m, "epoch") == 2 && str(m, "phase") == "playing"
	})); err != nil {
		return err
	}
	return within(time.Since(t), mw-slackD, mw+500*time.Millisecond, "setMedia barrier timeout")
}

// scStall: buffering in playing for stallGraceMs enters a stall wait at P(now) with action stall; it releases
// stallReleaseMs after waitingOn empties, and not if waitingOn fills again first; a ready within the grace cancels
// it; buffering for another epoch starts none (§12.5, §12.9).
func scStall(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	grace, release := x.polD("stallGraceMs"), x.polD("stallReleaseMs")
	clk, err := a.clock()
	if err != nil {
		return err
	}
	room := a.lastRoom()
	t0 := time.Now()
	_ = status(b, 1, "buffering", matched(), 10)
	if _, err := a.waitFor(reply, "B buffering", playerIs(b.PeerID, "buffering")); err != nil {
		return err
	}
	r, err := a.waitFor(grace+reply, "stall", isPhase("waiting"))
	if err != nil {
		return err
	}
	t1 := time.Now()
	k, by := actionOf(r)
	if k != "stall" || by != b.PeerID || !equalStrings(strList(r, "waitingOn"), []string{b.PeerID}) {
		return fmt.Errorf("stall snapshot %s; want waiting on B, action stall by B", brief(r))
	}
	if err := within(t1.Sub(t0), grace-slackD, grace+300*time.Millisecond, "stall after buffering"); err != nil {
		return err
	}
	if err := posBetween(num(r, "positionMs"), room, clk.early(t0)+x.pol("stallGraceMs"), clk.late(t1), "stall position"); err != nil {
		return err
	}
	// waitingOn empties and fills again before the stall release timer fires: no release.
	if err := b.sendBatch(statusMsg(1, "ready", matched(), 10), statusMsg(1, "buffering", matched(), 10)); err != nil {
		return err
	}
	if _, err := a.waitFor(reply, "B buffering again", isRoom(func(m Msg) bool {
		return str(participant(m, b.PeerID), "player") == "buffering" && equalStrings(strList(m, "waitingOn"), []string{b.PeerID})
	})); err != nil {
		return err
	}
	if err := a.expectNone(release+300*time.Millisecond, "release although waitingOn filled again within stallReleaseMs",
		isPhase("playing")); err != nil {
		return err
	}
	t0 = time.Now()
	_ = status(b, 1, "ready", matched(), 10)
	if _, err := a.waitFor(reply, "B ready", isRoom(func(m Msg) bool {
		return str(m, "phase") == "waiting" && len(strList(m, "waitingOn")) == 0
	})); err != nil {
		return fmt.Errorf("first room after B ready in a stall wait: %w", err)
	}
	r, err = a.waitFor(release+reply, "playing after the stall", isPhase("playing"))
	if err != nil {
		return err
	}
	if err := within(time.Since(t0), release-slackD, release+300*time.Millisecond, "release after a stall"); err != nil {
		return err
	}
	if k, _ := actionOf(r); k != "" {
		return fmt.Errorf("release after the stall carries an action: %s", brief(r))
	}
	// buffering cancelled by ready within the grace: no stall.
	if err := b.sendBatch(statusMsg(1, "buffering", matched(), 10), statusMsg(1, "ready", matched(), 10)); err != nil {
		return err
	}
	if err := a.expectNone(grace+300*time.Millisecond, "stall after a short buffering", isPhase("waiting")); err != nil {
		return err
	}
	// buffering for another epoch isn't stalled (§12.9).
	_ = status(b, 0, "buffering", matched(), 10)
	if _, err := a.waitFor(reply, "B buffering (old epoch)", playerIs(b.PeerID, "buffering")); err != nil {
		return err
	}
	return a.expectNone(grace+300*time.Millisecond, "stall from a buffering status for another epoch", isPhase("waiting"))
}

// scSeekCoalesce: a seek within seekCoalesceMs of the last applied one is held, replaced by later ones, and applied
// seekCoalesceMs after the last, under the rules of the phase at that moment; setMedia drops a held seek (§12.8).
func scSeekCoalesce(x *sc) error {
	cs, err := x.paused(2, 7_200_000)
	if err != nil {
		return err
	}
	a := cs[0]
	co := x.polD("seekCoalesceMs")
	seekRoom := func(phase string, pos float64) func(Msg) bool {
		return func(m Msg) bool {
			k, _ := actionOf(m)
			return str(m, "phase") == phase && num(m, "positionMs") == pos && k == "seek"
		}
	}
	nextRoom := func(what string, timeout time.Duration, want func(Msg) bool) (Msg, error) {
		r, err := a.waitFor(timeout, what, isType("room"))
		if err != nil {
			return nil, err
		}
		if !want(r) {
			return nil, fmt.Errorf("next room: %s; want %s", brief(r), what)
		}
		return r, nil
	}
	time.Sleep(co + 50*time.Millisecond)
	t0 := time.Now()
	if err := a.sendBatch(Msg{"type": "seek", "positionMs": 1000}, Msg{"type": "seek", "positionMs": 2000},
		Msg{"type": "seek", "positionMs": 3000}); err != nil {
		return err
	}
	if _, err := nextRoom("paused at 1000 (first seek applied at once)", reply, seekRoom("paused", 1000)); err != nil {
		return err
	}
	t1 := time.Now()
	r, err := nextRoom("paused at 3000 (the last held seek; 2000 replaced)", co+reply, seekRoom("paused", 3000))
	if err != nil {
		return err
	}
	if _, by := actionOf(r); by != a.PeerID {
		return fmt.Errorf("held seek applied by %v, want its sender", by)
	}
	if err := errs(within(time.Since(t0), co-slackD, co+time.Hour, "held seek after the batch"),
		within(time.Since(t1), 0, co+500*time.Millisecond, "held seek after the first applied one")); err != nil {
		return err
	}
	if err := a.expectNone(co+300*time.Millisecond, "a further seek", isType("room")); err != nil {
		return err
	}
	// The held seek follows the phase at the moment it is applied: here play came in between.
	time.Sleep(co + 50*time.Millisecond)
	t0 = time.Now()
	if err := a.sendBatch(Msg{"type": "seek", "positionMs": 10000}, Msg{"type": "seek", "positionMs": 20000},
		Msg{"type": "play"}); err != nil {
		return err
	}
	if _, err := nextRoom("paused at 10000", reply, seekRoom("paused", 10000)); err != nil {
		return err
	}
	if _, err := nextRoom("playing at 10000 (play, everyone ready)", reply, func(m Msg) bool {
		k, _ := actionOf(m)
		return str(m, "phase") == "playing" && num(m, "positionMs") == 10000 && k == "play"
	}); err != nil {
		return err
	}
	if _, err := nextRoom("playing at 20000 (held seek applied in playing, everyone ready)", co+reply,
		seekRoom("playing", 20000)); err != nil {
		return err
	}
	if err := within(time.Since(t0), co-slackD, co+time.Hour, "held seek after the batch"); err != nil {
		return err
	}
	// setMedia drops the held seek.
	time.Sleep(co + 50*time.Millisecond)
	if err := a.sendBatch(Msg{"type": "seek", "positionMs": 1000}, Msg{"type": "seek", "positionMs": 2000},
		Msg{"type": "setMedia", "media": sampleMedia(3_600_000)}); err != nil {
		return err
	}
	if _, err := nextRoom("playing at 1000", reply, seekRoom("playing", 1000)); err != nil {
		return err
	}
	if _, err := nextRoom("the media, waiting for epoch 2", reply, func(m Msg) bool {
		return hasMedia(m) && num(m, "epoch") == 2 && str(m, "phase") == "waiting"
	}); err != nil {
		return err
	}
	return a.expectNone(co+300*time.Millisecond, "the held seek applied after setMedia", isType("room"))
}

// scSeekHeldControl: a held seek is applied even if its sender lost control after it arrived (§12.8). Control changes
// on another connection within seekCoalesceMs, so with --relay-bin this runs on an unscaled relay of its own.
func scSeekHeldControl(x *sc) error {
	y := x
	if x.relay != nil {
		p, err := x.startRelay(1)
		if err != nil {
			return err
		}
		y = x.on(p, 1)
	} else if x.pol("seekCoalesceMs") < 150 {
		return skipf("seekCoalesceMs is %.0f ms; needs --relay-bin or a relay where it is 150 ms or more", x.pol("seekCoalesceMs"))
	}
	cs, err := y.paused(2, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	_ = a.send(Msg{"type": "setControl", "mode": "everyone"})
	if _, err := b.waitFor(reply, "control everyone", isRoom(func(m Msg) bool { return str(m, "control") == "everyone" })); err != nil {
		return err
	}
	time.Sleep(y.polD("seekCoalesceMs") + 50*time.Millisecond)
	if err := b.sendBatch(Msg{"type": "seek", "positionMs": 1000}, Msg{"type": "seek", "positionMs": 2000}); err != nil {
		return err
	}
	if _, err := a.waitFor(reply, "seek 1000 by B", isRoom(func(m Msg) bool { return num(m, "positionMs") == 1000 })); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "setControl", "mode": "host"})
	r, err := a.waitFor(reply, "control host", isType("room"))
	if err != nil {
		return err
	}
	if str(r, "control") != "host" || num(r, "positionMs") != 1000 {
		return fmt.Errorf("room after setControl host: %s; want control host before the held seek (inconclusive if "+
			"the held seek came first)", brief(r))
	}
	r, err = a.waitFor(y.polD("seekCoalesceMs")+reply, "held seek by B", isType("room"))
	if err != nil {
		return err
	}
	if k, by := actionOf(r); num(r, "positionMs") != 2000 || k != "seek" || by != b.PeerID {
		return fmt.Errorf("held seek after its sender lost control: %s; want paused at 2000, seek by B", brief(r))
	}
	return nil
}

// scNobodyInPlayer: in playing and in waiting, the room pauses at P(now) with no action once no connected
// participant is in the player, by status or by disconnect (§12.5 step 1, §12.10).
func scNobodyInPlayer(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	_ = status(a, 1, "idle", nil, 10)
	r, err := b.waitFor(reply, "A idle", playerIs(a.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(r, "phase") != "playing" {
		return fmt.Errorf("one idle participant paused the room: %s", brief(r))
	}
	_ = status(b, 1, "idle", nil, 10)
	// B's status is one event: the snapshot showing B idle is already paused.
	r, err = a.waitFor(reply, "room with B idle", playerIs(b.PeerID, "idle"))
	if err != nil {
		return err
	}
	if k, _ := actionOf(r); str(r, "phase") != "paused" || k != "" {
		return fmt.Errorf("first room once nobody is in the player: %s; want paused, no action", brief(r))
	}
	// In waiting.
	cs, err = x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	c, d := cs[0], cs[1]
	_ = status(d, 1, "loading", matched(), 10)
	if _, err := c.waitFor(reply, "D loading", playerIs(d.PeerID, "loading")); err != nil {
		return err
	}
	_ = c.send(Msg{"type": "seek", "positionMs": 40000})
	if _, err := c.waitFor(reply, "waiting on D", waitingOnIs(d.PeerID)); err != nil {
		return err
	}
	_ = status(c, 1, "idle", nil, 10)
	r, err = d.waitFor(reply, "C idle", playerIs(c.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(r, "phase") != "waiting" || !equalStrings(strList(r, "waitingOn"), []string{d.PeerID}) {
		return fmt.Errorf("C left the player while D loads: %s; want still waiting on D", brief(r))
	}
	_ = status(d, 1, "idle", nil, 10)
	r, err = c.waitFor(reply, "room with D idle", playerIs(d.PeerID, "idle"))
	if err != nil {
		return err
	}
	if k, _ := actionOf(r); str(r, "phase") != "paused" || num(r, "positionMs") != 40000 || k != "" ||
		len(strList(r, "waitingOn")) != 0 {
		return fmt.Errorf("first room once nobody is in the player while waiting: %s; want paused at 40000, "+
			"waitingOn [], no action", brief(r))
	}
	return nil
}

// scEffectivePlayer: a participant's effective player is loading until it reports on the room's epoch (§12.3). After
// setMedia an idle participant is waited for, and a status still in flight for the old media neither pauses the room
// as nobody in the player nor takes anyone off the barrier (§7.10, §12.5).
func scEffectivePlayer(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	_ = status(b, 1, "idle", nil, 10)
	r, err := a.waitFor(reply, "B idle", playerIs(b.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(r, "phase") != "playing" {
		return fmt.Errorf("B left the player while A plays: %s; want playing", brief(r))
	}
	_ = a.send(Msg{"type": "setMedia", "media": sampleMedia(3_600_000)})
	for _, c := range cs {
		r, err := c.waitFor(reply, "room with the media", hasMedia)
		if err != nil {
			return err
		}
		if num(r, "epoch") != 2 || str(r, "phase") != "waiting" || !equalStrings(strList(r, "waitingOn"), []string{a.PeerID, b.PeerID}) {
			return fmt.Errorf("%s: room after setMedia with B idle on the old media: %s; want epoch 2, waiting on A and B",
				c.name, brief(r))
		}
	}
	// A reports idle for the old epoch: its effective player stays loading.
	_ = status(a, 1, "idle", nil, 10)
	for _, c := range cs {
		r, err := c.waitFor(reply, "A idle", playerIs(a.PeerID, "idle"))
		if err != nil {
			return err
		}
		if str(r, "phase") != "waiting" || !equalStrings(strList(r, "waitingOn"), []string{a.PeerID, b.PeerID}) {
			return fmt.Errorf("%s: everyone reported idle for epoch 1 while the room has epoch 2: %s; want still "+
				"waiting on A and B", c.name, brief(r))
		}
	}
	_ = status(a, 2, "ready", matched(), 10)
	for _, c := range cs {
		if _, err := c.waitFor(reply, "waiting on B", waitingOnIs(b.PeerID)); err != nil {
			return err
		}
	}
	// B reports idle for the new epoch: it isn't counted any more, and the room releases.
	_ = status(b, 2, "idle", nil, 10)
	r, err = a.waitFor(reply, "room after B idle for epoch 2", isType("room"))
	if err != nil {
		return err
	}
	if k, _ := actionOf(r); str(r, "phase") != "playing" || len(strList(r, "waitingOn")) != 0 || k != "" {
		return fmt.Errorf("first room after B reported idle for the new media: %s; want the release, no action", brief(r))
	}
	return nil
}

// scIdleAfterDisconnect: the disconnect of the only participant in the player pauses the room at P(now) (§11.5 step
// 4, §12.5 step 1).
func scIdleAfterDisconnect(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	clk, err := b.clock()
	if err != nil {
		return err
	}
	_ = status(b, 1, "idle", nil, 10)
	room, err := b.waitFor(reply, "B idle", playerIs(b.PeerID, "idle"))
	if err != nil {
		return err
	}
	if str(room, "phase") != "playing" {
		return fmt.Errorf("one idle participant paused the room: %s", brief(room))
	}
	t0 := time.Now()
	a.drop()
	r, err := b.waitFor(reply, "room after the disconnect", isType("room"))
	if err != nil {
		return err
	}
	t1 := time.Now()
	if k, _ := actionOf(r); str(r, "phase") != "paused" || k != "" || participant(r, a.PeerID)["connected"] != false {
		return fmt.Errorf("first room after the disconnect: %s; want A disconnected, paused, no action", brief(r))
	}
	return posBetween(num(r, "positionMs"), room, clk.early(t0), clk.late(t1), "pause position")
}

// scEnded: in playing, the room pauses at durationMs with action ended by null when P reaches it, by timer, as
// rescheduled by rate; a play at the end releases and ends again in the same event; leaving playing stops the timer
// (§7.9, §12.6 step 5, §12.11).
func scEnded(x *sc) error {
	const dur = 4000
	cs, err := x.playing(2, 10, dur)
	if err != nil {
		return err
	}
	a := cs[0]
	clk, err := a.clock()
	if err != nil {
		return err
	}
	time.Sleep(time.Duration(math.Max(0, num(a.lastRoom(), "atMs")-clk.early(time.Now())+50) * float64(time.Millisecond)))
	_ = a.send(Msg{"type": "rate", "rate": 2.0})
	r, err := a.waitFor(reply, "rate 2", isRoom(func(m Msg) bool { return num(m, "rate") == 2 }))
	if err != nil {
		return err
	}
	if str(r, "phase") != "playing" || num(r, "positionMs") >= dur {
		return fmt.Errorf("room after rate 2: %s; want playing before the end", brief(r))
	}
	e, err := a.waitFor(time.Duration(dur)*time.Millisecond+reply, "paused at the end", isPhase("paused"))
	if err != nil {
		return err
	}
	t1 := time.Now()
	if k, by := actionOf(e); num(e, "positionMs") != dur || k != "ended" || by != nil {
		return fmt.Errorf("end snapshot %s; want paused at %d, action ended by null", brief(e), dur)
	}
	// The end timer fires at atMs + ceil((durationMs − positionMs) / rate) of the rate snapshot. The snapshot can't
	// arrive before that and must follow it closely.
	end := num(r, "atMs") + math.Ceil((dur-num(r, "positionMs"))/2)
	if err := timeBetween(end, clk.early(t1)-150, clk.late(t1), "end time after rate 2 (relay clock; arrival − 150 ms "+
		"to arrival)"); err != nil {
		return err
	}
	_ = a.send(Msg{"type": "play"})
	p, err := a.waitFor(reply, "room after play at the end", isType("room"))
	if err != nil {
		return err
	}
	if k, by := actionOf(p); str(p, "phase") != "paused" || num(p, "positionMs") != dur || k != "ended" || by != nil {
		return fmt.Errorf("first room after play at the end: %s; want one snapshot paused at %d with action ended by null",
			brief(p), dur)
	}
	if err := a.expectNone(300*time.Millisecond, "a second snapshot for one play", isType("room")); err != nil {
		return err
	}
	// Play from 1000 ms before the end at rate 2, then pause at once: no ended.
	_ = a.send(Msg{"type": "seek", "positionMs": dur - 1000})
	if _, err := a.waitFor(reply, "paused before the end", isRoom(func(m Msg) bool { return num(m, "positionMs") == dur-1000 })); err != nil {
		return err
	}
	pl, err := playNow(a, a)
	if err != nil {
		return err
	}
	_ = a.send(Msg{"type": "pause"})
	ps, err := a.waitFor(reply, "room after pause", isType("room"))
	if err != nil {
		return err
	}
	if k, _ := actionOf(ps); str(ps, "phase") != "paused" || k != "pause" || num(ps, "positionMs") >= dur {
		return fmt.Errorf("pause before the end: %s", brief(ps))
	}
	endAt := num(pl, "atMs") + 500 - clk.early(time.Now())
	return a.expectNone(time.Duration(max(endAt, 0)*float64(time.Millisecond))+300*time.Millisecond,
		"ended after the room left playing", isType("room"))
}
