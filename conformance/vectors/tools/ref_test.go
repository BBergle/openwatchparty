package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// These tests pin the boundaries of spec/SDK.md with values worked out by hand, so a change to ref.go that moves a
// boundary fails here before it silently rewrites the vectors.

func TestTimelineBoundaries(t *testing.T) {
	r := Room{"playing", 1000, 5000, 1}
	cases := []struct {
		now, want int64
		running   bool
	}{
		{4999, 1000, false}, // future atMs: hold
		{5000, 1000, true},
		{5001, 1001, true},
		{8999, 4999, true},
		{9000, 5000, true}, // reaches durationMs
		{9001, 5000, true}, // capped
	}
	for _, c := range cases {
		if got := Position(r, 5000, c.now); got != c.want {
			t.Errorf("Position(t=%d) = %d, want %d", c.now, got, c.want)
		}
		if got := Running(r, c.now); got != c.running {
			t.Errorf("Running(t=%d) = %v, want %v", c.now, got, c.running)
		}
	}
	if got := Position(Room{"playing", 0, 0, 1.15}, 1e9, 100); got != 114 {
		t.Errorf("1.15 × 100 floors the double product: got %d, want 114", got)
	}
}

func TestClockBoundaries(t *testing.T) {
	c := NewClock()
	c.Ping(1, 0)
	if got := c.Pong(1, 1000+500, 1000); got != "accepted" {
		t.Fatalf("rtt 1000: %s", got)
	}
	c.Ping(2, 2000)
	if got := c.Pong(2, 9999, 3001); got != "ignored" {
		t.Fatalf("rtt 1001: %s", got)
	}
	// best: offset 1000, rtt 1000. A sample with rtt 0 tolerates (0+1000)/2+100 = 600.
	c.Ping(3, 4000)
	if got := c.Pong(3, 4000+1600, 4000); got != "accepted" {
		t.Fatalf("difference 600: %s", got)
	}
	c.Ping(4, 5000)
	if got := c.Pong(4, 5000+400-1, 5000); got != "stepped" {
		// best is now offset 1600 rtt 0; tolerance 100; difference 1201
		t.Fatalf("difference over tolerance: %s", got)
	}
	if best, _ := c.Best(); best.offset != 399 || best.rtt != 0 {
		t.Fatalf("after step: %+v", best)
	}
}

func TestReconcileBands(t *testing.T) {
	base := ReconcileIn{
		Room: Room{"playing", 0, 0, 1}, DurationMs: 1e7, ServerNowMs: 100000,
		Player: Player{PositionMs: 100000, CanSetRate: true, Rate: 1},
	}
	do := func(drift int64, nudging, canSetRate bool) ReconcileOut {
		in := base
		in.Player.PositionMs += drift
		in.Nudging = nudging
		in.Player.CanSetRate = canSetRate
		if nudging {
			in.Player.Rate = 0.96
		}
		return Reconcile(in)
	}
	kinds := func(o ReconcileOut) string {
		s := ""
		for _, a := range o.Actions {
			s += a.Do + " "
		}
		return s
	}
	for _, c := range []struct {
		drift            int64
		nudging, canRate bool
		wantActions      string
		wantNudgingAfter bool
	}{
		{350, false, true, "", false},
		{351, false, true, "setRate ", true},
		{2000, false, true, "setRate ", true},
		{2001, false, true, "seek ", false},
		{150, true, true, "", true},
		{149, true, true, "setRate ", false},
		{1000, false, false, "", false},
		{1001, false, false, "seek ", false},
	} {
		got := do(c.drift, c.nudging, c.canRate)
		if kinds(got) != c.wantActions || got.Nudging != c.wantNudgingAfter {
			t.Errorf("drift %d nudging %v rate %v: got %q nudging %v", c.drift, c.nudging, c.canRate, kinds(got), got.Nudging)
		}
	}
	held := base
	held.Room.Phase = "paused"
	held.Room.PositionMs = 100000
	held.Player.Paused = true
	held.Player.PositionMs = 100500
	if got := Reconcile(held); len(got.Actions) != 0 {
		t.Errorf("held 500 ms off: %v", got.Actions)
	}
	held.Player.PositionMs = 100501
	if got := Reconcile(held); kinds(got) != "seek " {
		t.Errorf("held 501 ms off: %v", got.Actions)
	}
}

