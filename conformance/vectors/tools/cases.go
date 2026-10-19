package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------------------------------------------------
// timeline

type timelineIn struct {
	Room        Room  `json:"room"`
	DurationMs  int64 `json:"durationMs"`
	ServerNowMs int64 `json:"serverNowMs"`
}

type timelineOut struct {
	PositionMs int64 `json:"positionMs"`
	Running    bool  `json:"running"`
}

func timelineTable() Table {
	t := Table{Name: "timeline"}
	add := func(name string, r Room, dur, now int64) {
		t.Cases = append(t.Cases, Case{name, timelineIn{r, dur, now},
			timelineOut{Position(r, dur, now), Running(r, now)}})
	}
	const dur = 3487000
	const at = 918273645
	play := func(pos int64, rate float64) Room { return Room{"playing", pos, at, rate} }

	add("idle holds positionMs", Room{"idle", 0, 1000, 1}, dur, 50000)
	add("paused ignores time", Room{"paused", 61234, 1000, 1}, dur, 900000)
	add("waiting ignores time", Room{"waiting", 61234, 1000, 1}, dur, 900000)
	add("paused at durationMs", Room{"paused", dur, 1000, 1}, dur, 5000)
	add("paused ignores rate", Room{"paused", 61234, 1000, 2}, dur, 900000)
	add("playing at atMs", play(61234, 1), dur, at)
	add("playing 1 ms before atMs is a scheduled start", play(61234, 1), dur, at-1)
	add("playing 1500 ms before atMs is a scheduled start", play(61234, 1), dur, at-1500)
	add("playing 1 ms after atMs", play(61234, 1), dur, at+1)
	add("playing 1000 ms at rate 1", play(61234, 1), dur, at+1000)
	add("rate 1.25 floors", play(61234, 1.25), dur, at+999)
	add("rate 0.5 floors", play(61234, 0.5), dur, at+3)
	add("rate 0.5 one millisecond", play(61234, 0.5), dur, at+1)
	add("rate 2", play(61234, 2), dur, at+1000)
	add("rate 1.1 uses double arithmetic", play(61234, 1.1), dur, at+7)
	add("rate 1.15: the product is a double (114.99999999999999)", play(0, 1.15), dur, at+100)
	add("rate 1.15: the floor applies to the whole sum", play(61234, 1.15), dur, at+20)
	add("from zero at relay start", Room{"playing", 0, 0, 1}, dur, 0)
	add("one hour in", play(0, 1), dur, at+3600000)
	add("cap: 1 ms before durationMs", play(dur-1000, 1), dur, at+999)
	add("cap: exactly durationMs", play(dur-1000, 1), dur, at+1000)
	add("cap: 1 ms past durationMs", play(dur-1000, 1), dur, at+1001)
	add("cap: far past durationMs", play(dur-1000, 1), dur, at+600000)
	add("cap at rate 2", play(dur-1000, 2), dur, at+501)
	add("cap on a short clip", play(0, 1), 5000, at+5001)
	add("positionMs already at durationMs", play(dur, 1), dur, at+10)
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// clock sync

type clockEvent struct {
	Ping       *int64 `json:"ping,omitempty"`
	Pong       *int64 `json:"pong,omitempty"`
	ServerTime *int64 `json:"serverTime,omitempty"`
	AtMs       int64  `json:"atMs"`
}

type clockIn struct {
	Events []clockEvent `json:"events"`
}

type clockAfter struct {
	Result   string `json:"result"`
	OffsetMs *int64 `json:"offsetMs"`
	RttMs    *int64 `json:"rttMs"`
}

type clockOut struct {
	Pongs []clockAfter `json:"pongs"`
}

type clockSeq struct{ events []clockEvent }

func (s *clockSeq) ping(id, at int64) *clockSeq {
	s.events = append(s.events, clockEvent{Ping: new(int64(id)), AtMs: at})
	return s
}

func (s *clockSeq) pong(id, serverTime, at int64) *clockSeq {
	s.events = append(s.events, clockEvent{Pong: new(int64(id)), ServerTime: new(int64(serverTime)), AtMs: at})
	return s
}

// exchange is a ping sent at sentAt and answered rtt later with a serverTime whose sample offset is offset.
func (s *clockSeq) exchange(id, sentAt, rtt, offset int64) *clockSeq {
	return s.ping(id, sentAt).pong(id, offset+sentAt+rtt/2, sentAt+rtt)
}

func runClock(events []clockEvent) clockOut {
	c := NewClock()
	out := clockOut{Pongs: []clockAfter{}}
	for _, e := range events {
		if e.Ping != nil {
			c.Ping(*e.Ping, e.AtMs)
			continue
		}
		r := c.Pong(*e.Pong, *e.ServerTime, e.AtMs)
		after := clockAfter{Result: r}
		if best, ok := c.Best(); ok {
			after.OffsetMs, after.RttMs = new(int64(best.offset)), new(int64(best.rtt))
		}
		out.Pongs = append(out.Pongs, after)
	}
	return out
}

func clockTable() Table {
	t := Table{Name: "clock-sync"}
	add := func(name string, s *clockSeq) {
		t.Cases = append(t.Cases, Case{name, clockIn{s.events}, runClock(s.events)})
	}
	seq := func() *clockSeq { return &clockSeq{} }

	add("first sample", seq().exchange(1, 1000, 100, 48950))
	add("relay clock behind the local one", seq().exchange(1, 900000, 40, -899000))
	add("odd round trip halves down", seq().ping(1, 1000).pong(1, 6050, 1101))
	add("zero round trip", seq().exchange(1, 1000, 0, 5000))
	add("burst: the lowest round trip wins",
		seq().exchange(1, 0, 120, 5000).exchange(2, 500, 80, 5030).exchange(3, 1000, 95, 4990))
	add("tie: the oldest sample wins",
		seq().exchange(1, 0, 80, 5000).exchange(2, 500, 80, 5010))
	add("round trip of 1000 ms is kept", seq().exchange(1, 0, 1000, 5000))
	add("round trip of 1001 ms is dropped", seq().exchange(1, 0, 1001, 5000))
	add("dropped sample leaves the best alone",
		seq().exchange(1, 0, 100, 5000).exchange(2, 500, 1001, 5000))
	add("dropped sample never counts as a clock step",
		seq().exchange(1, 0, 100, 5000).exchange(2, 500, 1500, 90000))
	add("unknown ping id is ignored", seq().exchange(1, 0, 100, 5000).pong(7, 9999, 600))
	add("second pong for one ping is ignored",
		seq().exchange(1, 0, 100, 5000).pong(1, 5100, 700))
	add("negative round trip is ignored", seq().ping(1, 1000).pong(1, 5000, 999))
	add("pongs out of order",
		seq().ping(1, 0).ping(2, 500).pong(2, 5560, 620).pong(1, 5300, 640))
	add("burst then interval",
		seq().exchange(1, 0, 60, 2000).exchange(2, 500, 55, 2002).exchange(3, 1000, 70, 1998).
			exchange(4, 5000, 50, 2004).exchange(5, 10000, 65, 2001))
	window := seq().exchange(1, 0, 30, 7000)
	for i := int64(2); i <= 9; i++ {
		window.exchange(i, 5000*(i-1), 40+i, 7000+i)
	}
	add("window keeps 8 samples: the 9th evicts the best", window)
	add("step: difference equal to the tolerance is kept",
		seq().exchange(1, 0, 100, 1000).exchange(2, 500, 100, 1200))
	add("step: 1 ms over the tolerance clears the window",
		seq().exchange(1, 0, 100, 1000).exchange(2, 500, 100, 1201))
	add("step: negative jump over the tolerance clears the window",
		seq().exchange(1, 0, 100, 1000).exchange(2, 500, 100, 799))
	add("step: odd round-trip sum halves down (kept)",
		seq().exchange(1, 0, 100, 1000).exchange(2, 500, 101, 1200))
	add("step: odd round-trip sum halves down (cleared)",
		seq().exchange(1, 0, 100, 1000).exchange(2, 500, 101, 1201))
	add("step: compared with the best, not the newest",
		seq().exchange(1, 0, 20, 1000).exchange(2, 500, 300, 1250).exchange(3, 1000, 20, 1160))
	add("step: the window starts over from the stepped sample",
		seq().exchange(1, 0, 20, 1000).exchange(2, 500, 30, 1010).exchange(3, 1000, 200, 40000).
			exchange(4, 5000, 250, 40100))
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// reconciliation

func reconcileTable() Table {
	t := Table{Name: "reconcile"}
	const dur = 3487000
	const at = 1000000
	base := func() ReconcileIn {
		return ReconcileIn{
			Room:        Room{"playing", 60000, at, 1},
			DurationMs:  dur,
			ServerNowMs: at + 10000,
			Player:      Player{PositionMs: 70000, DurationMs: new(int64(dur)), CanSetRate: true, Rate: 1},
		}
	}
	add := func(name string, change func(*ReconcileIn)) {
		in := base()
		change(&in)
		t.Cases = append(t.Cases, Case{name, in, Reconcile(in)})
	}
	paused := func(in *ReconcileIn) {
		in.Room.Phase = "paused"
		in.Player.PositionMs = 60000
		in.Player.Paused = true
	}

	add("idle: nothing to do", func(in *ReconcileIn) { in.Room = Room{"idle", 0, at, 1}; in.Player.PositionMs = 5000 })

	// Held: paused, waiting.
	add("paused: player paused in place", paused)
	add("paused: player playing gets paused", func(in *ReconcileIn) { paused(in); in.Player.Paused = false })
	add("paused: 500 ms off holds", func(in *ReconcileIn) { paused(in); in.Player.PositionMs = 60500 })
	add("paused: 501 ms off seeks", func(in *ReconcileIn) { paused(in); in.Player.PositionMs = 60501 })
	add("paused: 501 ms behind seeks", func(in *ReconcileIn) { paused(in); in.Player.PositionMs = 59499 })
	add("paused: seek waits for the cooldown (1999 ms)", func(in *ReconcileIn) {
		paused(in)
		in.Player.PositionMs = 65000
		in.SinceSeekMs = new(int64(1999))
	})
	add("paused: seek after the cooldown (2000 ms)", func(in *ReconcileIn) {
		paused(in)
		in.Player.PositionMs = 65000
		in.SinceSeekMs = new(int64(2000))
	})
	add("waiting: held like paused", func(in *ReconcileIn) {
		in.Room.Phase = "waiting"
		in.Player.PositionMs = 61000
	})
	add("paused: positive offset", func(in *ReconcileIn) { paused(in); in.OffsetMs = 2000 })
	add("paused: negative offset", func(in *ReconcileIn) { paused(in); in.OffsetMs = -2000; in.Player.PositionMs = 58000 })
	add("paused: local position below 0 holds at 0", func(in *ReconcileIn) {
		paused(in)
		in.Room.PositionMs = 1000
		in.OffsetMs = -5000
		in.Player.PositionMs = 0
	})
	add("paused: ends a nudge", func(in *ReconcileIn) { paused(in); in.Nudging = true; in.Player.Rate = 0.96 })
	add("paused: applies the room rate", func(in *ReconcileIn) { paused(in); in.Room.Rate = 1.25 })
	add("paused: seek-only player ignores the room rate", func(in *ReconcileIn) {
		paused(in)
		in.Room.Rate = 1.25
		in.Player.CanSetRate = false
	})
	add("paused: past the local file's end holds at end - 1000", func(in *ReconcileIn) {
		paused(in)
		in.Room.PositionMs = dur
		in.Player.DurationMs = new(int64(dur - 7000))
		in.Player.PositionMs = dur - 8000
	})
	add("paused: seek clamps to the local file's end - 1000", func(in *ReconcileIn) {
		paused(in)
		in.Room.PositionMs = dur
		in.Player.DurationMs = new(int64(dur - 7000))
		in.Player.PositionMs = 3000000
	})
	add("paused: unknown local duration doesn't clamp", func(in *ReconcileIn) {
		paused(in)
		in.Room.PositionMs = dur
		in.Player.DurationMs = nil
		in.Player.PositionMs = 3000000
	})

	// Scheduled starts and negative local positions.
	add("scheduled start 1 ms ahead", func(in *ReconcileIn) {
		in.ServerNowMs = at - 1
		in.Player.PositionMs = 60000
		in.Player.Paused = true
	})
	add("scheduled start 1500 ms ahead, player elsewhere and playing", func(in *ReconcileIn) {
		in.ServerNowMs = at - 1500
		in.Player.PositionMs = 65000
	})
	add("scheduled start at atMs plays", func(in *ReconcileIn) {
		in.ServerNowMs = at
		in.Player.PositionMs = 60000
		in.Player.Paused = true
	})
	add("scheduled start sets the room rate", func(in *ReconcileIn) {
		in.ServerNowMs = at - 800
		in.Room.Rate = 1.5
		in.Player.PositionMs = 60000
		in.Player.Paused = true
	})
	add("negative offset delays the local start", func(in *ReconcileIn) {
		in.Room.PositionMs = 0
		in.OffsetMs = -5000
		in.ServerNowMs = at - 100
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})
	add("negative offset holds while the room runs", func(in *ReconcileIn) {
		in.Room.PositionMs = 0
		in.OffsetMs = -5000
		in.ServerNowMs = at + 3000
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})
	add("negative offset at rate 1.5 rounds the wait up", func(in *ReconcileIn) {
		in.Room.PositionMs = 0
		in.Room.Rate = 1.5
		in.Player.Rate = 1.5
		in.OffsetMs = -1000
		in.ServerNowMs = at
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})
	add("negative offset at rate 2 halves the wait", func(in *ReconcileIn) {
		in.Room.PositionMs = 0
		in.Room.Rate = 2
		in.OffsetMs = -5000
		in.ServerNowMs = at
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})
	add("negative offset reached: play from 0", func(in *ReconcileIn) {
		in.Room.PositionMs = 0
		in.OffsetMs = -5000
		in.ServerNowMs = at + 5000
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})

	// Playing.
	add("playing: in sync", func(in *ReconcileIn) {})
	add("playing: paused player plays", func(in *ReconcileIn) { in.Player.Paused = true })
	add("playing: 350 ms ahead stays", func(in *ReconcileIn) { in.Player.PositionMs = 70350 })
	add("playing: 350 ms behind stays", func(in *ReconcileIn) { in.Player.PositionMs = 69650 })
	add("playing: 351 ms ahead slows down", func(in *ReconcileIn) { in.Player.PositionMs = 70351 })
	add("playing: 351 ms behind speeds up", func(in *ReconcileIn) { in.Player.PositionMs = 69649 })
	add("nudging: 150 ms ahead keeps nudging", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 0.96
		in.Player.PositionMs = 70150
	})
	add("nudging: 149 ms ahead ends the nudge", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 0.96
		in.Player.PositionMs = 70149
	})
	add("nudging: 149 ms behind ends the nudge", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 1.04
		in.Player.PositionMs = 69851
	})
	add("nudging: overshoot flips the direction", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 0.96
		in.Player.PositionMs = 69800
	})
	add("nudging: rate already applied", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 1.04
		in.Player.PositionMs = 69000
	})
	add("playing: 2000 ms ahead nudges", func(in *ReconcileIn) { in.Player.PositionMs = 72000 })
	add("playing: 2000 ms behind nudges", func(in *ReconcileIn) { in.Player.PositionMs = 68000 })
	add("playing: 2001 ms ahead seeks", func(in *ReconcileIn) { in.Player.PositionMs = 72001 })
	add("playing: 2001 ms behind seeks", func(in *ReconcileIn) { in.Player.PositionMs = 67999 })
	add("playing: seek ends a nudge", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 0.96
		in.Player.PositionMs = 72500
	})
	add("playing: seek waits for the cooldown (1999 ms)", func(in *ReconcileIn) {
		in.Player.PositionMs = 75000
		in.SinceSeekMs = new(int64(1999))
	})
	add("playing: cooldown still ends a nudge", func(in *ReconcileIn) {
		in.Nudging = true
		in.Player.Rate = 1.04
		in.Player.PositionMs = 65000
		in.SinceSeekMs = new(int64(500))
	})
	add("playing: seek after the cooldown (2000 ms)", func(in *ReconcileIn) {
		in.Player.PositionMs = 75000
		in.SinceSeekMs = new(int64(2000))
	})
	add("playing: paused and far behind seeks, then plays", func(in *ReconcileIn) {
		in.Player.PositionMs = 0
		in.Player.Paused = true
	})
	add("playing: nudge at room rate 1.25 (slow)", func(in *ReconcileIn) {
		in.Room.Rate = 1.25
		in.Player.Rate = 1.25
		in.ServerNowMs = at + 8000
		in.Player.PositionMs = 70400
	})
	add("playing: nudge at room rate 1.25 (fast)", func(in *ReconcileIn) {
		in.Room.Rate = 1.25
		in.Player.Rate = 1.25
		in.ServerNowMs = at + 8000
		in.Player.PositionMs = 69600
	})
	add("playing: applies the room rate", func(in *ReconcileIn) {
		in.Room.Rate = 1.5
		in.ServerNowMs = at + 6000
		in.Player.PositionMs = 69000
	})
	add("playing: rate within 0.001 is left alone", func(in *ReconcileIn) { in.Player.Rate = 1.0009 })
	add("playing: positive offset", func(in *ReconcileIn) { in.OffsetMs = 3000; in.Player.PositionMs = 73000 })
	add("playing: negative offset", func(in *ReconcileIn) { in.OffsetMs = -3000; in.Player.PositionMs = 67000 })
	add("playing: offset change seeks", func(in *ReconcileIn) { in.OffsetMs = -3000 })
	add("buffering: no correction", func(in *ReconcileIn) { in.Player.Buffering = true; in.Player.PositionMs = 65000 })
	add("buffering: a nudge stays", func(in *ReconcileIn) {
		in.Player.Buffering = true
		in.Nudging = true
		in.Player.Rate = 1.04
		in.Player.PositionMs = 69000
	})

	// Seek-only players.
	seekOnly := func(in *ReconcileIn) { in.Player.CanSetRate = false }
	add("seek-only: 1000 ms ahead stays", func(in *ReconcileIn) { seekOnly(in); in.Player.PositionMs = 71000 })
	add("seek-only: 1000 ms behind stays", func(in *ReconcileIn) { seekOnly(in); in.Player.PositionMs = 69000 })
	add("seek-only: 1001 ms ahead seeks", func(in *ReconcileIn) { seekOnly(in); in.Player.PositionMs = 71001 })
	add("seek-only: 1001 ms behind seeks", func(in *ReconcileIn) { seekOnly(in); in.Player.PositionMs = 68999 })
	add("seek-only: cooldown", func(in *ReconcileIn) {
		seekOnly(in)
		in.Player.PositionMs = 68000
		in.SinceSeekMs = new(int64(1500))
	})
	add("seek-only: never nudges or sets the rate", func(in *ReconcileIn) {
		seekOnly(in)
		in.Nudging = true
		in.Room.Rate = 1.25
		in.ServerNowMs = at + 8000
		in.Player.PositionMs = 70600
	})
	add("seek-only: paused player plays", func(in *ReconcileIn) { seekOnly(in); in.Player.Paused = true })

	// Ends.
	add("cap: target stops at durationMs", func(in *ReconcileIn) {
		in.Room.PositionMs = dur - 500
		in.Player.DurationMs = new(int64(dur + 5000))
		in.Player.PositionMs = dur
	})
	add("end: both past the local file's end - 1000", func(in *ReconcileIn) {
		in.Room.PositionMs = dur - 12000
		in.Player.DurationMs = new(int64(dur - 5000))
		in.Player.PositionMs = dur - 5500
		in.Player.Paused = true
	})
	add("end: exactly at end - 1000", func(in *ReconcileIn) {
		in.Room.PositionMs = dur - 16000
		in.Player.DurationMs = new(int64(dur - 5000))
		in.Player.PositionMs = dur - 6000
	})
	add("end: local file shorter, player behind", func(in *ReconcileIn) {
		in.Room.PositionMs = dur - 12000
		in.Player.DurationMs = new(int64(dur - 5000))
		in.Player.PositionMs = 3000000
	})
	add("end: unknown local duration seeks to the target", func(in *ReconcileIn) {
		in.Room.PositionMs = dur - 12000
		in.Player.DurationMs = nil
		in.Player.PositionMs = 3000000
	})

	// Seek accuracy: every seek threshold is at least the player's seek accuracy.
	const cantSeek = 9007199254740991
	accuracy := func(in *ReconcileIn, ms int64) { in.Player.SeekAccuracyMs = ms }
	add("seek accuracy: paused, 3000 ms off holds with accuracy 3000", func(in *ReconcileIn) {
		paused(in)
		accuracy(in, 3000)
		in.Player.PositionMs = 63000
	})
	add("seek accuracy: paused, 3001 ms off seeks with accuracy 3000", func(in *ReconcileIn) {
		paused(in)
		accuracy(in, 3000)
		in.Player.PositionMs = 63001
	})
	add("seek accuracy: 499 leaves the held threshold at 500", func(in *ReconcileIn) {
		paused(in)
		accuracy(in, 499)
		in.Player.PositionMs = 60501
	})
	add("seek accuracy: playing, 5000 ms ahead nudges with accuracy 5000", func(in *ReconcileIn) {
		accuracy(in, 5000)
		in.Player.PositionMs = 75000
	})
	add("seek accuracy: playing, 5001 ms ahead seeks with accuracy 5000", func(in *ReconcileIn) {
		accuracy(in, 5000)
		in.Player.PositionMs = 75001
	})
	add("seek accuracy: 2000 leaves the seek threshold at 2000", func(in *ReconcileIn) {
		accuracy(in, 2000)
		in.Player.PositionMs = 67999
	})
	add("seek accuracy: seek-only, 4000 ms behind stays with accuracy 4000", func(in *ReconcileIn) {
		seekOnly(in)
		accuracy(in, 4000)
		in.Player.PositionMs = 66000
	})
	add("seek accuracy: seek-only, 4001 ms behind seeks with accuracy 4000", func(in *ReconcileIn) {
		seekOnly(in)
		accuracy(in, 4000)
		in.Player.PositionMs = 65999
	})
	add("seek accuracy: a player that can't seek follows by rate only", func(in *ReconcileIn) {
		accuracy(in, cantSeek)
		in.Player.PositionMs = 10000
	})
	add("seek accuracy: a player that can't seek or set rates is left alone", func(in *ReconcileIn) {
		seekOnly(in)
		accuracy(in, cantSeek)
		in.Player.PositionMs = 10000
	})
	add("seek accuracy: held, a player that can't seek only pauses", func(in *ReconcileIn) {
		paused(in)
		accuracy(in, cantSeek)
		in.Player.Paused = false
		in.Player.PositionMs = 10000
	})
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// matching and same title

