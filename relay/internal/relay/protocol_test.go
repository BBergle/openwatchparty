package relay

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// sendRaw sends text as one text message, byte for byte.
func (c *client) sendRaw(text string) {
	c.t.Helper()
	if err := c.ws.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
		c.t.Fatal(err)
	}
}

// expectRefusal requires an error with code and re (nil for null).
func (c *client) expectRefusal(code string, re any) {
	c.t.Helper()
	if m := c.expect("error"); m["code"] != code || m["re"] != re {
		c.t.Fatalf("got %v, want %s with re %v", m, code, re)
	}
}

func TestInvalidUTF8Closes1007(t *testing.T) {
	tr := startRelay(t, testConfig())
	c := tr.dial()
	c.sendRaw("{\"type\":\"ping\",\"id\":\"\xff\"}") // inside a pass-through value
	if code := c.closeCode(); code != websocket.CloseInvalidFramePayloadData {
		t.Fatalf("close %d, want 1007", code)
	}
}

func TestDuplicateMembers(t *testing.T) {
	tr := startRelay(t, testConfig())
	c := tr.dial()
	c.sendRaw(`{"type":"ping","type":"ping","id":1}`) // envelope: re null
	c.expectRefusal(errInvalidMessage, nil)
	c.sendRaw(`{"type":"nope","a":1,"a":2}`) // the type check comes first
	c.expectRefusal(errUnsupported, "nope")
	c.sendRaw(`{"type":"ping","id":1,"x":{"y":[{"a":1,"\u0061":2}]}}`) // any depth, after unescaping
	c.expectRefusal(errInvalidMessage, "ping")
	c.sendRaw(`{"type":"ping","id":1,"x":{"a":1},"y":{"a":2}}`) // one name in two objects is fine
	c.expect("pong")
}

func TestDuplicateMembersWalk(t *testing.T) {
	deep := strings.Repeat(`[`, 4000) + `{"a":1,"b":{"a":2},"a":3}` + strings.Repeat(`]`, 4000)
	for text, want := range map[string][2]bool{
		`{"type":"a","id":1e400}`:                           {false, false}, // out of float64 range, still valid JSON
		`{"type":"a","x":{"type":1,"type":2}}`:              {false, true},
		`{"type":"a","x":[1,{"b":1}],"type":"b"}`:           {true, true},
		`{"a":"\"type\"","type":"a"}`:                       {false, false},
		`{"type":"a","s":"{[\"","t":"]}","u":1}`:            {false, false}, // brackets and quotes inside strings
		`{"type":"a","x\"y":1,"x\"y":2}`:                    {false, true},
		`{"type":"a","x":{},"y":[[],{}],"z":{"a":1},"a":2}`: {false, false}, // one name in two objects
		`{"type":"a","\u0074ype":"b"}`:                      {true, true},
		`{"type":"a","x":` + deep + `}`:                     {false, true},
	} {
		typeTwice, any := duplicateMembers([]byte(text))
		if [2]bool{typeTwice, any} != want {
			t.Errorf("duplicateMembers(%s) = %v %v, want %v", text, typeTwice, any, want)
		}
	}
}

func TestLoneSurrogates(t *testing.T) {
	for text, want := range map[string]bool{
		`"\ud83d\ude00"`:   false, // a pair
		`"\uD83D\uDE00"`:   false,
		`"\ud83d"`:         true, // high alone
		`"\ud83dx"`:        true,
		`"\ud83d\u0041"`:   true,  // high, then no low
		`"\ude00"`:         true,  // low alone
		`"\\ud83d"`:        false, // an escaped backslash, then plain text
		`"\\\ude00"`:       true,
		`{"\udc00":1}`:     true, // member names too
		`"\u00e9\n\u200d"`: false,
	} {
		if got := hasLoneSurrogate([]byte(text)); got != want {
			t.Errorf("hasLoneSurrogate(%s) = %v, want %v", text, got, want)
		}
	}

	tr := startRelay(t, testConfig())
	c := tr.dial()
	c.sendRaw(`{"type":"pi\ud800ng","id":1}`) // in the type: envelope, re null
	c.expectRefusal(errInvalidMessage, nil)
	c.sendRaw(`{"type":"ping","id":"\udfff"}`) // in a pass-through value: shape
	c.expectRefusal(errInvalidMessage, "ping")
	c.sendRaw(`{"type":"ping","id":"\ud83d\ude00"}`)
	c.expect("pong")
}