func TestReconcileSeekAccuracy(t *testing.T) {
	in := func(drift, accuracy int64, canSetRate bool) ReconcileIn {
		return ReconcileIn{
			Room: Room{"playing", 0, 0, 1}, DurationMs: 1e7, ServerNowMs: 100000,
			Player: Player{PositionMs: 100000 + drift, CanSetRate: canSetRate, Rate: 1, SeekAccuracyMs: accuracy},
		}
	}
	seeks := func(o ReconcileOut) bool {
		return slices.ContainsFunc(o.Actions, func(a Action) bool { return a.Do == "seek" })
	}
	for _, c := range []struct {
		drift, accuracy int64
		canRate, seek   bool
	}{
		{2001, 0, true, true}, {2001, 1999, true, true}, {2001, 2001, true, false}, {2002, 2001, true, true},
		{1001, 0, false, true}, {1001, 1001, false, false}, {-1002, 1001, false, true},
	} {
		if got := Reconcile(in(c.drift, c.accuracy, c.canRate)); seeks(got) != c.seek {
			t.Errorf("drift %d accuracy %d rate %v: %v", c.drift, c.accuracy, c.canRate, got.Actions)
		}
	}
	held := in(0, 800, true)
	held.Room.Phase = "paused"
	held.Room.PositionMs = 100000
	held.Player.Paused = true
	held.Player.PositionMs = 100800
	if got := Reconcile(held); seeks(got) {
		t.Errorf("held 800 ms off, accuracy 800: %v", got.Actions)
	}
	held.Player.PositionMs = 100801
	if got := Reconcile(held); !seeks(got) {
		t.Errorf("held 801 ms off, accuracy 800: %v", got.Actions)
	}
}

func TestRankKeepsEveryKind(t *testing.T) {
	file := &File{Oshash: "00ff00ff00ff00ff"}
	for _, kind := range []string{"movie", "episode", "video"} {
		m := Media{Kind: kind, Title: "x", DurationMs: 100000, File: file}
		got := Rank(m, []Candidate{{ID: "other", Oshash: "1111111111111111"}, {ID: "same", Oshash: "00ff00ff00ff00ff"}})
		if len(got) != 2 || got[0].ID != "same" {
			t.Errorf("%s: %v", kind, got)
		}
	}
}

func TestMatchTolerance(t *testing.T) {
	m := Media{Kind: "movie", Title: "x", DurationMs: 100000}
	for d, want := range map[int64]string{1500: "matched", -1500: "matched", 1501: "mismatch", -1501: "mismatch"} {
		got := Match(MatchIn{m, []Candidate{{ID: "a", DurationMs: new(100000 + d)}}})
		if got.Result != want {
			t.Errorf("delta %d: %s, want %s", d, got.Result, want)
		}
	}
}

func TestMatchPublishesEveryKnownDelta(t *testing.T) {
	m := Media{Kind: "movie", Title: "x", DurationMs: 100000}
	// "a" matches first; "b" comes after the match and still has its delta; "c" is never probed.
	got := Match(MatchIn{m, []Candidate{
		{ID: "a", DurationMs: new(int64(100100))},
		{ID: "b", DurationMs: new(int64(103000))},
		{ID: "c", Probe: &Probe{DurationMs: new(int64(100000)), TakesMs: 1}},
	}})
	if len(got.Candidates) != 3 || *got.Candidates[0].DeltaMs != 100 || *got.Candidates[1].DeltaMs != 3000 ||
		got.Candidates[2].DeltaMs != nil {
		t.Errorf("published candidates %+v", got.Candidates)
	}
}