func matchingTable() Table {
	t := Table{Name: "matching"}
	const dur = 10200000
	movie := func() Media {
		return Media{
			Kind: "movie", Title: "Heat", Year: new(1995), Ids: map[string]string{"imdb": "tt0113277", "tmdb": "949"},
			DurationMs: dur,
			File:       &File{SizeBytes: new(int64(4700000000)), Oshash: "8e245d9679d31e12", Name: "Heat.1995.1080p.BluRay.mkv"},
		}
	}
	add := func(name string, m Media, cs ...Candidate) {
		receivable("matching", name, m)
		if cs == nil {
			cs = []Candidate{}
		}
		in := MatchIn{m, cs}
		t.Cases = append(t.Cases, Case{name, in, Match(in)})
	}
	known := func(id string, d int64) Candidate { return Candidate{ID: id, DurationMs: new(int64(d))} }
	probe := func(id string, d *int64, takes int64) Candidate {
		return Candidate{ID: id, Probe: &Probe{DurationMs: d, TakesMs: takes}}
	}

	add("no candidates", movie())
	add("one candidate, same duration", movie(), known("a", dur))
	add("+1500 ms matches", movie(), known("a", dur+1500))
	add("-1500 ms matches", movie(), known("a", dur-1500))
	add("+1501 ms asks the user", movie(), known("a", dur+1501))
	add("-1501 ms asks the user", movie(), known("a", dur-1501))
	add("no file hints in the descriptor", func() Media { m := movie(); m.File = nil; return m }(),
		known("a", dur+900), known("b", dur-200))
	add("oshash first", movie(),
		known("plain", dur),
		Candidate{ID: "hash", DurationMs: new(int64(dur + 1400)), Oshash: "8e245d9679d31e12"})
	add("oshash compares case-insensitively", movie(),
		known("plain", dur),
		Candidate{ID: "hash", DurationMs: new(int64(dur + 1400)), Oshash: "8E245D9679D31E12"})
	add("size before name", movie(),
		Candidate{ID: "name", DurationMs: new(int64(dur)), Name: "Heat.1995.1080p.BluRay.mkv"},
		Candidate{ID: "size", DurationMs: new(int64(dur + 1000)), SizeBytes: new(int64(4700000000))})
	add("name before duration, case-insensitive", movie(),
		known("plain", dur),
		Candidate{ID: "name", DurationMs: new(int64(dur - 1200)), Name: "heat.1995.1080P.bluray.MKV"})
	add("hash and size beat hash alone", movie(),
		Candidate{ID: "hash", DurationMs: new(int64(dur + 100)), Oshash: "8e245d9679d31e12"},
		Candidate{ID: "hash+size", DurationMs: new(int64(dur + 300)), Oshash: "8e245d9679d31e12", SizeBytes: new(int64(4700000000))})
	add("smallest duration difference first", movie(),
		known("plus900", dur+900), known("minus300", dur-300), known("exact", dur))
	add("equal keys keep the resolver's order", movie(),
		known("first", dur+500), known("second", dur-500), known("third", dur+500))
	add("a ranked candidate inside the tolerance wins over a closer one", movie(),
		known("closer", dur),
		Candidate{ID: "size", DurationMs: new(int64(dur + 1400)), SizeBytes: new(int64(4700000000))})
	add("known durations before probes", movie(),
		probe("probe", new(int64(dur)), 3000), known("known", dur+1400))
	add("a probe that ranks first opens first", movie(),
		known("known", dur),
		Candidate{ID: "hash", Oshash: "8e245d9679d31e12", Probe: &Probe{DurationMs: new(int64(dur + 20)), TakesMs: 4000}})
	add("failed probe is skipped", movie(),
		probe("broken", nil, 5000), probe("good", new(int64(dur-700)), 5000))
	add("all probes fail", movie(),
		probe("a", nil, 1000), probe("b", nil, 1000))
	add("a candidate without duration or probe is skipped", movie(),
		Candidate{ID: "nothing"}, known("b", dur+100))
	add("4 probes at most", movie(),
		probe("p1", new(int64(dur+5000)), 1000), probe("p2", new(int64(dur-4000)), 1000), probe("p3", new(int64(dur+3000)), 1000),
		probe("p4", new(int64(dur+2500)), 1000), probe("p5", new(int64(dur)), 1000))
	add("known duration still checked after the probe limit", movie(),
		Candidate{ID: "p1", Name: "Heat.1995.1080p.BluRay.mkv", Probe: &Probe{TakesMs: 1000}},
		Candidate{ID: "p2", Name: "Heat.1995.1080p.BluRay.mkv", Probe: &Probe{TakesMs: 1000}},
		Candidate{ID: "p3", Name: "Heat.1995.1080p.BluRay.mkv", Probe: &Probe{TakesMs: 1000}},
		Candidate{ID: "p4", Name: "Heat.1995.1080p.BluRay.mkv", Probe: &Probe{TakesMs: 1000}},
		Candidate{ID: "p5", Name: "Heat.1995.1080p.BluRay.mkv", Probe: &Probe{DurationMs: new(int64(dur)), TakesMs: 1000}},
		known("known", dur+300))
	add("probe budget: exactly 60 s is allowed", movie(),
		probe("a", new(int64(dur+9000)), 25000), probe("b", new(int64(dur+8000)), 25000), probe("c", new(int64(dur+1000)), 10000))
	add("probe budget: 1 ms over stops probing", movie(),
		probe("a", new(int64(dur+4000)), 30000), probe("b", new(int64(dur)), 30001), probe("c", new(int64(dur)), 1))
	add("closest tie keeps the ranked order", movie(),
		known("plus", dur+2000), known("minus", dur-2000))
	add("closest across probes and known durations", movie(),
		probe("probe", new(int64(dur-1600)), 1000), known("known", dur+1800))
	add("a candidate duration of 604800000 counts", movie(), known("max", 604800000))
	add("a candidate duration of 604800001 is unknown", movie(), known("over", 604800001), known("b", dur+3000))
	add("a candidate duration of 0 is unknown, so its probe runs", movie(),
		Candidate{ID: "zero", DurationMs: new(int64(0)), Probe: &Probe{DurationMs: new(int64(dur)), TakesMs: 1000}})
	add("a probe answer of 604800001 is a failed probe", movie(),
		probe("over", new(int64(604800001)), 1000), probe("good", new(int64(dur-500)), 1000))
	add("a negative probe answer is a failed probe", movie(), probe("negative", new(int64(-5)), 1000))

	video := Media{
		Kind: "video", Title: "Wedding", DurationMs: 5400000,
		File: &File{SizeBytes: new(int64(1200000000)), Oshash: "00ff00ff00ff00ff", Name: "wedding.mp4"},
	}
	add("video: every candidate is kept, the same file first", video,
		Candidate{ID: "size-only", DurationMs: new(int64(5400000)), SizeBytes: new(int64(1200000000)), Name: "other.mp4"},
		Candidate{ID: "size+name", DurationMs: new(int64(5400000)), SizeBytes: new(int64(1200000000)), Name: "Wedding.MP4"},
		Candidate{ID: "hash", DurationMs: new(int64(5400500)), Oshash: "00FF00FF00FF00FF"},
		Candidate{ID: "nothing", DurationMs: new(int64(5400000))})
	add("video: a candidate that isn't the same file matches by duration", video,
		Candidate{ID: "name-only", DurationMs: new(int64(5400000)), Name: "wedding.mp4"})
	add("video: non-matching hints keep and rank every candidate", video,
		Candidate{ID: "far", DurationMs: new(int64(5410000)), Oshash: "1111111111111111", Name: "a.mp4"},
		Candidate{ID: "near", DurationMs: new(int64(5401000)), SizeBytes: new(int64(5)), Name: "b.mp4"},
		Candidate{ID: "probe", Name: "c.mp4", Probe: &Probe{DurationMs: new(int64(5400000)), TakesMs: 1000}})
	videoNoFile := video
	videoNoFile.File = nil
	add("video: without file hints durations decide", videoNoFile,
		Candidate{ID: "a", DurationMs: new(int64(5402000)), SizeBytes: new(int64(1200000000)), Name: "wedding.mp4"},
		Candidate{ID: "b", DurationMs: new(int64(5400100))})
	add("video: without file hints and no duration within 1500 ms asks the user", videoNoFile,
		Candidate{ID: "a", DurationMs: new(int64(5300000))}, known("b", 5600000))
	add("video: same file but another duration asks the user", video,
		Candidate{ID: "hash", DurationMs: new(int64(5300000)), Oshash: "00ff00ff00ff00ff"})

	episode := Media{
		Kind: "episode", Title: "Pilot", Ids: map[string]string{"imdb": "tt0959621", "tvdb": "349232"},
		Series:     &Series{Title: "Breaking Bad", Year: new(2008), Ids: map[string]string{"imdb": "tt0903747", "tmdb": "1396", "tvdb": "81189"}},
		Season:     new(1),
		Episode:    new(1),
		DurationMs: 3487000,
	}
	add("episode: durations decide", episode, known("a", 3492000), known("b", 3486000))
	return t
}

