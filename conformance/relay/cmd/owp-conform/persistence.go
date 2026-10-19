package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// SPEC §14: persistence and restart, with §3.1's shutdown.

// scRestart: SIGTERM writes the rooms and closes every connection with 1001. The restarted relay restores each room
// paused at its position, with rev continuing and epoch, media, rate, control and locked kept, every participant
// disconnected with player idle and its match and offsetMs kept, tokens working and no reported rttMs.
// Until its first status a restored participant counts as loading, so a play waits for it. The host's absence starts
// at the restore; an ended room stays gone; restored rooms count for no IP; the state is readable by the relay's user
// only (§3.1, §11.10, §12.3, §14).
func scRestart(x *sc) error {
	cs, err := x.playing(2, 10, 7_200_000)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	// B plays another cut: its match and offsetMs must survive.
	bMatch := normalize(Msg{"state": "mismatched", "deltaMs": 2500})
	bStatus := func(c *Client, rtt int) error {
		m := Msg{"type": "status", "epoch": 1, "player": "ready", "match": bMatch, "offsetMs": -2500}
		if rtt >= 0 {
			m["rttMs"] = rtt
		}
		return c.send(m)
	}
	_ = bStatus(b, 10)
	if _, err := a.waitFor(reply, "B offset", isRoom(func(m Msg) bool { return num(participant(m, b.PeerID), "offsetMs") == -2500 })); err != nil {
		return err
	}
	clk, err := a.clock()
	if err != nil {
		return err
	}
	time.Sleep(time.Duration(max(0, num(a.lastRoom(), "atMs")-clk.early(time.Now())+50) * float64(time.Millisecond)))
	for _, m := range []Msg{{"type": "rate", "rate": 1.5}, {"type": "setControl", "mode": "everyone"}, {"type": "lock", "locked": true}} {
		_ = a.send(m)
		if _, err := a.waitFor(reply, "room after "+str(m, "type"), isType("room")); err != nil {
			return err
		}
	}
	room := a.lastRoom()
	if room["locked"] != true || num(room, "rate") != 1.5 || str(room, "control") != "everyone" {
		return fmt.Errorf("room before the restart: %s", brief(room))
	}
	// Room 2: only the member comes back, so it is promoted hostPromotionMs after the restore.
	r2, err := x.roomOf(2)
	if err != nil {
		return err
	}
	// Room 3: ended before the restart.
	e, _, err := x.create("E")
	if err != nil {
		return err
	}
	_ = e.send(Msg{"type": "end"})
	if _, err := e.waitFor(reply, "closed ended", isType("closed")); err != nil {
		return err
	}
	// One IP at its room allowance; the hosts disconnect, the rooms stay.
	addr := nextAddr()
	for i := range int(x.lim("maxRoomsPerIp")) {
		h, err := x.dialFrom(fmt.Sprintf("H%d", i), addr)
		if err != nil {
			return err
		}
		if _, err := enter(h, createMsg(h.name)); err != nil {
			return err
		}
		h.drop()
	}
	over, err := x.dialFrom("Over", addr)
	if err != nil {
		return err
	}
	j, err := enter(over, createMsg("Over"))
	if err := refusedWith(j, err, "rate_limited", "create from an IP at maxRoomsPerIp"); err != nil {
		return err
	}
	open := []*Client{a, b, r2[0], r2[1], e, over}

	t0 := time.Now()
	if err := x.relay.stop(); err != nil {
		return err
	}
	t1 := time.Now()
	for _, c := range open {
		if err := closedWith(c, websocket.CloseGoingAway, false); err != nil {
			return fmt.Errorf("on SIGTERM: %w", err)
		}
	}
	if bad, err := readableByOthers(x.relay.stateDir()); err != nil {
		return err
	} else if len(bad) > 0 {
		return fmt.Errorf("persisted state readable by other users: %s", strings.Join(bad, ", "))
	}
	starting := time.Now()
	if err := x.relay.start(); err != nil {
		return err
	}
	restored := time.Now()

	m2, j, err := x.resume(r2[1], "M2")
	if err != nil {
		return fmt.Errorf("resume after the restart: %w", err)
	}
	if hostOf(obj(j, "room")) != r2[0].PeerID {
		return fmt.Errorf("room 2 right after the restore: host %q, want %q", hostOf(obj(j, "room")), r2[0].PeerID)
	}
	a2, j, err := x.resume(a, "A2")
	if err != nil {
		return fmt.Errorf("resume after the restart: %w", err)
	}
	r := withType(obj(j, "room"))
	if str(j, "code") != a.Code || num(r, "rev") <= num(room, "rev") || num(r, "epoch") != 1 || str(r, "phase") != "paused" ||
		len(strList(r, "waitingOn")) != 0 || num(r, "rate") != 1.5 || str(r, "control") != "everyone" || r["locked"] != true ||
		hostOf(r) != a.PeerID {
		return fmt.Errorf("restored room %s, host %s; want rev above %v, epoch 1, paused, rate 1.5, control everyone, "+
			"locked, host A", brief(r), hostOf(r), room["rev"])
	}
	if ps := participants(r); len(ps) != 2 || str(ps[0], "peerId") != a.PeerID || str(ps[1], "peerId") != b.PeerID {
		return fmt.Errorf("restored participants %v; want A then B", ps)
	}
	if err := posBetween(num(r, "positionMs"), room, clk.early(t0), clk.late(t1), "restored positionMs"); err != nil {
		return err
	}
	if num(r, "atMs") > num(j, "serverTime") {
		return fmt.Errorf("restored atMs %v after joined serverTime %v; want the relay time of the restore", r["atMs"], j["serverTime"])
	}
	if err := mediaDiff(j["media"], sampleMedia(7_200_000)); err != nil {
		return fmt.Errorf("restored media: %w", err)
	}
	if p := participant(r, b.PeerID); p["connected"] != false || str(p, "player") != "idle" || str(p, "name") != "P1" ||
		str(p, "client") != clientName || !reflect.DeepEqual(p["match"], any(bMatch)) || num(p, "offsetMs") != -2500 {
		return fmt.Errorf("restored B = %v; want disconnected, player idle, name, client, match and offsetMs kept", p)
	}
	if p := participant(r, a.PeerID); p["connected"] != true || str(p, "player") != "idle" ||
		!reflect.DeepEqual(p["match"], any(normalize(matched()))) {
		return fmt.Errorf("restored A after its resume = %v; want connected, player idle, match kept", p)
	}
	b2, _, err := x.resume(b, "B2")
	if err != nil {
		return fmt.Errorf("resume after the restart: %w", err)
	}
	if _, err := a2.waitFor(reply, "B connected", connectedIs(b.PeerID, true)); err != nil {
		return err
	}
	// Neither has sent a status since the restore, so both count as loading (§12.3, §14): play waits for both.
	_ = b2.send(Msg{"type": "play"})
	w, err := a2.waitFor(reply, "room after play", isType("room"))
	if err != nil {
		return err
	}
	if k, by := actionOf(w); str(w, "phase") != "waiting" || num(w, "positionMs") != num(r, "positionMs") ||
		!equalStrings(strList(w, "waitingOn"), []string{a.PeerID, b.PeerID}) || k != "play" || by != b.PeerID {
		return fmt.Errorf("first room after play with no status since the restore: %s; want waiting on A and B at the "+
			"restored %v, play by B", brief(w), r["positionMs"])
	}
	// Statuses without rttMs: no rttMs survived the restart, so the start delay counts defaultRttMs for both.
	_ = status(a2, 1, "ready", matched(), -1)
	if _, err := a2.waitFor(reply, "waiting on B", waitingOnIs(b.PeerID)); err != nil {
		return err
	}
	clk2, err := a2.clock()
	if err != nil {
		return err
	}
	t2 := time.Now()
	_ = bStatus(b2, -1)
	p, err := a2.waitFor(reply, "room after B ready", isType("room"))
	if err != nil {
		return err
	}
	t3 := time.Now()
	if k, _ := actionOf(p); str(p, "phase") != "playing" || num(p, "positionMs") != num(r, "positionMs") || k != "" {
		return fmt.Errorf("first room after B ready: %s; want playing from the restored %v, no action", brief(p), r["positionMs"])
	}
	want := x.startDelay(x.pol("defaultRttMs"))
	if err := timeBetween(num(p, "atMs"), clk2.early(t2)+want, clk2.late(t3)+want,
		"atMs after the restart (no rttMs persisted, so defaultRttMs)"); err != nil {
		return err
	}
	hp := x.polD("hostPromotionMs")
	pr, err := m2.waitFor(hp+3*time.Second, "promotion in room 2", isRoom(func(m Msg) bool { return hostOf(m) == r2[1].PeerID }))
	if err != nil {
		return err
	}
	if k, by := actionOf(pr); k != "host" || by != nil {
		return fmt.Errorf("promotion after the restore: %s; want action host by null", brief(pr))
	}
	// The absence starts at the restore: not before the process started, and about when it printed `listening`.
	if err := errs(within(time.Since(starting), hp-slackD, time.Hour, "promotion after the relay's start"),
		within(time.Since(restored), 0, hp+800*time.Millisecond, "promotion after the restore")); err != nil {
		return err
	}
	_, j, err = x.resume(e, "E2")
	if err := refusedWith(j, err, "resume_failed", "resume in a room ended before the restart"); err != nil {
		return err
	}
	c, err := x.dialFrom("Again", addr)
	if err != nil {
		return err
	}
	if _, err := enter(c, createMsg("Again")); err != nil {
		return fmt.Errorf("create from an IP whose rooms were all restored: %w", err)
	}
	return nil
}