func TestCandidateDurationBounds(t *testing.T) {
	m := Media{Kind: "movie", Title: "x", DurationMs: 604800000}
	for d, want := range map[int64]string{604800000: "matched", 604800001: "unavailable", 0: "unavailable",
		1: "mismatch", -1: "unavailable"} {
		if got := Match(MatchIn{m, []Candidate{{ID: "a", DurationMs: new(d)}}}); got.Result != want {
			t.Errorf("durationMs %d: %s, want %s", d, got.Result, want)
		}
		got := Match(MatchIn{m, []Candidate{{ID: "a", Probe: &Probe{DurationMs: new(d), TakesMs: 1}}}})
		if got.Result != want || len(got.Probed) != 1 {
			t.Errorf("probe answer %d: %s %v, want %s", d, got.Result, got.Probed, want)
		}
	}
}

func TestProbeDeadlineIsInclusive(t *testing.T) {
	m := Media{Kind: "movie", Title: "x", DurationMs: 100000}
	probe := func(takes int64) Candidate {
		return Candidate{ID: "a", Probe: &Probe{DurationMs: new(int64(100000)), TakesMs: takes}}
	}
	if got := Match(MatchIn{m, []Candidate{probe(60000)}}); got.Result != "matched" {
		t.Errorf("60000 ms: %s", got.Result)
	}
	if got := Match(MatchIn{m, []Candidate{probe(60001)}}); got.Result != "unavailable" || len(got.Probed) != 0 {
		t.Errorf("60001 ms: %s %v", got.Result, got.Probed)
	}
}

func TestForbidden(t *testing.T) {
	for _, c := range []rune{0, 0x1f, 0x7f, 0x80, 0x9f, 0x61c, 0x200e, 0x200f, 0x202a, 0x202e, 0x2066, 0x2069} {
		if !Forbidden(c) {
			t.Errorf("U+%04X allowed", c)
		}
	}
	for _, c := range []rune{0x20, 0x7e, 0xa0, 0x61b, 0x200d, 0x2029, 0x202f, 0x2065, 0x206a, 0x1F600} {
		if Forbidden(c) {
			t.Errorf("U+%04X forbidden", c)
		}
	}
	if !ValidName(strings.Repeat("\U0001F600", 32)) || ValidName(strings.Repeat("a", 33)) {
		t.Error("name length is not counted in code points")
	}
	if !ValidClient("a/b") || ValidClient("/b") || ValidClient("a/") || ValidClient("ab") {
		t.Error("client shape")
	}
}

