package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

// SPEC §4 and §6: versioning, envelope, validation, refusal order, name and client.

// rawCase is one raw message and the error it must get; re "" means null.
type rawCase struct {
	raw, code, re string
}

func sendRawCases(c *Client, cases []rawCase) error {
	for _, s := range cases {
		if err := c.sendRaw(websocket.TextMessage, []byte(s.raw)); err != nil {
			return err
		}
		if err := expectErr(c, s.code, s.re); err != nil {
			return fmt.Errorf("after %s: %w", truncate(s.raw, 60), err)
		}
	}
	return nil
}

// scValidation: envelope, type and shape refusals (§6.2) with their re, ints as JSON Schema reads them (§2), null in
// an optional field (§2), unknown members ignored at every depth (§4), duplicate member names and unpaired surrogate
// escapes refused at every depth (§4); a refusal never closes the connection.
func scValidation(x *sc) error {
	e, err := x.dial("E")
	if err != nil {
		return err
	}
	if err := sendRawCases(e, []rawCase{
		{"not json", "invalid_message", ""},
		{"[]", "invalid_message", ""},
		{`"ping"`, "invalid_message", ""},
		{`{"id":1}`, "invalid_message", ""},
		{`{"type":42}`, "invalid_message", ""},
		{`{"type":null}`, "invalid_message", ""},
		{`{"type":"bogus"}`, "unsupported", "bogus"},
		{`{"type":"Ping","id":1}`, "unsupported", "Ping"},
		{`{"type":"joined"}`, "unsupported", "joined"},
		{`{"type":"ping"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":null}`, "invalid_message", "ping"},
		{`{"type":"ping","id":true}`, "invalid_message", "ping"},
		{`{"type":"ping","id":{}}`, "invalid_message", "ping"},
		// Two type members, also when one is spelled with an escape, are an envelope error; so is a type with an
		// unpaired surrogate escape. An unknown type is refused before its duplicates.
		{`{"type":"ping","type":"ping","id":1}`, "invalid_message", ""},
		{`{"type":"ping","id":1,"\u0074ype":"ping"}`, "invalid_message", ""},
		{`{"type":"ping\uD800","id":1}`, "invalid_message", ""},
		{`{"type":"bogus\uDC00"}`, "invalid_message", ""},
		{`{"type":"bogus","x":1,"x":2}`, "unsupported", "bogus"},
		{`{"type":"bogus","x":"\uD800"}`, "unsupported", "bogus"},
		// Duplicates elsewhere, compared after decoding escapes, at any depth, unknown members included.
		{`{"type":"ping","id":1,"id":2}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"i\u0064":1}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x":1,"x":1}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x":{"a":1,"b":{"c":1,"\u0063":2}}}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x":[{"a":1},{"a":1,"a":2}]}`, "invalid_message", "ping"},
		// Unpaired surrogate escapes in any other string, member names included.
		{`{"type":"ping","id":"\uD83D"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":"\uDE00"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":"\uDE00\uD83D"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":"\uD83D\u0041"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":"\uD83Dx"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":"\uD83D\uD83D\uDE00"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x":"\udfff"}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x\uDBFF":1}`, "invalid_message", "ping"},
		{`{"type":"ping","id":1,"x":{"y":["\uD800"]}}`, "invalid_message", "ping"},
	}); err != nil {
		return err
	}
	if err := e.sendRaw(websocket.BinaryMessage, []byte(`{"type":"ping","id":1}`)); err != nil {
		return err
	}
	if err := expectErr(e, "invalid_message", ""); err != nil {
		return fmt.Errorf("binary message: %w", err)
	}
	_ = e.send(Msg{"type": "ping", "id": "alive", "xFuture": Msg{"nested": true}})
	if _, err := e.waitFor(reply, "pong after the refusals", isType("pong")); err != nil {
		return err
	}
	// Accepted: a surrogate pair (either case), an escaped backslash before u, equal names in different objects,
	// names that differ in case.
	for _, s := range []struct{ raw, id string }{
		{`{"type":"ping","id":"\uD83D\uDE00"}`, "😀"},
		{`{"type":"ping","id":"\ud83d\ude00"}`, "😀"},
		{`{"type":"ping","id":"\\uD800"}`, `\uD800`},
		{`{"type":"ping","id":"\\\\uD800\\"}`, `\\uD800\`},
		{`{"type":"ping","id":"s","x":{"id":1},"y":[{"id":2},{"id":3}]}`, "s"},
		{`{"type":"ping","id":"c","ID":2,"Type":"x"}`, "c"},
	} {
		if err := e.sendRaw(websocket.TextMessage, []byte(s.raw)); err != nil {
			return err
		}
		p, err := e.waitFor(reply, "pong", func(m Msg) bool { return isType("pong")(m) || isType("error")(m) })
		if err != nil {
			return err
		}
		if str(p, "type") != "pong" || str(p, "id") != s.id {
			return fmt.Errorf("after %s: got %s, want pong id %q", s.raw, brief(p), s.id)
		}
	}
	if _, err := enter(e, createMsg("E")); err != nil {
		return err
	}
	// play/pause/seek/rate without media get invalid_message whatever their arguments (§12.1), so the shape checks
	// of those run in a room with media, paused so seeks land deterministically.
	if err := e.send(Msg{"type": "setMedia", "media": sampleMedia(7_200_000)}); err != nil {
		return err
	}
	// E has sent no status, so it counts as loading (§12.3) and the room waits for it; a pause stops the wait.
	if _, err := e.waitFor(reply, "the media, waiting on E", func(m Msg) bool { return hasMedia(m) && waitingOnIs(e.PeerID)(m) }); err != nil {
		return err
	}
	_ = e.send(Msg{"type": "pause"})
	if _, err := e.waitFor(reply, "paused", isPhase("paused")); err != nil {
		return err
	}
	if err := sendRawCases(e, []rawCase{
		{`{"type":"status","epoch":0,"player":"dancing","match":null,"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":null}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":-1,"player":"idle","match":null,"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":1.5,"player":"idle","match":null,"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":9007199254740992,"player":"idle","match":null,"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":{"state":"maybe"},"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":{},"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":{"state":"matched","deltaMs":0.5},"offsetMs":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":null,"offsetMs":"0"}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":null,"offsetMs":0,"rttMs":-1}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":null,"offsetMs":0,"rttMs":null}`, "invalid_message", "status"},
		{`{"type":"seek"}`, "invalid_message", "seek"},
		{`{"type":"seek","positionMs":-5}`, "invalid_message", "seek"},
		{`{"type":"seek","positionMs":1.5}`, "invalid_message", "seek"},
		{`{"type":"seek","positionMs":null}`, "invalid_message", "seek"},
		{`{"type":"seek","positionMs":"10"}`, "invalid_message", "seek"},
		{`{"type":"seek","positionMs":9007199254740992}`, "invalid_message", "seek"},
		{`{"type":"pause","positionMs":-1}`, "invalid_message", "pause"},
		{`{"type":"pause","positionMs":null}`, "invalid_message", "pause"},
		{`{"type":"rate","rate":3.0}`, "invalid_message", "rate"},
		{`{"type":"rate","rate":0.4}`, "invalid_message", "rate"},
		{`{"type":"rate","rate":"1"}`, "invalid_message", "rate"},
		{`{"type":"rate"}`, "invalid_message", "rate"},
		{`{"type":"setMedia","media":null}`, "invalid_message", "setMedia"},
		{`{"type":"setMedia","media":{"kind":"movie","title":"X","durationMs":1000},"positionMs":null}`, "invalid_message", "setMedia"},
		{`{"type":"setControl","mode":"all"}`, "invalid_message", "setControl"},
		{`{"type":"setControl"}`, "invalid_message", "setControl"},
		{`{"type":"lock","locked":"yes"}`, "invalid_message", "lock"},
		{`{"type":"lock","locked":null}`, "invalid_message", "lock"},
		{`{"type":"transferHost","peerId":5}`, "invalid_message", "transferHost"},
		{`{"type":"kick"}`, "invalid_message", "kick"},
	}); err != nil {
		return err
	}
	// Ints: any number with an integral value in range, whatever its JSON spelling (§2). Seeks that follow within
	// seekCoalesceMs are held and applied later (§12.8), so wait for each one's own snapshot.
	for _, s := range []struct {
		raw  string
		want float64
	}{
		{`{"type":"seek","positionMs":3000.0}`, 3000},
		{`{"type":"seek","positionMs":4e3}`, 4000},
		{`{"type":"seek","positionMs":9007199254740991}`, 7_200_000},
	} {
		if err := e.sendRaw(websocket.TextMessage, []byte(s.raw)); err != nil {
			return err
		}
		r, err := e.waitFor(reply, "room after "+s.raw, func(m Msg) bool { return isType("room")(m) || isType("error")(m) })
		if err != nil {
			return err
		}
		if str(r, "type") != "room" || num(r, "positionMs") != s.want {
			return fmt.Errorf("after %s: %s, want paused at %v", s.raw, brief(r), s.want)
		}
	}
	// Unknown members at every depth are ignored: this status is applied.
	_ = e.send(Msg{"type": "status", "epoch": 1, "player": "loading", "offsetMs": 0, "xFuture": 1,
		"match": Msg{"state": "searching", "xNested": Msg{"deeper": []any{1}}}})
	r, err := e.waitFor(reply, "room after a status with unknown members", func(m Msg) bool { return isType("room")(m) || isType("error")(m) })
	if err != nil {
		return err
	}
	if p := participant(r, e.PeerID); str(r, "type") != "room" || str(p, "player") != "loading" {
		return fmt.Errorf("status with unknown members: %s", brief(r))
	}
	return nil
}

// scRefusalOrder: the checks of §6.2 and each message's refusals run in their order.
func scRefusalOrder(x *sc) error {
	out, err := x.dial("Out")
	if err != nil {
		return err
	}
	// Shape before membership; a bound is part of the shape (§7.5, §10).
	if err := sendRawCases(out, []rawCase{
		{`{"type":"seek"}`, "invalid_message", "seek"},
		{`{"type":"status","epoch":0}`, "invalid_message", "status"},
		{`{"type":"status","epoch":0,"player":"idle","match":null,"offsetMs":-604800001}`, "invalid_message", "status"},
		{`{"type":"setMedia","media":{"kind":"movie","title":"X","durationMs":604800001}}`, "invalid_message", "setMedia"},
		{`{"type":"status","epoch":0,"player":"idle","match":null,"offsetMs":-604800000}`, "not_in_room", "status"},
		{`{"type":"setMedia","media":{"kind":"movie","title":"X","durationMs":604800000}}`, "not_in_room", "setMedia"},
		{`{"type":"seek","positionMs":10}`, "not_in_room", "seek"},
		{`{"type":"leave"}`, "not_in_room", "leave"},
	}); err != nil {
		return err
	}
	cs, err := x.roomOf(2)
	if err != nil {
		return err
	}
	a, b := cs[0], cs[1]
	if err := sendRawCases(a, []rawCase{
		{`{"type":"create","name":"","client":"x/1"}`, "invalid_message", "create"},
		{`{"type":"bogus"}`, "unsupported", "bogus"},
		{`{"type":"join","code":"ZZZZZZ","name":"A","client":"x/1"}`, "already_in_room", "join"},
	}); err != nil {
		return err
	}
	// Permission before room state: in a room without media, play from a participant that isn't a controller is
	// forbidden, not invalid_message; transferHost and kick naming nobody from a non-host are forbidden.
	return sendRawCases(b, []rawCase{
		{`{"type":"play"}`, "forbidden", "play"},
		{`{"type":"seek","positionMs":5}`, "forbidden", "seek"},
		{`{"type":"transferHost","peerId":"nobody"}`, "forbidden", "transferHost"},
		{`{"type":"kick","peerId":"nobody"}`, "forbidden", "kick"},
		{`{"type":"clearMedia"}`, "forbidden", "clearMedia"},
	})
}

// scNameClient: name is 1–maxNameLength characters without forbidden characters (Cc, C1 included, and Bidi_Control);
// client is <app>/<version>, 1–maxClientLength characters without forbidden characters; other format and space
// characters are allowed; both are stored unchanged (§2, §6.3).
func scNameClient(x *sc) error {
	maxName, maxClient := int(x.lim("maxNameLength")), int(x.lim("maxClientLength"))
	e, err := x.dial("E")
	if err != nil {
		return err
	}
	bad := []Msg{
		{"name": "", "client": clientName},
		{"name": strings.Repeat("é", maxName+1), "client": clientName},
		{"name": "bell\u0007", "client": clientName},
		{"name": "del\u007f", "client": clientName},
		{"name": "E"},
		{"name": "E", "client": nil},
		{"name": "E", "client": "NoSlash"},
		{"name": "E", "client": "/1.0"},
		{"name": "E", "client": "App/"},
		{"name": "E", "client": "App/1.0\n"},
		{"name": "E", "client": "A/" + strings.Repeat("v", maxClient-1)},
		{"client": clientName},
		{"name": 5, "client": clientName},
	}
	// Every Bidi_Control character and C1 controls at both ends of their range in name; some of them in <app> and
	// in <version>.
	for _, r := range []rune{0x80, 0x85, 0x9f, 0x61c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e,
		0x2066, 0x2067, 0x2068, 0x2069} {
		bad = append(bad, Msg{"name": "E" + string(r) + "e", "client": clientName})
	}
	for _, c := range []string{"Ap\u0080p/1.0", "App/1.0\u009f", "\u202eppA/1.0", "App/\u20661.0\u2069"} {
		bad = append(bad, Msg{"name": "E", "client": c})
	}
	for _, m := range bad {
		m["type"] = "create"
		_ = e.send(m)
		if err := expectErr(e, "invalid_message", "create"); err != nil {
			return fmt.Errorf("create name %+q client %+q: %w", m["name"], m["client"], err)
		}
	}
	_ = e.send(Msg{"type": "join", "code": "ZZZZZZ", "name": "E\u202e", "client": clientName})
	if err := expectErr(e, "invalid_message", "join"); err != nil {
		return fmt.Errorf("join with name E U+202E: %w", err)
	}
	_ = e.send(Msg{"type": "join", "code": "ZZZZZZ", "name": "E", "client": "NoSlash"})
	if err := expectErr(e, "invalid_message", "join"); err != nil {
		return fmt.Errorf("join with client NoSlash: %w", err)
	}
	name, client := strings.Repeat("é", maxName-1)+"名", "Äpp/"+strings.Repeat("v", maxClient-6)+"/2"
	j, err := enter(e, Msg{"type": "create", "name": name, "client": client})
	if err != nil {
		return fmt.Errorf("create with a %d-character name and a %d-character client: %w", maxName, maxClient, err)
	}
	p := participant(obj(j, "room"), e.PeerID)
	if str(p, "name") != name || str(p, "client") != client {
		return fmt.Errorf("stored name %q client %q, want them unchanged", str(p, "name"), str(p, "client"))
	}
	// Allowed: no-break spaces, the zero width joiner, the separators and spaces next to the Bidi_Control ranges.
	f, err := x.dial("F")
	if err != nil {
		return err
	}
	name, client = "A\u00a0B\u200dC\u2029D\u202fE\u200bF", "Äpp\u00a0X/1.0\u200d\u202f"
	j, err = enter(f, Msg{"type": "create", "name": name, "client": client})
	if err != nil {
		return fmt.Errorf("create with name %+q client %+q: %w", name, client, err)
	}
	if p := participant(obj(j, "room"), f.PeerID); str(p, "name") != name || str(p, "client") != client {
		return fmt.Errorf("stored name %+q client %+q, want %+q and %+q", str(p, "name"), str(p, "client"), name, client)
	}
	return nil
}

// findExamples looks for spec/examples in the working directory and the directories above it; "" when there is none.
func findExamples() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, "spec", "examples")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// clientTypes are the client → relay message types (§7). Every other example type is the relay's, or a list.
var clientTypes = map[string]bool{
	"create": true, "join": true, "resume": true, "ping": true, "status": true, "play": true, "pause": true,
	"seek": true, "rate": true, "setMedia": true, "clearMedia": true, "setControl": true, "transferHost": true,
	"kick": true, "lock": true, "leave": true, "end": true,
}

// example is a spec/examples file as the suite sends it, byte for byte: its type is the file name's first part, and
// a descriptor goes as the media of a setMedia.
type example struct {
	name, typ string
	raw       []byte
}

// msg is the example as it decodes.
func (ex example) msg() (Msg, error) {
	var m Msg
	if err := json.Unmarshal(ex.raw, &m); err != nil {
		return nil, fmt.Errorf("example %s: %w", ex.name, err)
	}
	return m, nil
}

// readExamples reads the client → relay message and descriptor examples of spec/examples/dir, in file name order.
func (x *sc) readExamples(dir string) ([]example, error) {
	paths, err := filepath.Glob(filepath.Join(x.examples, dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []example
	for _, path := range paths {
		name := filepath.Base(path)
		typ, _, _ := strings.Cut(name, ".")
		if typ != "descriptor" && !clientTypes[typ] {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("example %w", err)
		}
		if typ == "descriptor" {
			typ, b = "setMedia", slices.Concat([]byte(`{"type":"setMedia","media":`), b, []byte("}"))
		}
		out = append(out, example{name, typ, b})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no client message or descriptor examples in %s", filepath.Join(x.examples, dir))
	}
	return out, nil
}

// closeFor is the close status §3.1 gives a message on receipt, before any check of its content, or 0.
func (x *sc) closeFor(raw []byte) int {
	switch {
	case len(raw) > int(x.lim("maxMessageBytes")):
		return websocket.CloseMessageTooBig
	case !utf8.Valid(raw):
		return websocket.CloseInvalidFramePayloadData
	}
	return 0
}

// refusalOf is the error §6.2 gives a message that breaks its envelope or its shape: invalid_message with re null
// for an invalid envelope, unsupported for an unknown type, else invalid_message with re its type.
func refusalOf(raw []byte) (code, re string) {
	typ, ok := envelopeType(raw)
	switch {
	case !ok:
		return "invalid_message", ""
	case !clientTypes[typ]:
		return "unsupported", typ
	}
	return "invalid_message", typ
}

// envelopeType is the type of a message whose envelope is valid (§6.2 step 2): a JSON object with exactly one type
// member, names compared after decoding escapes, whose value is a string without an unpaired surrogate escape.
func envelopeType(raw []byte) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", false
	}
	var lit json.RawMessage
	n := 0
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return "", false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "", false
		}
		if k == "type" {
			n, lit = n+1, v
		}
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	var typ string
	if n != 1 || lit[0] != '"' || json.Unmarshal(lit, &typ) != nil || loneSurrogate(lit) {
		return "", false
	}
	return typ, true
}

// loneSurrogate reports whether a JSON string literal holds an unpaired surrogate escape (§4).
func loneSurrogate(lit []byte) bool {
	high := false // the last escape was a high surrogate, still unpaired
	for i := 0; i < len(lit); i++ {
		if lit[i] != '\\' || i+1 == len(lit) || lit[i+1] != 'u' || i+6 > len(lit) {
			if high {
				return true
			}
			if lit[i] == '\\' {
				i++ // an escaped character, a backslash included
			}
			continue
		}
		u, err := strconv.ParseUint(string(lit[i+2:i+6]), 16, 16)
		if err != nil {
			return false // not JSON; the decoder refuses it first
		}
		i += 5
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if high {
				return true
			}
			high = true
		case u >= 0xDC00 && u <= 0xDFFF:
			if !high {
				return true
			}
			high = false
		case high:
			return true
		}
	}
	return high
}

// scExamples: every client → relay example of spec/examples/invalid gets, from outside a room, the refusal §6.2
// gives it (shape comes before membership), or the close §3.1 gives it when it is too large or not UTF-8; every
// create, setMedia, descriptor and status example of spec/examples/valid, sent byte for byte, is accepted and
// stored or forwarded as it decodes (§3.1, §4, §6.2, §6.3, §7.5, §8.3, §10).
func scExamples(x *sc) error {
	if x.examples == "" {
		return skipf("needs spec/examples: run from the repository or pass --examples DIR")
	}
	invalid, err := x.readExamples("invalid")
	if err != nil {
		return err
	}
	valid, err := x.readExamples("valid")
	if err != nil {
		return err
	}
	e, err := x.dial("E")
	if err != nil {
		return err
	}
	for _, ex := range invalid {
		if code := x.closeFor(ex.raw); code != 0 {
			c, err := x.dial("U")
			if err != nil {
				return err
			}
			if err := c.sendRaw(websocket.TextMessage, ex.raw); err != nil {
				return err
			}
			if err := closedWith(c, code, true); err != nil {
				return fmt.Errorf("%s: %w", ex.name, err)
			}
			continue
		}
		if err := e.sendRaw(websocket.TextMessage, ex.raw); err != nil {
			return err
		}
		code, re := refusalOf(ex.raw)
		if err := expectErr(e, code, re); err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
	}
	byType := map[string][]example{}
	for _, ex := range valid {
		byType[ex.typ] = append(byType[ex.typ], ex)
	}
	// Each create on one connection, which then leaves: the room goes with its only participant (§11.6).
	v, err := x.dial("V")
	if err != nil {
		return err
	}
	for _, ex := range byType["create"] {
		want, err := ex.msg()
		if err != nil {
			return err
		}
		if err := v.sendRaw(websocket.TextMessage, ex.raw); err != nil {
			return err
		}
		j, err := v.waitFor(reply, "joined", func(m Msg) bool { return isType("joined")(m) || isType("error")(m) })
		if err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
		if str(j, "type") != "joined" {
			return fmt.Errorf("%s: got %s, want joined", ex.name, brief(j))
		}
		v.setJoined(j)
		if p := participant(obj(j, "room"), v.PeerID); str(p, "name") != str(want, "name") || str(p, "client") != str(want, "client") {
			return fmt.Errorf("%s: stored name %+q client %+q, want %+q and %+q", ex.name, str(p, "name"),
				str(p, "client"), str(want, "name"), str(want, "client"))
		}
		if err := v.send(Msg{"type": "leave"}); err != nil {
			return err
		}
	}
	host, _, err := x.create("H")
	if err != nil {
		return err
	}
	for _, ex := range byType["setMedia"] {
		want, err := ex.msg()
		if err != nil {
			return err
		}
		media := obj(want, "media")
		if err := host.sendRaw(websocket.TextMessage, ex.raw); err != nil {
			return err
		}
		r, err := host.waitFor(reply, "room with media", func(m Msg) bool { return hasMedia(m) || isType("error")(m) })
		if err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
		if str(r, "type") != "room" {
			return fmt.Errorf("%s: got %s, want a room with the media", ex.name, brief(r))
		}
		if err := mediaDiff(r["media"], media); err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
		if pos := min(num(want, "positionMs"), num(media, "durationMs")); num(r, "positionMs") != pos {
			return fmt.Errorf("%s: positionMs %v, want %.0f", ex.name, r["positionMs"], pos)
		}
	}
	// A status that changes nothing publishes nothing, so a ping after it marks where its snapshot, if any, came.
	for _, ex := range byType["status"] {
		want, err := ex.msg()
		if err != nil {
			return err
		}
		if err := host.sendRaw(websocket.TextMessage, ex.raw); err != nil {
			return err
		}
		if err := host.send(Msg{"type": "ping", "id": ex.name}); err != nil {
			return err
		}
		m, err := host.waitFor(reply, "pong", func(m Msg) bool {
			return isType("error")(m) || isType("pong")(m) && str(m, "id") == ex.name
		})
		if err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
		if str(m, "type") != "pong" {
			return fmt.Errorf("%s: got %s, want it accepted", ex.name, brief(m))
		}
		if p := participant(host.lastRoom(), host.PeerID); str(p, "player") != str(want, "player") ||
			!reflect.DeepEqual(p["match"], want["match"]) || num(p, "offsetMs") != num(want, "offsetMs") {
			return fmt.Errorf("%s: stored %v, want player, match and offsetMs as sent", ex.name, p)
		}
	}
	return nil
}