// stateWriteWait is how long after a change a persisting relay may take to write it (§14). Not scaled.
const stateWriteWait = 5 * time.Second

// scRestartCrash: a relay that persists writes every change within 5 s, drops a deleted room within 5 s and writes
// a playing room at least every 5 s, so all three survive a crash, the playing room at a position at most 5 s old
// (§14).
func scRestartCrash(x *sc) error {
	cs, err := x.playing(1, 10, 7_200_000)
	if err != nil {
		return err
	}
	p := cs[0]
	clk, err := p.clock()
	if err != nil {
		return err
	}
	playing := p.lastRoom()
	a, _, err := x.create("A")
	if err != nil {
		return err
	}
	_ = a.send(Msg{"type": "lock", "locked": true})
	if _, err := a.waitFor(reply, "locked", isRoom(func(m Msg) bool { return m["locked"] == true })); err != nil {
		return err
	}
	e, _, err := x.create("E")
	if err != nil {
		return err
	}
	_ = e.send(Msg{"type": "end"})
	if _, err := e.waitFor(reply, "closed ended", isType("closed")); err != nil {
		return err
	}
	// The playing room's last change is then over 7 s old: only a periodic write keeps its position within 5 s.
	time.Sleep(stateWriteWait + 2500*time.Millisecond)
	t0 := time.Now()
	x.relay.kill()
	t1 := time.Now()
	if err := x.relay.start(); err != nil {
		return err
	}
	_, j, err := x.resume(a, "A2")
	if err != nil {
		return fmt.Errorf("resume %v after a change, then SIGKILL: %w", stateWriteWait, err)
	}
	if r := obj(j, "room"); r["locked"] != true {
		return fmt.Errorf("room locked %v before SIGKILL restored with locked %v", stateWriteWait, r["locked"])
	}
	_, j, err = x.resume(p, "P2")
	if err != nil {
		return fmt.Errorf("resume of a room playing at SIGKILL: %w", err)
	}
	r := obj(j, "room")
	lo, hi := posAt(playing, clk.early(t0)-slack)-float64(stateWriteWait/time.Millisecond), posAt(playing, clk.late(t1)+slack)
	if got := num(r, "positionMs"); str(r, "phase") != "paused" || got < lo || got > hi {
		return fmt.Errorf("room playing at SIGKILL restored %s at %v; want paused at %.0f–%.0f (at most %v before the kill)",
			str(r, "phase"), r["positionMs"], lo, hi, stateWriteWait)
	}
	_, j, err = x.resume(e, "E2")
	return refusedWith(j, err, "resume_failed", fmt.Sprintf("resume in a room ended %v before SIGKILL", stateWriteWait))
}