type sameTitleIn struct {
	A Media `json:"a"`
	B Media `json:"b"`
}

type sameTitleOut struct {
	Same bool `json:"same"`
}

func sameTitleTable() Table {
	t := Table{Name: "same-title"}
	add := func(name string, a, b Media) {
		receivable("same-title", name, a)
		receivable("same-title", name, b)
		t.Cases = append(t.Cases, Case{name, sameTitleIn{a, b}, sameTitleOut{SameTitle(a, b)}})
	}
	movie := func(ids map[string]string) Media {
		return Media{Kind: "movie", Title: "Heat", Year: new(1995), Ids: ids, DurationMs: 10200000}
	}
	// ep always carries series, as an episode must; nil seriesIds leaves out series.ids.
	ep := func(ids, seriesIds map[string]string, season, episode *int) Media {
		return Media{Kind: "episode", Title: "Pilot", Ids: ids, Series: &Series{Title: "Breaking Bad", Ids: seriesIds},
			Season: season, Episode: episode, DurationMs: 3487000}
	}
	video := func(f *File) Media { return Media{Kind: "video", Title: "Wedding", DurationMs: 5400000, File: f} }
	ids := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	add("movie: shared imdb", movie(ids("imdb", "tt0113277")), movie(ids("imdb", "tt0113277", "tmdb", "949")))
	add("movie: shared tmdb only", movie(ids("tmdb", "949")), movie(ids("imdb", "tt0113277", "tmdb", "949")))
	add("movie: shared plex guid", movie(ids("plex", "plex://movie/5d776825880197001ec967c9")),
		movie(ids("plex", "plex://movie/5d776825880197001ec967c9")))
	add("movie: one shared ID is enough", movie(ids("imdb", "tt0113277", "tmdb", "949")),
		movie(ids("imdb", "tt0113277", "tmdb", "950")))
	add("movie: no shared ID", movie(ids("imdb", "tt0113277")), movie(ids("imdb", "tt0113278", "tmdb", "949")))
	add("movie: same value in another namespace", movie(ids("tmdb", "949")), movie(ids("tvdb", "949")))
	add("movie: unknown namespace doesn't count", movie(ids("trakt", "1234")), movie(ids("trakt", "1234")))
	add("movie: IDs on one side only", movie(ids("imdb", "tt0113277")), movie(nil))
	add("movie: no IDs", movie(nil), movie(nil))
	add("movie: IDs compare exactly", movie(ids("imdb", "tt0113277")), movie(ids("imdb", "TT0113277")))
	add("movie vs episode", movie(ids("imdb", "tt0959621")), ep(ids("imdb", "tt0959621"), nil, new(1), new(1)))

	bb := ids("imdb", "tt0903747", "tvdb", "81189")
	add("episode: shared episode ID", ep(ids("imdb", "tt0959621"), nil, nil, nil),
		ep(ids("imdb", "tt0959621", "tvdb", "349232"), bb, new(1), new(1)))
	add("episode: shared episode ID beats different numbers", ep(ids("tvdb", "349232"), bb, new(1), new(1)),
		ep(ids("tvdb", "349232"), bb, new(0), new(7)))
	add("episode: shared series ID and numbers", ep(nil, ids("tvdb", "81189"), new(1), new(1)),
		ep(nil, ids("tmdb", "1396", "tvdb", "81189"), new(1), new(1)))
	add("episode: shared series ID, other episode", ep(nil, bb, new(1), new(1)), ep(nil, bb, new(1), new(2)))
	add("episode: shared series ID, other season", ep(nil, bb, new(1), new(1)), ep(nil, bb, new(2), new(1)))
	add("episode: shared series ID, one without season", ep(nil, bb, nil, new(1)), ep(nil, bb, new(1), new(1)))
	add("episode: series IDs in different namespaces", ep(nil, ids("imdb", "tt0903747"), new(1), new(1)),
		ep(nil, ids("tmdb", "1396"), new(1), new(1)))
	add("episode: one without series IDs", ep(nil, nil, new(1), new(1)), ep(nil, bb, new(1), new(1)))
	add("episode: anilist series", ep(nil, ids("anilist", "21"), new(1), new(1000)),
		ep(nil, ids("anilist", "21", "mal", "21"), new(1), new(1000)))
	add("episode: different episode IDs beat equal numbers", ep(ids("imdb", "tt0959621"), bb, new(1), new(1)),
		ep(ids("imdb", "tt1054724"), bb, new(1), new(1)))
	add("episode: episode ID on one side only, equal numbers", ep(ids("imdb", "tt0959621"), bb, new(1), new(1)),
		ep(nil, bb, new(1), new(1)))
	add("episode: episode IDs in different namespaces, equal numbers", ep(ids("imdb", "tt0959621"), bb, new(1), new(1)),
		ep(ids("tvdb", "349232"), bb, new(1), new(1)))
	add("episode: different unknown-namespace IDs don't count", ep(ids("trakt", "62085"), bb, new(1), new(1)),
		ep(ids("trakt", "62086"), bb, new(1), new(1)))

	add("video: same oshash", video(&File{Oshash: "00ff00ff00ff00ff"}),
		video(&File{Oshash: "00ff00ff00ff00ff", Name: "x.mp4"}))
	add("video: same size and name, name case-insensitive",
		video(&File{SizeBytes: new(int64(1200000000)), Name: "wedding.mp4"}),
		video(&File{SizeBytes: new(int64(1200000000)), Name: "Wedding.MP4"}))
	add("video: same size, other name", video(&File{SizeBytes: new(int64(1200000000)), Name: "wedding.mp4"}),
		video(&File{SizeBytes: new(int64(1200000000)), Name: "wedding (1).mp4"}))
	add("video: same name, one size missing", video(&File{Name: "wedding.mp4"}),
		video(&File{SizeBytes: new(int64(1200000000)), Name: "wedding.mp4"}))
	add("video: different oshash, same size and name", video(&File{Oshash: "1111111111111111",
		SizeBytes: new(int64(5)), Name: "a.mp4"}), video(&File{Oshash: "2222222222222222", SizeBytes: new(int64(5)), Name: "a.mp4"}))
	add("video: one without file", video(nil), video(&File{Oshash: "00ff00ff00ff00ff"}))
	add("video: IDs don't count", Media{Kind: "video", Title: "a", Ids: ids("imdb", "tt1"), DurationMs: 1},
		Media{Kind: "video", Title: "a", Ids: ids("imdb", "tt1"), DurationMs: 1})
	return t
}

