package relay

import (
	"encoding/json"
	"io"
	"maps"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRestartRestoresRooms is the SIGTERM path: Shutdown saves, a new relay on
// the same state directory restores every room paused with its participants
// reserved, and they resume with their tokens.
func TestRestartRestoresRooms(t *testing.T) {
	cfg := testConfig()
	cfg.StateDir = t.TempDir()
	first := startRelay(t, cfg)
	host, member := playingRoom(t, first)
	host.send(msg{"type": "lock", "locked": true})
	before := host.until("locked", func(r msg) bool { return r["locked"] == true })
	first.stop()
	if code := host.closeCode(); code != 1001 {
		t.Fatalf("shutdown closed with %d, want 1001", code)
	}

	saved, err := os.ReadFile(filepath.Join(cfg.StateDir, roomsFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{host.token, member.token, "127.0.0.1"} {
		if strings.Contains(string(saved), secret) {
			t.Fatalf("%s holds a token or an IP", roomsFile)
		}
	}

	second := startRelay(t, cfg)
	back := second.dial()
	back.send(msg{"type": "resume", "code": host.code, "peerId": member.peerID, "token": member.token})
	joined := back.expect("joined")
	r := back.last
	var bad []string
	for _, c := range []struct {
		field string
		ok    bool
	}{
		{"phase", r["phase"] == "paused"},
		{"locked", r["locked"] == true},
		{"epoch", num(r, "epoch") == 1},
		{"rev", num(r, "rev") > num(before, "rev")},
		{"positionMs", num(r, "positionMs") >= 1000 && num(r, "positionMs") <= 5000},
		{"host", hostOf(r) == host.peerID},
		{"media", joined["media"] != nil},
	} {
		if !c.ok {
			bad = append(bad, c.field)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("restored room: %s wrong in %v, joined.media %v", strings.Join(bad, ", "), r, joined["media"])
	}
	if !strings.Contains(jsonOf(r), `"connected":false`) {
		t.Fatalf("the host must be reserved, not connected: %v", r)
	}

	wrong := second.dial()
	wrong.send(msg{"type": "resume", "code": host.code, "peerId": host.peerID, "token": member.token})
	wrong.expectError(errResumeFailed)
	hostBack := second.dial()
	hostBack.send(msg{"type": "resume", "code": host.code, "peerId": host.peerID, "token": host.token})
	hostBack.expect("joined")
	hostBack.send(msg{"type": "play"})
	hostBack.until("playing after the restart", func(r msg) bool { return actionKind(r) == "play" })
}

func TestRestoreSkipsExpiredRooms(t *testing.T) {
	cfg := testConfig()
	cfg.StateDir = t.TempDir()
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	file := `{"version":1,"rooms":[{"code":"K7MQ2X","createdAt":` + jsonOf(old) + `,"rev":3,"epoch":0,"media":null,
		"positionMs":0,"rate":1,"control":"host","locked":false,"peerSeq":1,
		"participants":[{"peerId":"p1","tokenHash":"` + strings.Repeat("A", 43) + `","name":"H","client":"T/1",
		"host":true,"match":null,"offsetMs":0}]}]}`
	if err := os.WriteFile(filepath.Join(cfg.StateDir, roomsFile), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := startRelay(t, cfg)
	if tr.srv.roomCount() != 0 {
		t.Fatal("a room past its maximum age was restored")
	}
	c := tr.dial()
	c.send(msg{"type": "join", "code": "K7MQ2X", "name": "J", "client": "T/1"})
	c.expectError(errRoomNotFound)
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(filepath.Join(cfg.StateDir, roomsFile))
		if !strings.Contains(string(data), "K7MQ2X") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the expired room stayed in the state file")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRestoreRoomAge: a saved room's remaining age is maxRoomAgeMs minus the
// time since createdAt, without overflow at any createdAt. A createdAt in the
// future (the clock went back) counts as now, so no room outlives
// maxRoomAgeMs after its restore.
func TestRestoreRoomAge(t *testing.T) {
	s, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	wall := time.Now()
	ms := wall.UnixMilli()
	for _, tc := range []struct {
		createdAt int64
		restored  bool
	}{
		{math.MinInt64, false}, {ms - maxRoomAgeMs, false}, {ms - maxRoomAgeMs + 1000, true}, {ms, true},
		{ms + 3_600_000, true}, {math.MaxInt64, true},
	} {
		rec := roomRecord{Code: "K7MQ2X", CreatedAt: tc.createdAt, Rev: 1, Rate: 1, Control: controlHost,
			Participants: []participantRecord{{PeerID: "p1", TokenHash: strings.Repeat("A", 43), Name: "H",
				Client: "T/1", Host: true}}}
		r, err := s.restoreRoom(&rec, s.now(), wall)
		if err != nil || (r != nil) != tc.restored {
			t.Errorf("createdAt %d: restored %v (%v), want %v", tc.createdAt, r != nil, err, tc.restored)
			continue
		}
		if r != nil {
			r.mu.Lock()
			if got, want := r.record(s.now()).CreatedAt, min(tc.createdAt, ms); got != want {
				t.Errorf("createdAt %d restored as %d, want %d", tc.createdAt, got, want)
			}
			r.closed = true
			r.mu.Unlock()
		}
	}
}

// TestRestoreChecksOffsets: a saved participant's offsetMs past ±604800000
// makes the room invalid, as a status with it would have been refused.
func TestRestoreChecksOffsets(t *testing.T) {
	s, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offsetMs int64
		ok       bool
	}{{-604_800_001, false}, {-604_800_000, true}, {604_800_000, true}, {604_800_001, false}} {
		rec := roomRecord{Code: "K7MQ2X", CreatedAt: time.Now().UnixMilli(), Rev: 1, Rate: 1, Control: controlHost,
			Participants: []participantRecord{{PeerID: "p1", TokenHash: strings.Repeat("A", 43), Name: "H",
				Client: "T/1", Host: true, OffsetMs: tc.offsetMs}}}
		r, err := s.restoreRoom(&rec, s.now(), time.Now())
		if (err == nil) != tc.ok {
			t.Errorf("offsetMs %d: %v", tc.offsetMs, err)
		}
		if r != nil {
			r.mu.Lock()
			r.closed = true
			r.mu.Unlock()
		}
	}
}

// The descriptor goes out as the host sent it, whitespace included, in the
// room that starts its epoch and also after a restart (SPEC.md §8.3).
func TestMediaBytesForwarded(t *testing.T) {
	cfg := testConfig()
	cfg.StateDir = t.TempDir()
	first := startRelay(t, cfg)
	host, member := first.dial(), first.dial()
	host.create("Host", "Test/1.0")
	member.join(host.code, "Member")
	descriptor := `{ "kind" : "movie",  "title":"T\u00e9", "durationMs":5000, "ext":{"app.example":{"k":[1, 2]}} }`
	if err := host.ws.WriteMessage(1, []byte(`{"type":"setMedia","media":`+descriptor+`}`)); err != nil {
		t.Fatal(err)
	}
	rawMedia := func(c *client, typ string) string {
		for {
			c.ws.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, data, err := c.ws.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(string(data), `{"type":"`+typ+`"`) {
				return string(data)
			}
		}
	}
	if got := rawMedia(member, "room"); !strings.HasSuffix(got, `,"media":`+descriptor+`}`) {
		t.Fatalf("room after setMedia: %s", got)
	}
	first.stop()
	second := startRelay(t, cfg)
	back := second.dial()
	back.send(msg{"type": "resume", "code": host.code, "peerId": member.peerID, "token": member.token})
	if got := rawMedia(back, "joined"); !strings.HasSuffix(got, `,"media":`+descriptor+`}`) {
		t.Fatalf("joined after a restart: %s", got)
	}
}

// TestPlayingRoomCheckpoints: while a room plays, rooms.json is written at
// least every 5 s without any change, so a crash restores a position at most
// 5 s old (SPEC.md §14).
func TestPlayingRoomCheckpoints(t *testing.T) {
	cfg := testConfig()
	cfg.StateDir = t.TempDir()
	tr := startRelay(t, cfg)
	host, _ := playingRoom(t, tr)
	r := tr.srv.lookup(host.code)
	reads := 0
	for end := time.Now().Add(7 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		data, err := os.ReadFile(filepath.Join(cfg.StateDir, roomsFile))
		if err != nil {
			continue // not written yet
		}
		var file stateFile
		if err := json.Unmarshal(data, &file); err != nil || len(file.Rooms) != 1 {
			t.Fatalf("%s: %v %s", roomsFile, err, data)
		}
		r.mu.Lock()
		pos := r.position(tr.srv.now())
		r.mu.Unlock()
		if behind := pos - file.Rooms[0].PositionMs; behind > 5000 {
			t.Fatalf("the saved position is %d ms behind the playing room's", behind)
		}
		reads++
	}
	if reads == 0 {
		t.Fatalf("%s was never written", roomsFile)
	}
}

func TestUsageAndMetrics(t *testing.T) {
	cfg := testConfig()
	cfg.StateDir = t.TempDir()
	tr := startRelay(t, cfg)
	host, member := tr.dial(), tr.dial()
	host.create("Secret Name", "Example/1.31.0")
	member.send(msg{"type": "join", "code": host.code, "name": "Other", "client": "example/2"})
	member.expect("joined")

	admin := httptest.NewServer(tr.srv.AdminHandler("test"))
	defer admin.Close()
	day := time.Now().UTC().Format(time.DateOnly)
	usage, _ := getJSON(t, admin.URL+"/admin/usage")
	if usage["id"] != "test" || num(usage["days"].(msg)[day].(msg), "Example") != 1 ||
		num(usage["days"].(msg)[day].(msg), "example") != 1 {
		t.Fatalf("usage: %v", usage)
	}

	resp, err := admin.Client().Get(admin.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(body)
	for _, want := range []string{"owp_rooms 1\n", "owp_connections 2\n", "owp_rooms_created_total 1\n",
		"owp_joins_total 1\n", `owp_participants{connected="true"} 2`} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
	for _, secret := range []string{host.code, "Secret Name", "127.0.0.1"} {
		if strings.Contains(text, secret) {
			t.Errorf("metrics expose %q", secret)
		}
	}

	tr.stop()
	var saved map[string]map[string]int64
	data, err := os.ReadFile(filepath.Join(cfg.StateDir, usageFile))
	if err != nil || json.Unmarshal(data, &saved) != nil || saved[day]["Example"] != 1 {
		t.Fatalf("usage.json after shutdown: %s %v", data, err)
	}
	again, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if again.usage.days()[day]["Example"] != 1 {
		t.Fatal("usage counts were not loaded at start")
	}
}

func TestSaveInterval(t *testing.T) {
	for rooms, want := range map[int]time.Duration{0: time.Second, 1000: time.Second, 2500: 2500 * time.Millisecond,
		4000: 4 * time.Second, 100_000: 4 * time.Second} {
		if got := saveInterval(rooms); got != want {
			t.Errorf("saveInterval(%d) = %v, want %v", rooms, got, want)
		}
	}
}

func TestUsageKeeps400Days(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	oldest := now.AddDate(0, 0, -399) // the 400th day, today included
	u := newUsage("")
	u.add("Old", "a", oldest.AddDate(0, 0, -1))
	u.add("Kept", "a", oldest)
	u.add("Today", "a", now) // a new day drops the days that ran out
	days := u.days()
	if len(days) != 2 || days[oldest.Format(time.DateOnly)]["Kept"] != 1 || days["2026-10-18"]["Today"] != 1 {
		t.Fatalf("days after a new day: %v", days)
	}

	path := filepath.Join(t.TempDir(), usageFile)
	file := `{"` + oldest.AddDate(0, 0, -1).Format(time.DateOnly) + `":{"Old":1},"` + oldest.Format(time.DateOnly) +
		`":{"Kept":2},"junk":{"X":1}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	u = newUsage(path)
	if err := u.load(now); err != nil {
		t.Fatal(err)
	}
	if days := u.days(); len(days) != 1 || days[oldest.Format(time.DateOnly)]["Kept"] != 2 {
		t.Fatalf("days after load: %v", days)
	}
	if err := u.save(); err != nil { // the dropped days leave the file
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "Old") || strings.Contains(string(data), "junk") {
		t.Fatalf("usage.json kept dropped days: %s", data)
	}
}

func TestUsageSweepDropsDaysWithoutTraffic(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	oldest := now.AddDate(0, 0, -399) // the 400th day, today included
	u := newUsage("")
	u.add("Old", "a", oldest.AddDate(0, 0, -1))
	u.add("Kept", "a", oldest)
	u.sweep(now) // no counted request today, yet the day that ran out goes
	if days := u.days(); len(days) != 1 || days[oldest.Format(time.DateOnly)]["Kept"] != 1 {
		t.Fatalf("days after a sweep: %v", days)
	}
	u.sweep(now.AddDate(0, 0, 1))
	if days := u.days(); len(days) != 0 {
		t.Fatalf("days after a sweep the next day: %v", days)
	}
}

func TestUsageAppCap(t *testing.T) {
	now := time.Now()
	u := newUsage("")
	for i := range maxAppsPerDay + 5 {
		u.add(jsonOf(i), jsonOf(i), now) // one name per address
	}
	u.add("0", "x", now) // a known name still counts as itself
	apps := u.days()[now.UTC().Format(time.DateOnly)]
	if len(apps) != maxAppsPerDay+1 || apps[otherApp] != 5 || apps["0"] != 2 {
		t.Fatalf("%d names, %d other", len(apps), apps[otherApp])
	}
}

func TestUsageNamesPerAddress(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	u := newUsage("")
	for _, app := range []string{"A", "B", "C", "D"} {
		u.add(app, "2001:db8::/64", now) // the fourth new name is one too many
	}
	u.add("A", "2001:db8::/64", now) // a name already in the record counts as itself
	u.add("D", "192.0.2.1", now)     // another address may still add it
	u.add("D", "2001:db8::/64", now) // and then it counts as itself from anywhere
	want := map[string]int64{"A": 2, "B": 1, "C": 1, otherApp: 1, "D": 2}
	if got := u.days()["2026-10-18"]; !maps.Equal(got, want) {
		t.Fatalf("counts %v, want %v", got, want)
	}
	u.add("E", "2001:db8::/64", now.AddDate(0, 0, 1)) // a new UTC day clears the allowance
	if got := u.days()["2026-10-19"]; got["E"] != 1 {
		t.Fatalf("next day: %v", got)
	}
}

func TestMetricsLabelOnlyTopApps(t *testing.T) {
	tr := startRelay(t, testConfig())
	now := time.Now()
	for i := range metricsApps {
		for range 3 {
			tr.srv.usage.add("Top"+jsonOf(i), jsonOf(i), now) // 50 names counted 3 times
		}
	}
	// Live participants of a top app and of a 51st app, counted once by its create.
	for _, app := range []string{"Top7/1", "Rare/1"} {
		tr.dial().create("N", app)
	}
	var b strings.Builder
	tr.srv.writeMetrics(&b, "test")
	text := b.String()
	if !strings.Contains(text, `owp_participants_by_client{client="Top7"} 1`) ||
		!strings.Contains(text, `owp_participants_by_client{client="(other)"} 1`) || strings.Contains(text, "Rare") {
		t.Fatalf("metrics labels:\n%s", text)
	}
}