// scStateTokens: the state keeps token hashes, never a token. No file of the state directory holds a token the suite
// received in the run, by name or content: at the first write after a create and a join, at the first write after
// a resume, and after a graceful shutdown (§14).
func scStateTokens(x *sc) error {
	dir := x.relay.stateDir()
	before, err := stateFiles(dir)
	if err != nil {
		return err
	}
	a, _, err := x.create("A")
	if err != nil {
		return err
	}
	b, _, err := x.join(a.Code, "B")
	if err != nil {
		return err
	}
	if err := tokensAbsentAfter(dir, before, "after a create and a join"); err != nil {
		return err
	}
	// Let the disconnect's write pass, so the write after the resume is the resume's.
	if before, err = stateFiles(dir); err != nil {
		return err
	}
	b.drop()
	if _, err := a.waitFor(reply, "B disconnected", connectedIs(b.PeerID, false)); err != nil {
		return err
	}
	if err := tokensAbsentAfter(dir, before, "after a disconnect"); err != nil {
		return err
	}
	if before, err = stateFiles(dir); err != nil {
		return err
	}
	b2, _, err := x.resume(b, "B2")
	if err != nil {
		return err
	}
	if err := tokensAbsentAfter(dir, before, "after a resume"); err != nil {
		return err
	}
	if err := x.relay.stop(); err != nil {
		return err
	}
	for _, c := range []*Client{a, b2} {
		if err := closedWith(c, websocket.CloseGoingAway, false); err != nil {
			return fmt.Errorf("on SIGTERM: %w", err)
		}
	}
	if err := tokensAbsentAfter(dir, nil, "after a graceful shutdown"); err != nil {
		return err
	}
	return x.relay.start()
}