func TestCodes(t *testing.T) {
	if got, err := NormalizeCode(" k7m-q2x "); err != nil || got != "K7MQ2X" {
		t.Errorf("normalize: %q %v", got, err)
	}
	for _, bad := range []string{"K7MQ2", "K7MQ2X2", "K7MQ20", "K7MQ2O", "K7M_Q2"} {
		if _, err := NormalizeCode(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestUnpairedSurrogates(t *testing.T) {
	for raw, want := range map[string]bool{
		`"\ud83d\ude00"`:        false,
		`"\uD83D\uDE00"`:        false,
		`"\ud800"`:              true,
		`"\udc00\ud800"`:        true,
		`"\ud800\ud83d\ude00"`:  true,
		`"\\ud800"`:             false, // an escaped backslash, then text
		`{"\ud800": 1}`:         true,
		`["\u0041", "\ud7ff"]`:  false,
		`["\ue000", "\udfff"]`:  true,
		`"\ud83d\u0041\ude00"`:  true,
		`"x\"\ud800"`:           true,
		`{"a": "\\", "b": "x"}`: false,
	} {
		if got := holdsUnpaired(decode([]byte(raw))); got != want {
			t.Errorf("%s: unpaired %v, want %v", raw, got, want)
		}
	}
}

func TestInts(t *testing.T) {
	for raw, want := range map[string]bool{
		"0": true, "-0": true, "1.0": true, "1e3": true, "1.5": false, "9007199254740991": true,
		"-9007199254740991": true, "9007199254740992": false, "1e400": false, `"1"`: false, "true": false,
	} {
		if _, got := intOf(decode([]byte(raw))); got != want {
			t.Errorf("%s: int %v, want %v", raw, got, want)
		}
	}
}

func TestProtocolBounds(t *testing.T) {
	media := func(d string) []byte { return []byte(`{"kind": "movie", "title": "x", "durationMs": ` + d + `}`) }
	for d, want := range map[string]bool{"1": true, "604800000": true, "6.048e8": true, "0": false, "604800001": false} {
		if CheckMedia(media(d), true) != want || CheckMedia(media(d), false) != want {
			t.Errorf("durationMs %s: want %v", d, want)
		}
	}
	room := func(position, offset string) []byte {
		return []byte(`{"rev": 1, "code": "K7MQ2X", "epoch": 1, "phase": "paused", "positionMs": ` + position + `,
			"atMs": 0, "rate": 1, "control": "host", "locked": false, "waitingOn": [],
			"participants": [{"peerId": "p1", "name": "A", "client": "a/1", "host": true, "connected": true,
			"player": "idle", "match": null, "offsetMs": ` + offset + `}]}`)
	}
	for _, c := range []struct {
		position, offset string
		want             bool
	}{
		{"604800000", "604800000", true}, {"0", "-604800000", true}, {"604800001", "0", false},
		{"0", "604800001", false}, {"0", "-604800001", false},
	} {
		if got := CheckRoom(room(c.position, c.offset)); got != c.want {
			t.Errorf("positionMs %s offsetMs %s: %v, want %v", c.position, c.offset, got, c.want)
		}
	}
}

func TestMediaSizes(t *testing.T) {
	media := func(titleLen int) []byte {
		return []byte(`{"kind": "movie", "title": "` + strings.Repeat("a", titleLen) + `", "durationMs": 1}`)
	}
	// {"kind":"movie","title":"","durationMs":1} is 42 bytes compact.
	if !CheckMedia(media(4096-42), true) || CheckMedia(media(4097-42), true) || !CheckMedia(media(4097-42), false) {
		t.Error("descriptor size limit")
	}
}

func TestRoomRate(t *testing.T) {
	room := func(rate string) []byte {
		return []byte(`{"rev": 1, "code": "K7MQ2X", "epoch": 0, "phase": "idle", "positionMs": 0, "atMs": 0,
			"rate": ` + rate + `, "control": "host", "locked": false, "waitingOn": [],
			"participants": [{"peerId": "p1", "name": "A", "client": "a/1", "host": true, "connected": true,
			"player": "idle", "match": null, "offsetMs": 0}]}`)
	}
	for rate, want := range map[string]bool{"0.5": true, "2": true, "2.0": true, "0.4999": false, "2.0001": false} {
		if got := CheckRoom(room(rate)); got != want {
			t.Errorf("rate %s: %v, want %v", rate, got, want)
		}
	}
}

func TestListSchemes(t *testing.T) {
	list := func(url string) []byte {
		return []byte(`{"relays": [{"id": "a", "name": "A", "url": "` + url + `", "prefixes": "A"}]}`)
	}
	for url, ok := range map[string]bool{
		"wss://a.example/v1/ws": true, "ws://a.example/v1/ws": false, "wss://127.0.0.1:18100/v1/ws": true,
		"ws://127.0.0.1:18100/v1/ws": false,
	} {
		if _, err := ParseRelayList(list(url)); (err == nil) != ok {
			t.Errorf("url %q: %v", url, err)
		}
	}
}

func TestTablesAreWellFormed(t *testing.T) {
	for _, table := range tables() {
		seen := map[string]bool{}
		for _, c := range table.Cases {
			if seen[c.Name] {
				t.Errorf("%s: duplicate case name %q", table.Name, c.Name)
			}
			seen[c.Name] = true
		}
		a, err := render(table)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := render(table)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: rendering is not deterministic", table.Name)
		}
		if !json.Valid(a) {
			t.Errorf("%s: invalid JSON", table.Name)
		}
		for _, ch := range a {
			if ch >= 0x7f || ch < 0x20 && ch != '\n' {
				t.Errorf("%s: byte %#x outside printable ASCII in output", table.Name, ch)
				break
			}
		}
	}
}
