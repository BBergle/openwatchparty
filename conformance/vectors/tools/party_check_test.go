package main

import (
	"strings"
	"testing"
)

// firstTrace is the party table's first trace as a driver reads it, changed by edit. Its steps are create (id c1),
// joined, advance 14999 and advance 1; its outputs at index 2 are empty.
func firstTrace(t *testing.T, edit func(setup object, steps, outputs []any) (object, []any, []any)) Case {
	t.Helper()
	c := partyTable().Cases[0]
	in, err := generic(c.Input)
	if err != nil {
		t.Fatal(err)
	}
	exp, err := generic(c.Expect)
	if err != nil {
		t.Fatal(err)
	}
	input, expect := in.(object), exp.(object)
	setup, steps, outputs := edit(input["setup"].(object), input["steps"].([]any), expect["outputs"].([]any))
	return Case{c.Name, object{"setup": setup, "steps": steps}, object{"outputs": outputs}}
}

func TestCheckTracesRefusesMalformedTraces(t *testing.T) {
	type edit = func(setup object, steps, outputs []any) (object, []any, []any)
	step := func(i int, s object) edit {
		return func(setup object, steps, outputs []any) (object, []any, []any) {
			steps[i] = s
			return setup, steps, outputs
		}
	}
	output := func(i int, ch string, v any) edit {
		return func(setup object, steps, outputs []any) (object, []any, []any) {
			outputs[i].(object)[ch] = v
			return setup, steps, outputs
		}
	}
	setupMember := func(k string, v any) edit {
		return func(setup object, steps, outputs []any) (object, []any, []any) {
			setup[k] = v
			return setup, steps, outputs
		}
	}
	cases := []struct {
		name string
		edit edit
		want string
	}{
		{"unknown step kind", step(2, object{"wait": 5}), "no known step kind"},
		{"two step kinds", step(2, object{"advance": 1, "drop": nil}), "two step kinds"},
		{"unknown step member", step(2, object{"advance": 1, "speed": 2}), "unknown member speed"},
		{"advance of 0", step(2, object{"advance": 0}), "advance takes"},
		{"fewer outputs than steps", func(setup object, steps, outputs []any) (object, []any, []any) {
			return setup, steps, outputs[:3]
		}, "4 steps but 3 outputs"},
		{"no steps", func(setup object, steps, outputs []any) (object, []any, []any) {
			return setup, []any{}, []any{}
		}, "non-empty"},
		{"unknown call", step(0, object{"call": "dance", "id": "c1"}), `unknown call "dance"`},
		{"resume, no longer an API", step(2, object{"call": "resume", "id": "c2"}), `unknown call "resume"`},
		{"unknown call member", step(0, object{"call": "create", "id": "c1", "code": "K7MQ2X"}), "unknown member code"},
		{"missing call member", step(2, object{"call": "seek"}), "missing ms"},
		{"call needing an id without one", step(0, object{"call": "create"}), "needs an id"},
		{"call id used twice", step(2, object{"call": "play", "id": "c1"}), `"c1" used twice`},
		{"call never done", func(setup object, steps, outputs []any) (object, []any, []any) {
			delete(outputs[1].(object), "done")
			return setup, steps, outputs
		}, "never reports done"},
		{"done naming no call", output(2, "done", []any{object{"id": "c9"}}), `"c9" names no call`},
		{"done twice", output(2, "done", []any{object{"id": "c1"}}), `"c1" is done twice`},
		{"done with value and error", output(1, "done", []any{object{"id": "c1", "value": 1, "error": "forbidden"}}),
			"not both"},
		{"unknown command", func(setup object, steps, outputs []any) (object, []any, []any) {
			steps[2] = object{"player": "p1"}
			outputs[2].(object)["command"] = []any{object{"player": "p1", "do": "stop"}}
			return setup, steps, outputs
		}, "unknown command stop"},
		{"unknown channel", output(2, "log", []any{object{"line": "x"}}), "unknown member log"},
		{"empty channel", output(2, "send", []any{}), "non-empty array"},
		{"empty state", output(2, "state", object{}), "non-empty object"},
		{"unknown state field", output(2, "state", object{"volume": 1}), "unknown member volume"},
		{"timeScale, no longer a state field", output(2, "state", object{"timeScale": 1}), "unknown member timeScale"},
		{"command on an unknown player", output(2, "command", []any{object{"player": "p9", "do": "play"}}),
			"unknown player p9"},
		{"unknown event", output(2, "event", []any{object{"type": "party"}}), "unknown event type"},
		{"sent ping", output(2, "send", []any{object{"type": "ping", "id": 1}}), "other than ping"},
		{"unknown resolver entry", output(2, "resolver", []any{object{"search": "Heat"}}), "unknown entry search"},
		{"net entry without a URL", output(2, "net", []any{object{"get": ""}}), "get takes a URL"},
		{"scripted pong", step(2, object{"recv": object{"type": "pong", "id": 1, "serverTime": 5}}), "answers pings"},
		{"refused snapshot", step(2, object{"recv": object{"type": "room", "rev": 0}}), "mark it malformed"},
		{"malformed mark on a valid joined", func(setup object, steps, outputs []any) (object, []any, []any) {
			steps[1].(object)["malformed"] = true
			return setup, steps, outputs
		}, "passes its check"},
		{"malformed mark on an unknown type", step(2, object{"recv": object{"type": "hello"}, "malformed": true}),
			"malformed applies to"},
		{"malformed mark on a media message, no longer a type", step(2, object{"recv": object{"type": "media",
			"epoch": 1, "media": nil}, "malformed": true}), "malformed applies to"},
		{"malformed false", step(2, object{"recv": object{"type": "hello"}, "malformed": false}), "malformed takes true"},
		{"refused error", step(2, object{"recv": object{"type": "error", "code": "", "message": "x", "re": nil}}),
			"mark it malformed"},
		{"recvText without text", step(2, object{"recvText": object{"type": "room"}}), "recvText takes a string"},
		{"on with an http URL", step(2, object{"drop": nil, "on": "http://relay.test"}), "on takes"},
		{"negative rtt", step(2, object{"rtt": -1}), "rtt takes"},
		{"send entry with on but no msg", output(2, "send", []any{object{"on": "ws://relay.test/v1/ws"}}),
			"a message or {on, msg}"},
		{"done re without an error", output(1, "done", []any{object{"id": "c1", "value": "K7MQ2X", "re": nil}}),
			"go with an error"},
		{"setMedia failing the send checks without an id", step(2, object{"call": "setMedia", "ref": "x",
			"media": object{"kind": "movie", "title": ""}}), "needs an id"},
		{"candidate with an empty ref", step(2, object{"answer": []any{object{"id": "a", "ref": ""}}}), "wrong type"},
		{"hold of an unknown player", step(2, object{"hold": "p1"}), "unknown player p1"},
		{"nextGet with text", step(2, object{"nextGet": "later"}), "nextGet takes"},
		{"nextOpen with a body", step(2, object{"nextOpen": object{}}), "nextOpen takes"},
		{"unknown setup member", setupMember("seed", 1), "unknown member seed"},
		{"connectTimeoutMs, no longer an option", setupMember("connectTimeoutMs", 3000), "unknown member connectTimeoutMs"},
		{"base with a trailing /", setupMember("base", "http://relay.test/"), "base must be"},
		{"base of another scheme", setupMember("base", "ftp://relay.test"), "base must be"},
		{"list with a base", setupMember("list", object{"relays": []any{}}), "list must be null"},
		{"random draw of 1", setupMember("random", []any{1}), "random draws"},
		{"random draw finer than 2^-53", setupMember("random", []any{0.1}), "multiples of 2^-53"},
		{"name too long", setupMember("name", strings.Repeat("a", 33)), "name or client"},
	}
	for _, c := range cases {
		err := checkTraces([]Case{firstTrace(t, c.edit)})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
}

func TestCheckTracesRefusesDuplicateNames(t *testing.T) {
	c := partyTable().Cases[0]
	if err := checkTraces([]Case{c, c}); err == nil || !strings.Contains(err.Error(), "duplicate case name") {
		t.Errorf("got %v, want a duplicate name error", err)
	}
}

func TestCheckTracesAcceptsThePublicSource(t *testing.T) {
	public := func(list any) Case {
		return firstTrace(t, func(setup object, steps, outputs []any) (object, []any, []any) {
			setup["base"] = nil
			setup["list"] = list
			return setup, steps, outputs
		})
	}
	relay := func(url string) object {
		return object{"relays": []any{object{"id": "na", "name": "North America", "url": url, "prefixes": "2345678"}}}
	}
	if err := checkTraces([]Case{public(relay("wss://na.relay.openwatch.party/v1/ws"))}); err != nil {
		t.Error(err)
	}
	for _, list := range []any{nil, relay("ws://na.relay.openwatch.party/v1/ws")} {
		if err := checkTraces([]Case{public(list)}); err == nil || !strings.Contains(err.Error(), "valid public list") {
			t.Errorf("list %v: got %v, want the public source to refuse it", list, err)
		}
	}
}

func TestCheckTracesAcceptsMarkedMalformedInput(t *testing.T) {
	c := firstTrace(t, func(setup object, steps, outputs []any) (object, []any, []any) {
		joined := steps[1].(object)["recv"].(object)
		joined["you"] = object{"peerId": "ann", "token": strings.Repeat("t", 42)}
		steps[1].(object)["malformed"] = true
		steps[2] = object{"recvText": "[1,2]"}
		steps[3] = object{"recv": object{"type": "pong", "id": 1, "serverTime": -1}, "malformed": true}
		return setup, steps, outputs
	})
	if err := checkTraces([]Case{c}); err != nil {
		t.Error(err)
	}
}