func TestForbiddenCharacters(t *testing.T) {
	tr := startRelay(t, testConfig())
	c := tr.dial()
	for _, name := range []string{"a\u202eb", "a\u2066b", "a\u0085b", "a\u061cb", "a\x7fb"} {
		c.send(msg{"type": "create", "name": name, "client": "Test/1.0"})
		c.expectRefusal(errInvalidMessage, "create")
		c.send(msg{"type": "create", "name": "N", "client": "Te" + name + "st/1.0"})
		c.expectRefusal(errInvalidMessage, "create")
	}
	c.create("👨\u200d👩", "Test/1.0") // a zero-width joiner stays allowed
}

// TestRelayFull: at maxRooms a create gets relay_full, after the rooms-per-IP
// check, and gives back the room slot it reserved.
func TestRelayFull(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MaxRooms, cfg.Limits.MaxRoomsPerIP = 1, 2
	tr := startRelay(t, cfg)
	a, b := tr.dial(), tr.dial() // both from 127.0.0.1
	a.create("A", "Test/1.0")
	b.send(msg{"type": "create", "name": "B", "client": "Test/1.0"})
	b.expectRefusal(errRelayFull, "create")
	a.send(msg{"type": "leave"}) // the room empties and is deleted
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.send(msg{"type": "create", "name": "B", "client": "Test/1.0"})
		m := b.next()
		if m["type"] == "joined" {
			break
		}
		if m["code"] != errRelayFull || time.Now().After(deadline) {
			t.Fatalf("create after the only room was deleted: %v", m)
		}
	}
	// Not rate_limited: no refused create kept the slot it reserved.
	c := tr.dial()
	c.send(msg{"type": "create", "name": "C", "client": "Test/1.0"})
	c.expectRefusal(errRelayFull, "create")
}

// TestSetMediaWaitsForEveryoneConnected: after setMedia every connected
// participant counts as loading until it reports the new epoch, so a status
// for the old media crossing setMedia changes nothing (SPEC.md §12.3).
func TestSetMediaWaitsForEveryoneConnected(t *testing.T) {
	tr := startRelay(t, testConfig())
	host, member := tr.dial(), tr.dial()
	host.create("Host", "Test/1.0")
	member.join(host.code, "Member")
	host.setMedia(movie, 0)
	r := host.until("waiting", phase("waiting"))
	if !slices.Equal(waitingOn(r), []string{host.peerID, member.peerID}) {
		t.Fatalf("waitingOn after setMedia with nobody reporting yet: %v", r)
	}
	host.status(0, "idle") // still about the old media
	member.status(1, "ready")
	r = host.until("member ready", func(r msg) bool { return slices.Equal(waitingOn(r), []string{host.peerID}) })
	if r["phase"] != "waiting" {
		t.Fatalf("a status for the old epoch paused the room: %v", r)
	}
	host.status(1, "ready")
	host.until("playing", phase("playing"))
}

// TestBounds: a descriptor's durationMs is 1-604800000 and a status's offsetMs
// within ±604800000, both ends included (SPEC.md §7.5, §10, §13.1). A value
// past either end is refused with invalid_message and changes nothing.
func TestBounds(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MessageBurst = 1000
	tr := startRelay(t, cfg)
	host := tr.dial()
	host.create("Host", "Test/1.0")
	media := func(d int64) msg {
		return msg{"type": "setMedia", "media": msg{"kind": "movie", "title": "T", "durationMs": d}}
	}
	for _, d := range []int64{0, 1, 604_800_001, 604_800_000} {
		host.send(media(d))
		if d < 1 || d > 604_800_000 {
			host.expectRefusal(errInvalidMessage, "setMedia")
		} else if m := host.expectMedia(); num(m["media"].(msg), "durationMs") != d {
			t.Fatalf("setMedia with durationMs %d: %v", d, m)
		}
	}
	for _, o := range []int64{604_800_001, 604_800_000, -604_800_001, -604_800_000} {
		host.send(msg{"type": "status", "epoch": 2, "player": "loading", "match": nil, "offsetMs": o})
		if o < -604_800_000 || o > 604_800_000 {
			host.expectRefusal(errInvalidMessage, "status")
			continue
		}
		host.until("the offset", func(r msg) bool {
			return num(r["participants"].([]any)[0].(msg), "offsetMs") == o
		})
	}
}