// stateFiles reads every regular file under dir, keyed by its path relative to dir.
func stateFiles(dir string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil // a temporary file renamed away meanwhile
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(b)
		return nil
	})
	return files, err
}

// tokensAbsentAfter requires that no file under dir holds a token the suite received. With before set, it watches
// the files until the first write after before was read, at most stateWriteWait and a little; with before nil it
// checks them once.
func tokensAbsentAfter(dir string, before map[string]string, what string) error {
	deadline := time.Now().Add(stateWriteWait + 500*time.Millisecond)
	for {
		files, err := stateFiles(dir)
		if err != nil {
			return err
		}
		for name, text := range files {
			for _, token := range issuedList(issued.tokens) {
				if strings.Contains(name, token) || strings.Contains(text, token) {
					return fmt.Errorf("%s, state file %s holds the token %s: the state keeps only its hash", what, name, token)
				}
			}
		}
		if before == nil || !maps.Equal(files, before) || time.Now().After(deadline) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// readableByOthers lists the files under dir that a user other than the owner can read: the file grants read to
// group or others and every directory from dir down grants them search.
func readableByOthers(dir string) ([]string, error) {
	var bad []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		group, other := fi.Mode().Perm()&0o040 != 0, fi.Mode().Perm()&0o004 != 0
		for p := filepath.Dir(path); ; p = filepath.Dir(p) {
			di, err := os.Stat(p)
			if err != nil {
				return err
			}
			group = group && di.Mode().Perm()&0o010 != 0
			other = other && di.Mode().Perm()&0o001 != 0
			if p == dir || p == filepath.Dir(p) {
				break
			}
		}
		if group || other {
			rel, _ := filepath.Rel(dir, path)
			bad = append(bad, fmt.Sprintf("%s (%v)", rel, fi.Mode().Perm()))
		}
		return nil
	})
	return bad, err
}