// receivable panics unless m passes the receive checks of spec/SDK.md §9.7: a vector never feeds an SDK a descriptor
// it would refuse on receipt.
func receivable(table, name string, m Media) {
	raw, err := json.Marshal(m)
	if err != nil || !CheckMedia(raw, false) {
		panic(fmt.Sprintf("%s %q: the descriptor fails the receive checks: %s", table, name, raw))
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// code routing

type routingIn struct {
	Code   string       `json:"code"`
	Relays []RouteRelay `json:"relays"`
}

type routingOut struct {
	Code  string  `json:"code"`
	Relay *string `json:"relay"`
}

type errorOut struct {
	Error string `json:"error"`
}

func codeRoutingTable() Table {
	t := Table{Name: "code-routing"}
	// Three relays that leave QRSTUVWXYZ to no relay, as a list may (SPEC.md §5.2).
	three := []RouteRelay{{"a", "2345678"}, {"b", "9ABCDEF"}, {"c", "GHJKMNP"}}
	addWith := func(name, code string, relays []RouteRelay) {
		var expect any
		norm, err := NormalizeCode(code)
		if err != nil {
			expect = errorOut{err.Error()}
		} else {
			out := routingOut{Code: norm}
			if id := Route(norm, relays); id != "" {
				out.Relay = new(id)
			}
			expect = out
		}
		t.Cases = append(t.Cases, Case{name, routingIn{code, relays}, expect})
	}
	add := func(name, code string) { addWith(name, code, three) }

	add("upper case", "K7MQ2X")
	add("lower case", "k7mq2x")
	add("mixed case", "k7Mq2X")
	add("grouped with a hyphen", "K7M-Q2X")
	add("grouped with a space", "k7m q2x")
	add("surrounding whitespace", "  K7MQ2X \n")
	add("tab and carriage return", "\tK7M\rQ2X")
	add("no-break space", "K7M\u00a0Q2X")
	add("narrow no-break and figure spaces", "K7\u202fMQ\u20072X")
	add("en dash", "K7M\u2013Q2X")
	add("em dash", "K7M\u2014Q2X")
	add("minus sign", "K7M\u2212Q2X")
	add("non-breaking hyphen", "K7M\u2011Q2X")
	add("several separators", "- K7 -- MQ - 2X -")
	add("first symbol 2 routes to a", "2ABCDE")
	add("first symbol 8 routes to a", "823456")
	add("first symbol 9 routes to b", "923456")
	add("first symbol F routes to b", "F23456")
	add("first symbol G routes to c", "G23456")
	add("first symbol P routes to c", "PQRSTU")
	add("first symbol Q: no relay owns it", "Q23456")
	add("first symbol Z: no relay owns it", "ZZZZZZ")
	add("U V W X Y are symbols", "UVWXY2")
	add("five symbols", "K7MQ2")
	add("seven symbols", "K7MQ2XA")
	add("empty", "")
	add("only separators", " - - ")
	add("zero is not a symbol", "K7MQ20")
	add("letter O is not a symbol", "K7MQ2O")
	add("one is not a symbol", "K7MQ21")
	add("letter I is not a symbol", "K7MQ2I")
	add("lower-case l is not a symbol", "k7mq2l")
	add("underscore is not stripped", "K7M_Q2X")
	add("dot is not stripped", "K7M.Q2X")
	add("non-ASCII letter", "K7MQ2\u00c9")
	add("full-width letter", "\uff2b7MQ2X")
	addWith("self-hosted relay owns every symbol", "Q2X7MK", []RouteRelay{{"local", Alphabet}})
	addWith("a symbol no relay owned, now owned by a new relay", "Q2X7MK",
		[]RouteRelay{{"a", "2345678"}, {"b", "9ABCDEF"}, {"c", "GHJKMNP"}, {"d", "QRSTUVWXYZ"}})
	addWith("ownership after a move", "G2X7MK",
		[]RouteRelay{{"a", "2345678"}, {"b", "9ABCDEF"}, {"c", "HJKMNP"}, {"d", "G"}})
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// relay list

type listIn struct {
	List json.RawMessage `json:"list"`
}

type listOut struct {
	Relays []ListedRelay `json:"relays"`
}

func relayListTable() Table {
	t := Table{Name: "relay-list"}
	add := func(name, doc string) {
		var expect any
		relays, err := ParseRelayList([]byte(doc))
		if err != nil {
			expect = errorOut{err.Error()}
		} else {
			expect = listOut{relays}
		}
		t.Cases = append(t.Cases, Case{name, listIn{json.RawMessage(doc)}, expect})
	}
	// Three relays that leave QRSTUVWXYZ to no relay, as a list may (SPEC.md §5.2).
	const three = `[
		{"id": "a", "name": "Relay A", "url": "wss://a.relay.example/v1/ws", "prefixes": "2345678"},
		{"id": "b", "name": "Relay B", "url": "wss://b.relay.example/v1/ws", "prefixes": "9ABCDEF"},
		{"id": "c", "name": "Relay C", "url": "wss://c.relay.example/v1/ws", "prefixes": "GHJKMNP"}]`
	one := func(relay string) string { return `{"relays": [` + relay + `]}` }
	// entry is a valid relay entry with some members replaced; a value of "omit" leaves the member out.
	entry := func(kv ...string) string {
		fields := [][2]string{{"id", `"x"`}, {"name", `"X"`}, {"url", `"wss://x.example/v1/ws"`}, {"prefixes", `"A"`}}
		for i := 0; i < len(kv); i += 2 {
			for j := range fields {
				if fields[j][0] == kv[i] {
					fields[j][1] = kv[i+1]
				}
			}
		}
		var parts []string
		for _, f := range fields {
			if f[1] != "omit" {
				parts = append(parts, `"`+f[0]+`": `+f[1])
			}
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	str := func(s string) string { b, _ := json.Marshal(s); return string(b) }

	add("a list that leaves symbols to no relay", `{"relays": `+three+`}`)
	add("a ws url is refused", one(entry("url", `"ws://x.example/v1/ws"`)))
	add("a ws url with a port is refused", one(entry("url", `"ws://127.0.0.1:18100/v1/ws"`)))
	add("a wss url with an IP address and a port", one(entry("url", `"wss://127.0.0.1:18100/v1/ws"`)))
	add("path before /v1/ws is kept", one(entry("url", `"wss://example.com/owp/v1/ws"`)))
	add("unknown fields are ignored",
		`{"protocol": 1, "relays": [{"id": "x", "name": "X", "url": "wss://x.example/v1/ws", "prefixes": "A",
		"weight": 3}], "motd": "hi"}`)
	add("id of 32 characters", one(entry("id", str(strings.Repeat("a", 31)+"9"))))
	add("id with digits and dashes", one(entry("id", `"eu-2"`)))
	add("name of 64 characters", one(entry("name", str(strings.Repeat("\u00e9", 64)))))
	add("not an object", `[1, 2]`)
	add("relays missing", `{"protocol": 1}`)
	add("relays empty", `{"relays": []}`)
	add("relays not an array", `{"relays": {"id": "x"}}`)
	add("relay not an object", `{"relays": ["wss://x.example/v1/ws"]}`)
	add("id missing", one(entry("id", "omit")))
	add("id empty", one(entry("id", `""`)))
	add("id not a string", one(entry("id", "7")))
	add("id of 33 characters", one(entry("id", str(strings.Repeat("a", 33)))))
	add("id with an upper-case letter", one(entry("id", `"Eu"`)))
	add("id starting with a dash", one(entry("id", `"-eu"`)))
	add("id with an underscore", one(entry("id", `"eu_2"`)))
	add("duplicate id", `{"relays": [`+entry()+`, `+entry("url", `"wss://y.example/v1/ws"`, "prefixes", `"B"`)+`]}`)
	add("name missing", one(entry("name", "omit")))
	add("name null", one(entry("name", "null")))
	add("name empty", one(entry("name", `""`)))
	add("name not a string", one(entry("name", "5")))
	add("name of 65 characters", one(entry("name", str(strings.Repeat("\u00e9", 65)))))
	add("name with a control character", one(entry("name", `"Eu\u0007rope"`)))
	add("name with a C1 control character", one(entry("name", `"Eu\u0085rope"`)))
	add("name with U+009F", one(entry("name", `"Europe\u009f"`)))
	add("name with a right-to-left override", one(entry("name", `"\u202eeporuE"`)))
	add("name with an isolate", one(entry("name", `"\u2066Europe\u2069"`)))
	add("name with a left-to-right mark", one(entry("name", `"Europe\u200e"`)))
	add("name with the Arabic letter mark", one(entry("name", `"Europe\u061c"`)))
	add("name with a no-break space", one(entry("name", `"North\u00a0America"`)))
	add("name with a zero-width joiner", one(entry("name", `"Europe\u200d"`)))
	add("name of 64 emoji", one(entry("name", str(strings.Repeat("\U0001F600", 64)))))
	add("name of 65 emoji", one(entry("name", str(strings.Repeat("\U0001F600", 65)))))
	add("name with a surrogate pair escape", one(entry("name", `"Europe \ud83d\ude00"`)))
	add("name with an unpaired high surrogate", one(entry("name", `"Eu\ud800rope"`)))
	add("name with an unpaired low surrogate", one(entry("name", `"\udc00Europe"`)))
	add("name with a high surrogate before a pair", one(entry("name", `"Europe\ud800\ud83d\ude00"`)))
	add("name ending in a high surrogate", one(entry("name", `"Europe\ud83d"`)))
	add("url missing", one(entry("url", "omit")))
	add("url not a string", one(entry("url", "1")))
	add("https url", one(entry("url", `"https://x.example/v1/ws"`)))
	add("url without /v1/ws", one(entry("url", `"wss://x.example/"`)))
	add("url with a trailing slash", one(entry("url", `"wss://x.example/v1/ws/"`)))
	add("url with a query", one(entry("url", `"wss://x.example/v1/ws?a=1"`)))
	add("url with a fragment", one(entry("url", `"wss://x.example/v1/ws#a"`)))
	add("url without a host", one(entry("url", `"wss:///v1/ws"`)))
	add("upper-case scheme", one(entry("url", `"WSS://x.example/v1/ws"`)))
	add("url with a space", one(entry("url", `"wss://x.example/my relay/v1/ws"`)))
	add("url with a tab", one(entry("url", `"wss://x.example\t/v1/ws"`)))
	add("url with a control character", one(entry("url", `"wss://x.exa\u0007mple/v1/ws"`)))
	add("url with DEL", one(entry("url", `"wss://x.example\u007f/v1/ws"`)))
	add("url with a non-ASCII host", one(entry("url", `"wss://b\u00fccher.example/v1/ws"`)))
	add("url with an unpaired surrogate", one(entry("url", `"wss://x.example\ud800/v1/ws"`)))
	add("url with percent-encoding and a tilde", one(entry("url", `"wss://x.example/~owp/a%20b/v1/ws"`)))
	add("url with a punycode host", one(entry("url", `"wss://xn--bcher-kva.example/v1/ws"`)))
	add("url with an IPv6 literal and a port", one(entry("url", `"wss://[fd00::20]:8080/v1/ws"`)))
	add("url with a greater-than sign in the host", one(entry("url", `"wss://x>y.example/v1/ws"`)))
	add("prefixes missing", one(entry("prefixes", "omit")))
	add("prefixes empty", one(entry("prefixes", `""`)))
	add("prefixes not a string", one(entry("prefixes", `["A"]`)))
	add("lower-case prefix", one(entry("prefixes", `"a"`)))
	add("prefix outside the alphabet", one(entry("prefixes", `"A0"`)))
	add("symbol repeated in one relay", one(entry("prefixes", `"AA"`)))
	add("symbol owned by two relays", `{"relays": [`+entry("prefixes", `"AB"`)+`, `+
		entry("id", `"y"`, "url", `"wss://y.example/v1/ws"`, "prefixes", `"BC"`)+`]}`)
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// names

type nameIn struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

type validOut struct {
	Valid bool `json:"valid"`
}

// jsonText encodes a string as JSON text without HTML escaping.
func jsonText(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func namesTable() Table {
	t := Table{Name: "names"}
	add := func(field, caseName, raw string, valid func(string) bool) {
		value := decode([]byte(raw)).(string)
		t.Cases = append(t.Cases, Case{caseName, nameIn{field, json.RawMessage(raw)}, validOut{valid(value)}})
	}
	name := func(caseName, value string) { add("name", caseName, jsonText(value), ValidName) }
	client := func(caseName, value string) { add("client", caseName, jsonText(value), ValidClient) }

	name("name: one character", "A")
	name("name: empty", "")
	name("name: 32 characters", strings.Repeat("a", 32))
	name("name: 33 characters", strings.Repeat("a", 33))
	name("name: 32 emoji are 32 characters", strings.Repeat("\U0001F600", 32))
	name("name: 33 emoji are 33 characters", strings.Repeat("\U0001F600", 33))
	name("name: a combining mark is a character", strings.Repeat("e\u0301", 16))
	name("name: 33 code points with combining marks", strings.Repeat("e\u0301", 16)+"x")
	name("name: spaces are kept and allowed", " Anna ")
	name("name: emoji ZWJ sequence", "\U0001F469\u200d\U0001F4BB")
	name("name: zero-width space is allowed", "An\u200bna")
	name("name: no-break space is allowed", "Anna\u00a0B")
	name("name: narrow no-break space is allowed", "Anna\u202fB")
	name("name: line separator is allowed", "Anna\u2028B")
	name("name: byte order mark is allowed", "\ufeffAnna")
	name("name: NUL", "An\u0000na")
	name("name: tab", "An\tna")
	name("name: line feed", "Anna\n")
	name("name: U+001F", "Anna\u001f")
	name("name: DEL", "Anna\u007f")
	name("name: U+0080", "Anna\u0080")
	name("name: U+009F", "Anna\u009f")
	name("name: Arabic letter mark U+061C", "Anna\u061c")
	name("name: left-to-right mark U+200E", "Anna\u200e")
	name("name: right-to-left mark U+200F", "Anna\u200f")
	name("name: left-to-right embedding U+202A", "\u202aAnna")
	name("name: right-to-left override U+202E", "\u202eannA")
	name("name: left-to-right isolate U+2066", "\u2066Anna")
	name("name: pop directional isolate U+2069", "Anna\u2069")

	client("client: app and version", "Example/1.31.0")
	client("client: one character each", "a/b")
	client("client: no slash", "Example")
	client("client: empty app", "/1.0")
	client("client: empty version", "Example/")
	client("client: only a slash", "/")
	client("client: the version may hold slashes", "Example/1.0/beta")
	client("client: a slash right after the first", "a//")
	client("client: two slashes, empty app", "//")
	client("client: spaces are allowed", "Open Watch/1.0 beta")
	client("client: 64 characters", "App/"+strings.Repeat("1", 60))
	client("client: 65 characters", "App/"+strings.Repeat("1", 61))
	client("client: 64 characters, some outside ASCII", "\u00c5pp/"+strings.Repeat("\u00e9", 60))
	client("client: line feed", "Example/1.0\n")
	client("client: right-to-left mark", "Example/1.0\u200f")
	client("client: empty", "")

	add("name", "name: surrogate pair escapes are one character", `"`+strings.Repeat(`\ud83d\ude00`, 32)+`"`, ValidName)
	add("name", "name: unpaired high surrogate", `"Anna\ud800"`, ValidName)
	add("name", "name: unpaired low surrogate", `"\udc00Anna"`, ValidName)
	add("name", "name: two high surrogates", `"\ud83d\ud83dAnna"`, ValidName)
	add("client", "client: unpaired surrogate in the version", `"Example/1.0\udfff"`, ValidClient)
	return t
}

// members builds a JSON object from ordered name/value pairs, values as JSON text. changes replace or append
// members; a value of "omit" leaves the member out.
func members(base [][2]string, changes ...string) string {
	fields := slices.Clone(base)
	for i := 0; i < len(changes); i += 2 {
		j := slices.IndexFunc(fields, func(f [2]string) bool { return f[0] == changes[i] })
		if j < 0 {
			fields = append(fields, [2]string{changes[i], changes[i+1]})
		} else {
			fields[j][1] = changes[i+1]
		}
	}
	var parts []string
	for _, f := range fields {
		if f[1] != "omit" {
			parts = append(parts, `"`+f[0]+`": `+f[1])
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ---------------------------------------------------------------------------------------------------------------------
// media-check

type mediaCheckIn struct {
	On    string          `json:"on"`
	Media json.RawMessage `json:"media"`
}

// portable reports whether every SDK encodes v to the same number of bytes: no control characters, U+2028 or U+2029
// in strings (encoders escape them differently) and, when numbers is set, only integer literals as numbers. Numbers
// outside `ext` don't need the check: the SDKs' Media types hold them as integers.
func portable(v any, numbers bool) bool {
	switch v := v.(type) {
	case string:
		return !strings.ContainsFunc(v, func(c rune) bool { return c < 0x20 || c == '\u2028' || c == '\u2029' })
	case json.Number:
		_, err := strconv.ParseInt(string(v), 10, 64)
		return !numbers || err == nil
	case []any:
		return !slices.ContainsFunc(v, func(x any) bool { return !portable(x, numbers) })
	case map[string]any:
		for k, x := range v {
			if !portable(k, numbers) || !portable(x, numbers) {
				return false
			}
		}
	}
	return true
}

func mediaCheckTable() Table {
	t := Table{Name: "media-check"}
	add := func(name, on, raw string) {
		send := on == "send"
		valid := CheckMedia([]byte(raw), send)
		if m, _ := decode([]byte(raw)).(object); send && valid && !(portable(m, false) && portable(m["ext"], true)) {
			panic(fmt.Sprintf("media-check %q: its size depends on the encoder", name))
		}
		t.Cases = append(t.Cases, Case{name + " (" + on + ")", mediaCheckIn{on, json.RawMessage(raw)}, validOut{valid}})
	}
	// value cases are well typed, so every SDK can decode them into its Media type and check them both ways; shape
	// cases break a type and only the receive path sees them.
	value := func(name, raw string) { add(name, "receive", raw); add(name, "send", raw) }
	shape := func(name, raw string) { add(name, "receive", raw) }
	// sized checks that a case's name tells the truth about its size.
	sized := func(name, raw string, whole, extBytes int) {
		w, e := SentSizes(decode([]byte(raw)).(object))
		if w != whole || e != extBytes {
			panic(fmt.Sprintf("media-check %q: %d bytes, ext %d bytes", name, w, e))
		}
		value(name, raw)
	}

	base := [][2]string{
		{"kind", `"movie"`}, {"title", `"Heat"`}, {"year", "1995"}, {"ids", `{"imdb": "tt0113277", "tmdb": "949"}`},
		{"durationMs", "10200000"},
		{"file", `{"sizeBytes": 4700000000, "oshash": "8e245d9679d31e12", "name": "Heat.1995.1080p.BluRay.mkv"}`},
		{"ext", `{"com.example": {"server": "1a2b3c", "item": "4711"}}`},
	}
	movie := func(changes ...string) string { return members(base, changes...) }
	episode := func(changes ...string) string {
		return movie(append([]string{"kind", `"episode"`, "title", `"Pilot"`,
			"series", `{"title": "Breaking Bad", "year": 2008, "ids": {"tvdb": "81189"}}`, "season", "1", "episode", "1"},
			changes...)...)
	}
	fileWith := func(name string) string { return `{"sizeBytes": 1000, "name": ` + name + `}` }

	value("movie", movie())
	value("only kind, title and durationMs", `{"kind": "movie", "title": "Heat", "durationMs": 1}`)
	value("episode", episode())
	value("video with a file and no IDs", movie("kind", `"video"`, "title", `"Beach 2024"`, "year", "omit", "ids", "omit"))
	value("a movie may carry series", movie("series", `{"title": "Heat"}`))
	value("unknown members are ignored", movie("rating", `"R"`, "file", `{"name": "Heat.mkv", "codec": "hevc"}`))
	value("unknown ID namespace", movie("ids", `{"trakt": "1234"}`))
	value("year 0 and negative years are ints", movie("year", "-50"))
	value("int written as 3487000.0", movie("durationMs", "3487000.0"))
	value("int written with an exponent", movie("durationMs", "3.487e6"))
	value("title with an emoji ZWJ sequence", movie("title", `"Family \ud83d\udc68\u200d\ud83d\udc69\u200d\ud83d\udc67"`))
	value("title in a right-to-left script", movie("title", `"\u05d4\u05e9\u05d5\u05d3"`))
	value("title with a no-break space", movie("title", `"Heat\u00a0II"`))
	value("title empty", movie("title", `""`))
	value("title with a line feed", movie("title", `"Heat\n"`))
	value("title with NUL", movie("title", `"He\u0000at"`))
	value("title with a C1 control character", movie("title", `"Heat\u0085"`))
	value("title with a right-to-left override", movie("title", `"\u202etaeH"`))
	value("title with an isolate", movie("title", `"\u2068Heat\u2069"`))
	value("title with an unpaired high surrogate", movie("title", `"Heat\ud800"`))
	value("title with an unpaired low surrogate", movie("title", `"\udc00Heat"`))
	value("episode without series", movie("kind", `"episode"`))
	value("series title empty", episode("series", `{"title": ""}`))
	value("series title with a left-to-right mark", episode("series", `{"title": "Breaking Bad\u200e"}`))
	value("series title with an unpaired surrogate", episode("series", `{"title": "Breaking\udbff Bad"}`))
	value("season 0 is allowed", episode("season", "0"))
	value("season -1", episode("season", "-1"))
	value("episode -1", episode("episode", "-1"))
	value("ID empty", movie("ids", `{"imdb": ""}`))
	value("series ID empty", episode("series", `{"title": "Breaking Bad", "ids": {"tvdb": ""}}`))
	value("ID with an unpaired surrogate", movie("ids", `{"imdb": "tt\ud800"}`))
	value("ID namespace with an unpaired surrogate", movie("ids", `{"im\udc00db": "tt0113277"}`))
	value("durationMs 1", movie("durationMs", "1"))
	value("durationMs 0", movie("durationMs", "0"))
	value("durationMs negative", movie("durationMs", "-5"))
	value("durationMs 604800000 (7 days)", movie("durationMs", "604800000"))
	value("durationMs 604800001", movie("durationMs", "604800001"))
	value("durationMs 2^53 - 1", movie("durationMs", "9007199254740991"))
	value("sizeBytes 0", movie("file", `{"sizeBytes": 0}`))
	value("sizeBytes negative", movie("file", `{"sizeBytes": -1}`))
	value("oshash upper case", movie("file", `{"oshash": "8E245D9679D31E12"}`))
	value("oshash of 15 digits", movie("file", `{"oshash": "8e245d9679d31e1"}`))
	value("oshash of 17 digits", movie("file", `{"oshash": "8e245d9679d31e120"}`))
	value("oshash not hex", movie("file", `{"oshash": "8e245d9679d31e1g"}`))
	value("file name with a leading dot", movie("file", fileWith(`".beach.mov"`)))
	value("file name of three dots", movie("file", fileWith(`"..."`)))
	value("file name starting with three dots", movie("file", fileWith(`"...x.mp4"`)))
	value("file name with two dots inside", movie("file", fileWith(`"Heat..1995.mkv"`)))
	value("file name empty", movie("file", fileWith(`""`)))
	value("file name .", movie("file", fileWith(`"."`)))
	value("file name ..", movie("file", fileWith(`".."`)))
	value("file name with a slash", movie("file", fileWith(`"Movies/Heat.mkv"`)))
	value("file name is a slash", movie("file", fileWith(`"/"`)))
	value("file name with a backslash", movie("file", fileWith(`"C:\\Movies\\Heat.mkv"`)))
	value("file name climbing directories", movie("file", fileWith(`"../../.ssh/id_rsa"`)))
	value("file name with NUL", movie("file", fileWith(`"Heat.mkv\u0000.txt"`)))
	value("file name with a right-to-left override", movie("file", fileWith(`"Heat\u202evkm.exe"`)))
	value("file name with an unpaired surrogate", movie("file", fileWith(`"Heat\ud800.mkv"`)))
	value("ext member of two labels", movie("ext", `{"org.example": {"type": "movie", "metaId": "tt0113277"}}`))
	value("ext member with digits and dashes", movie("ext", `{"com.example-2.app": 1}`))
	value("ext values may be any JSON", movie("ext", `{"app.a": null, "app.b": [1, "x"], "app.c": true}`))
	value("ext empty", movie("ext", `{}`))
	value("ext name of one label", movie("ext", `{"example": {}}`))
	value("ext name with an upper-case letter", movie("ext", `{"com.Example": {}}`))
	value("ext name with an empty label", movie("ext", `{"com..example": {}}`))
	value("ext name ending in a dot", movie("ext", `{"com.example.": {}}`))
	value("ext name with an underscore", movie("ext", `{"com.exam_ple": {}}`))
	value("ext name with an unpaired surrogate", movie("ext", `{"com.example\ud800": {}}`))
	value("ext value with an unpaired surrogate", movie("ext", `{"com.example": {"server": ["a\udc00"]}}`))
	value("ext member name with an unpaired surrogate", movie("ext", `{"com.example": {"\ud800": 1}}`))

	shape("not an object", `["movie"]`)
	shape("null", `null`)
	shape("kind missing", movie("kind", "omit"))
	shape("kind unknown", movie("kind", `"show"`))
	shape("kind upper case", movie("kind", `"Movie"`))
	shape("title missing", movie("title", "omit"))
	shape("title null", movie("title", "null"))
	shape("title a number", movie("title", "1995"))
	shape("year not integral", movie("year", "1995.5"))
	shape("year a string", movie("year", `"1995"`))
	shape("year null", movie("year", "null"))
	shape("ids not an object", movie("ids", `["tt0113277"]`))
	shape("ID a number", movie("ids", `{"tmdb": 949}`))
	shape("series not an object", episode("series", `"Breaking Bad"`))
	shape("series without title", episode("series", `{"year": 2008}`))
	shape("series year a string", episode("series", `{"title": "Breaking Bad", "year": "2008"}`))
	shape("season not integral", episode("season", "1.5"))
	shape("durationMs missing", movie("durationMs", "omit"))
	shape("durationMs not integral", movie("durationMs", "1000.5"))
	shape("durationMs a string", movie("durationMs", `"1000"`))
	shape("durationMs too large for a double", movie("durationMs", "1e400"))
	shape("file not an object", movie("file", `"Heat.mkv"`))
	shape("sizeBytes a string", movie("file", `{"sizeBytes": "1000"}`))
	shape("file name a number", movie("file", `{"name": 1995}`))
	shape("ext not an object", movie("ext", `["com.example"]`))

	// Sizes: measured on send only, on the SDK's own compact encoding.
	titled := func(title string) string { return `{"kind":"movie","title":"` + title + `","durationMs":1000}` }
	overhead := len(titled(""))
	sized("descriptor of 4096 bytes", titled(strings.Repeat("a", maxMediaBytes-overhead)), 4096, 0)
	sized("descriptor of 4097 bytes", titled(strings.Repeat("a", maxMediaBytes+1-overhead)), 4097, 0)
	eAcute := strings.Repeat("\u00e9", (maxMediaBytes-overhead)/2)
	sized("4096 bytes, U+00E9 counts as two", titled(eAcute+strings.Repeat("a", (maxMediaBytes-overhead)%2)), 4096, 0)
	sized("4097 bytes, U+00E9 counts as two", titled(eAcute+strings.Repeat("a", (maxMediaBytes-overhead)%2+1)), 4097, 0)
	spaced := strings.ReplaceAll(titled(strings.Repeat("a", maxMediaBytes-overhead)), ",", " ,   ")
	sized("whitespace in the input doesn't count", spaced, 4096, 0)
	sized("unknown members don't count", strings.TrimSuffix(titled(strings.Repeat("a", maxMediaBytes-overhead)), "}")+
		`,"notes":"`+strings.Repeat("n", 200)+`"}`, 4096, 0)
	sized("ints are sent as integers", strings.Replace(titled(strings.Repeat("a", maxMediaBytes-overhead)),
		`"durationMs":1000`, `"durationMs":1000.000`, 1), 4096, 0)
	extOf := func(note string) string { return `{"com.example":{"note":"` + note + `"}}` }
	extOverhead := len(extOf(""))
	withExt := func(ext string) string { return `{"kind":"movie","title":"Heat","durationMs":1000,"ext":` + ext + `}` }
	ext1024 := extOf(strings.Repeat("x", maxExtBytes-extOverhead))
	ext1025 := extOf(strings.Repeat("x", maxExtBytes+1-extOverhead))
	sized("ext of 1024 bytes", withExt(ext1024), len(withExt(ext1024)), 1024)
	sized("ext of 1025 bytes", withExt(ext1025), len(withExt(ext1025)), 1025)
	return t
}

// ---------------------------------------------------------------------------------------------------------------------
// room-check

// roomCheckIn holds the members of a `room` message other than `type` and `media`: the snapshot fields and an
// optional `action`.
type roomCheckIn struct {
	Room json.RawMessage `json:"room"`
}

func roomCheckTable() Table {
	t := Table{Name: "room-check"}
	add := func(name, raw string) {
		t.Cases = append(t.Cases, Case{name, roomCheckIn{json.RawMessage(raw)}, validOut{CheckRoom([]byte(raw))}})
	}
	hostBase := [][2]string{
		{"peerId", `"p1"`}, {"name", `"Edde"`}, {"client", `"Example/1.31.0"`}, {"host", "true"}, {"connected", "true"},
		{"player", `"ready"`}, {"match", `{"state": "matched", "deltaMs": -120}`}, {"offsetMs", "0"},
	}
	host := func(changes ...string) string { return members(hostBase, changes...) }
	guest := func(changes ...string) string {
		return host(append([]string{"peerId", `"p2"`, "name", `"Anna"`, "client", `"Sample/2.4.0"`, "host", "false",
			"connected", "false", "player", `"idle"`, "match", "null"}, changes...)...)
	}
	list := func(ps ...string) string { return "[" + strings.Join(ps, ", ") + "]" }
	roomBase := [][2]string{
		{"rev", "42"}, {"code", `"K7MQ2X"`}, {"epoch", "3"}, {"phase", `"playing"`}, {"positionMs", "61234"},
		{"atMs", "918273645"}, {"rate", "1.0"}, {"control", `"host"`}, {"locked", "false"}, {"waitingOn", "[]"},
		{"participants", list(host(), guest())},
	}
	room := func(changes ...string) string { return members(roomBase, changes...) }
	withGuest := func(changes ...string) string { return room("participants", list(host(), guest(changes...))) }

	add("the snapshot of SPEC.md section 9.1", room())
	add("idle room", room("epoch", "0", "phase", `"idle"`, "positionMs", "0", "atMs", "0"))
	add("waiting room", room("phase", `"waiting"`, "waitingOn", `["p2"]`))
	add("paused room with control for everyone", room("phase", `"paused"`, "control", `"everyone"`, "locked", "true"))
	add("rate 0.5", room("rate", "0.5"))
	add("rate 2", room("rate", "2"))
	add("rev written as 42.0", room("rev", "42.0"))
	add("rev written with an exponent", room("rev", "4.2e1"))
	add("atMs 2^53 - 1", room("atMs", "9007199254740991"))
	add("action by a participant", room("action", `{"kind": "seek", "by": "p2"}`))
	add("action by a peer who has left", room("action", `{"kind": "pause", "by": "p3"}`))
	add("action by the relay", room("action", `{"kind": "ended", "by": null}`))
	add("action of a kind this SDK doesn't know", room("action", `{"kind": "chapter", "by": "p1"}`))
	add("action with unknown members", room("action", `{"kind": "seek", "by": "p1", "rev": 41}`))
	add("lastAction is an unknown member", room("lastAction", `"seek"`))
	add("match without deltaMs", withGuest("match", `{"state": "searching"}`))
	add("negative offsetMs", withGuest("offsetMs", "-2500"))
	add("positionMs 604800000", room("phase", `"paused"`, "positionMs", "604800000"))
	add("offsetMs 604800000", withGuest("offsetMs", "604800000"))
	add("offsetMs -604800000", withGuest("offsetMs", "-604800000"))
	add("unknown members are ignored", room("theme", `"dark"`, "participants", list(host("avatar", `"x"`), guest())))
	add("peer IDs of 16 characters", room("participants", list(host("peerId", `"Ab-_0123456789xy"`), guest())))

	add("not an object", `[42]`)
	for _, k := range []string{"rev", "code", "epoch", "phase", "positionMs", "atMs", "rate", "control", "locked",
		"waitingOn", "participants"} {
		add(k+" missing", room(k, "omit"))
	}
	add("rev 0", room("rev", "0"))
	add("rev not integral", room("rev", "42.5"))
	add("rev a string", room("rev", `"42"`))
	add("code in lower case", room("code", `"k7mq2x"`))
	add("code of 5 symbols", room("code", `"K7MQ2"`))
	add("code with a letter outside the alphabet", room("code", `"K7MQ2I"`))
	add("epoch negative", room("epoch", "-1"))
	add("phase unknown", room("phase", `"stopped"`))
	add("phase upper case", room("phase", `"Playing"`))
	add("positionMs negative", room("positionMs", "-1"))
	add("positionMs 604800001", room("phase", `"paused"`, "positionMs", "604800001"))
	add("positionMs too large for a double", room("positionMs", "1e400"))
	add("atMs negative", room("atMs", "-1"))
	add("atMs 2^53", room("atMs", "9007199254740992"))
	add("rate 0.49", room("rate", "0.49"))
	add("rate 2.01", room("rate", "2.01"))
	add("rate 0", room("rate", "0"))
	add("rate negative", room("rate", "-1"))
	add("rate a string", room("rate", `"1.0"`))
	add("rate null", room("rate", "null"))
	add("control unknown", room("control", `"all"`))
	add("locked a string", room("locked", `"false"`))
	add("locked a number", room("locked", "0"))
	add("waitingOn not an array", room("phase", `"waiting"`, "waitingOn", `"p2"`))
	add("waitingOn holds a number", room("phase", `"waiting"`, "waitingOn", `[2]`))
	add("waitingOn holds a bad peer ID", room("phase", `"waiting"`, "waitingOn", `["p 2"]`))
	add("waitingOn holds a peer ID twice", room("phase", `"waiting"`, "waitingOn", `["p2", "p2"]`))
	add("waitingOn not empty while playing", room("waitingOn", `["p2"]`))
	add("idle room at a position", room("phase", `"idle"`, "positionMs", "5"))
	add("action null", room("action", "null"))
	add("action a string", room("action", `"seek"`))
	add("action without kind", room("action", `{"by": "p1"}`))
	add("action kind empty", room("action", `{"kind": "", "by": "p1"}`))
	add("action kind a number", room("action", `{"kind": 3, "by": "p1"}`))
	add("action without by", room("action", `{"kind": "seek"}`))
	add("action by a number", room("action", `{"kind": "seek", "by": 2}`))
	add("action by an empty peer ID", room("action", `{"kind": "seek", "by": ""}`))
	add("action by a peer ID of 17 characters", room("action", `{"kind": "seek", "by": "Ab-_0123456789xyz"}`))
	add("participants empty", room("participants", "[]"))
	add("participants not an array", room("participants", host()))
	add("participant not an object", room("participants", list(host(), `"p2"`)))
	add("no host", room("participants", list(host("host", "false"), guest())))
	add("two hosts", room("participants", list(host(), guest("host", "true"))))
	add("host a string", withGuest("host", `"false"`))
	add("connected missing", withGuest("connected", "omit"))
	add("peer ID of 17 characters", withGuest("peerId", `"Ab-_0123456789xyz"`))
	add("peer ID empty", withGuest("peerId", `""`))
	add("peer ID with a dot", withGuest("peerId", `"p.2"`))
	add("name of 33 characters", withGuest("name", jsonText(strings.Repeat("a", 33))))
	add("name with a right-to-left override", withGuest("name", `"\u202eannA"`))
	add("name with an unpaired surrogate", withGuest("name", `"Anna\ud800"`))
	add("client without a slash", withGuest("client", `"Sample"`))
	add("player unknown", withGuest("player", `"playing"`))
	add("match a string", withGuest("match", `"matched"`))
	add("match state unknown", withGuest("match", `{"state": "found"}`))
	add("match deltaMs not integral", withGuest("match", `{"state": "mismatched", "deltaMs": 1.5}`))
	add("match deltaMs -2^63", withGuest("match", `{"state": "mismatched", "deltaMs": -9223372036854775808}`))
	add("offsetMs not integral", withGuest("offsetMs", "0.5"))
	add("offsetMs 604800001", withGuest("offsetMs", "604800001"))
	add("offsetMs -604800001", withGuest("offsetMs", "-604800001"))
	add("offsetMs missing", withGuest("offsetMs", "omit"))
	return t
}