// TestSpecDescriptorExamples sends every descriptor and setMedia example of
// spec/examples as a host's setMedia: the valid ones change the media, the
// invalid ones get invalid_message, or close 1007 when they aren't UTF-8.
func TestSpecDescriptorExamples(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MessageBurst = 1000
	tr := startRelay(t, cfg)
	host := tr.dial()
	host.create("Host", "Test/1.0")
	for _, dir := range []string{"valid", "invalid"} {
		for _, prefix := range []string{"descriptor", "setMedia"} {
			paths, err := filepath.Glob(filepath.Join("..", "..", "..", "spec", "examples", dir, prefix+".*.json"))
			if err != nil || len(paths) == 0 {
				t.Fatalf("no %s %s examples: %v", dir, prefix, err)
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				text := string(data)
				if close := closeFor(data); close != 0 {
					c := tr.dial()
					c.sendRaw(text)
					if code := c.closeCode(); code != close {
						t.Errorf("%s: close %d, want %d", filepath.Base(path), code, close)
					}
					continue
				}
				if prefix == "descriptor" {
					text = `{"type":"setMedia","media":` + text + `}`
				}
				host.sendRaw(text)
				if dir == "valid" {
					host.expectMedia()
				} else if m := host.expect("error"); m["code"] != errInvalidMessage || m["re"] != "setMedia" {
					t.Errorf("%s: %v", filepath.Base(path), m)
				}
			}
		}
	}
}

// TestSpecInvalidMessageExamples sends every invalid client -> relay example
// of spec/examples from outside a room. Envelope, type and shape come before
// membership in the refusal order (SPEC.md §6.2), so each gets invalid_message
// with re its type, or the outcome listed below, or closes the connection on
// receipt: 1009 when too large, 1007 when not UTF-8.
func TestSpecInvalidMessageExamples(t *testing.T) {
	cfg := testConfig()
	cfg.Limits.MessageBurst = 1000
	tr := startRelay(t, cfg)
	c := tr.dial()
	envelope := map[string]bool{"end.type-null.json": true, "leave.no-type.json": true, // invalid_message, re null
		"play.duplicate-type.json": true, "play.type-lone-surrogate.json": true}
	unsupported := map[string]bool{"clearMedia.wrong-case.json": true, "play.wrong-case.json": true}
	paths, _ := filepath.Glob(filepath.Join("..", "..", "..", "spec", "examples", "invalid", "*.json"))
	sent := 0
	for _, path := range paths {
		typ, _, _ := strings.Cut(filepath.Base(path), ".")
		if !knownType(typ) {
			continue // relay -> client messages and descriptors
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sent++
		if close := closeFor(data); close != 0 {
			bad := tr.dial()
			bad.sendRaw(string(data))
			if code := bad.closeCode(); code != close {
				t.Errorf("%s: close %d, want %d", filepath.Base(path), code, close)
			}
			continue
		}
		c.sendRaw(string(data))
		m, name := c.expect("error"), filepath.Base(path)
		switch {
		case unsupported[name]:
			if m["code"] != errUnsupported {
				t.Errorf("%s: %v, want unsupported", name, m)
			}
		case envelope[name]:
			if m["code"] != errInvalidMessage || m["re"] != nil {
				t.Errorf("%s: %v, want invalid_message with re null", name, m)
			}
		case m["code"] != errInvalidMessage || m["re"] != typ:
			t.Errorf("%s: %v, want invalid_message with re %q", name, m, typ)
		}
	}
	if sent < 50 {
		t.Fatalf("only %d invalid client examples found", sent)
	}
}

// closeFor is the close code a message gets on receipt (SPEC.md §3.1), or 0.
func closeFor(data []byte) int {
	switch {
	case len(data) > maxMessageBytes:
		return websocket.CloseMessageTooBig
	case !utf8.Valid(data):
		return websocket.CloseInvalidFramePayloadData
	}
	return 0
}
