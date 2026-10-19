package main

// The party table: traces of the SDK party state machine (spec/SDK.md §5–§15). Unlike every other table, nothing here
// computes an expectation. Each trace's outputs are written by hand from SDK.md and SPEC.md, and the comment above each
// trace names the sections it pins. checkTraces (party_check.go) refuses a malformed trace. The README's party section
// defines the format.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	trBase   = "http://relay.test"
	trURL    = "ws://relay.test/v1/ws"
	trPublic = "https://openwatch.party/relays.json"
	trCode   = "K7MQ2X"
	trName   = "Ann"
	trClient = "Example/1.0"
	trOffset = 1_000_000 // relay time = local time + setup.relayOffsetMs
	heatMs   = 10_200_000
)

// ---------------------------------------------------------------------------------------------------------------------
// JSON building blocks

// obj is a JSON object whose members keep the order they are written in, so a trace renders as it reads.
type obj []kv

type kv struct {
	k string
	v any
}

// o builds an obj from name/value pairs.
func o(pairs ...any) obj {
	if len(pairs)%2 != 0 {
		panic(fmt.Sprintf("o: odd number of arguments: %v", pairs))
	}
	m := make(obj, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		k, ok := pairs[i].(string)
		if !ok {
			panic(fmt.Sprintf("o: member name %v is not a string", pairs[i]))
		}
		m = append(m, kv{k, pairs[i+1]})
	}
	return m
}

func (m obj) MarshalJSON() ([]byte, error) {
	if m == nil {
		return []byte("null"), nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	b.WriteByte('{')
	for i, p := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := enc.Encode(p.k); err != nil {
			return nil, err
		}
		b.Truncate(b.Len() - 1) // Encode ends with a newline
		b.WriteByte(':')
		if err := enc.Encode(p.v); err != nil {
			return nil, err
		}
		b.Truncate(b.Len() - 1)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ---------------------------------------------------------------------------------------------------------------------
// Input and expect

type partySetup struct {
	StartMs       int64     `json:"startMs"`
	RelayOffsetMs int64     `json:"relayOffsetMs"`
	RttMs         int64     `json:"rttMs"`
	Random        []float64 `json:"random"`
	Base          *string   `json:"base"`
	List          obj       `json:"list"`
	Name          string    `json:"name"`
	Client        string    `json:"client"`
	Resolver      bool      `json:"resolver"`
}

type partyIn struct {
	Setup partySetup `json:"setup"`
	Steps []obj      `json:"steps"`
}

// out is what the party did in response to one step. An absent channel means nothing on it; state lists only the
// fields the step checks.
type out struct {
	Send     []obj `json:"send,omitempty"`
	Command  []obj `json:"command,omitempty"`
	Event    []obj `json:"event,omitempty"`
	Resolver []obj `json:"resolver,omitempty"`
	Net      []obj `json:"net,omitempty"`
	Done     []obj `json:"done,omitempty"`
	State    obj   `json:"state,omitempty"`
}

type partyOut struct {
	Outputs []out `json:"outputs"`
}

// setup is the usual one: Relays.at(http://relay.test), one relay at ws://relay.test/v1/ws that never fetches a list,
// relay time 1000000 ms ahead of local time 0, pongs at once, a resolver.
func setup() partySetup {
	return partySetup{
		RelayOffsetMs: trOffset,
		Random:        []float64{},
		Base:          new(trBase),
		Name:          trName,
		Client:        trClient,
		Resolver:      true,
	}
}

// trace collects steps with their outputs and keeps the local time the steps have reached.
type trace struct {
	name  string
	setup partySetup
	now   int64
	steps []obj
	outs  []out
}

func newTrace(name string, s partySetup) *trace {
	return &trace{name: name, setup: s, now: s.StartMs}
}

func (t *trace) do(step obj, res out) *trace {
	if step[0].k == "advance" {
		t.now += step[0].v.(int64)
	}
	t.steps = append(t.steps, step)
	t.outs = append(t.outs, res)
	return t
}

// relayNow is the relay time after the steps so far.
func (t *trace) relayNow() int64 { return t.now + t.setup.RelayOffsetMs }

func (t *trace) cas() Case {
	return Case{t.name, partyIn{t.setup, t.steps}, partyOut{t.outs}}
}

// ---------------------------------------------------------------------------------------------------------------------
// Steps

func call(api string, args ...any) obj { return append(o("call", api), o(args...)...) }
func recv(msg any) obj                 { return o("recv", msg) }
func advance(ms int64) obj             { return o("advance", ms) }
func drop(code any) obj                { return o("drop", code) }
func nextOpen(how string) obj          { return o("nextOpen", how) }
func nextGet(how any) obj              { return o("nextGet", how) }
func hold(player string) obj           { return o("hold", player) }
func finish(player string) obj         { return o("finish", player) }
func answer(cands ...obj) obj          { return o("answer", cands) }

// player sets an adapter's readable state, creating it on first use.
func player(id string, fields ...any) obj { return append(o("player", id), o(fields...)...) }

// probeAnswer answers candidate id's running probe with a duration, or nothing when durationMs is nil.
func probeAnswer(id string, durationMs any) obj {
	return o("probeAnswer", id, "durationMs", durationMs)
}

// ---------------------------------------------------------------------------------------------------------------------
// Relay messages

type peer struct {
	PeerID    string `json:"peerId"`
	Name      string `json:"name"`
	Client    string `json:"client"`
	Host      bool   `json:"host"`
	Connected bool   `json:"connected"`
	Player    string `json:"player"`
	Match     obj    `json:"match"`
	OffsetMs  int64  `json:"offsetMs"`
}

func newPeer(id, name string, host bool) peer {
	return peer{PeerID: id, Name: name, Client: trClient, Host: host, Connected: true, Player: "idle"}
}

// reported is p after a status with player and match.
func (p peer) reported(player string, match obj) peer {
	p.Player, p.Match = player, match
	return p
}

type snap struct {
	Type         string   `json:"type,omitempty"`
	Rev          int64    `json:"rev"`
	Code         string   `json:"code"`
	Epoch        int64    `json:"epoch"`
	Phase        string   `json:"phase"`
	PositionMs   int64    `json:"positionMs"`
	AtMs         int64    `json:"atMs"`
	Rate         float64  `json:"rate"`
	Control      string   `json:"control"`
	Locked       bool     `json:"locked"`
	WaitingOn    []string `json:"waitingOn"`
	Participants []peer   `json:"participants"`
	// Action and Media are a `room` message's own members (SPEC.md §8.2): absent unless set.
	Action obj `json:"action,omitempty"`
	Media  any `json:"media,omitempty"`
}

// newRoom is a new room (SPEC.md §9.3) created at relay time atMs with the given participants.
func newRoom(rev, atMs int64, peers ...peer) snap {
	return snap{Rev: rev, Code: trCode, Phase: "idle", AtMs: atMs, Rate: 1, Control: "host", WaitingOn: []string{},
		Participants: peers}
}

// msg is s as a `room` message.
func (s snap) msg() snap {
	s.Type = "room"
	return s
}

// acted is s carrying the action an event set (SPEC.md §9.1): kind by a peer ID, or by nil for the relay.
func (s snap) acted(kind string, by any) snap {
	s.Action = o("kind", kind, "by", by)
	return s
}

// withMedia is s carrying the media of the epoch it starts (SPEC.md §8.3): the descriptor, or null for nil.
func (s snap) withMedia(media *Media) snap {
	s.Media = json.RawMessage("null")
	if media != nil {
		s.Media = media
	}
	return s
}

// withPeer is s with participant i replaced by p; s itself is unchanged.
func (s snap) withPeer(i int, p peer) snap {
	s.Participants = slices.Clone(s.Participants)
	s.Participants[i] = p
	return s
}

func token(peerID string) string { return peerID + "-" + strings.Repeat("t", 42-len(peerID)) }

// joinedMsg answers create, join or resume for peerID with room and media (nil for none). joined.room carries
// neither action nor media.
func joinedMsg(peerID string, room snap, media *Media, serverTime int64) obj {
	if room.Action != nil || room.Media != nil {
		panic("joined.room carries no action or media")
	}
	var m any
	if media != nil {
		m = media
	}
	return o("type", "joined", "code", room.Code, "you", o("peerId", peerID, "token", token(peerID)), "room", room,
		"media", m, "serverTime", serverTime)
}

func heat() *Media {
	return &Media{Kind: "movie", Title: "Heat", Year: new(1995), Ids: map[string]string{"imdb": "tt0113277"},
		DurationMs: heatMs}
}

// ---------------------------------------------------------------------------------------------------------------------
// Outputs

func matchOf(state string, deltaMs ...int64) obj {
	if len(deltaMs) == 0 {
		return o("state", state)
	}
	return o("state", state, "deltaMs", deltaMs[0])
}

// status is the status message with the setup's round trip (SDK.md §10.1).
func (t *trace) status(epoch int64, player string, match obj) obj {
	return o("type", "status", "epoch", epoch, "player", player, "match", match, "offsetMs", 0, "rttMs", t.setup.RttMs)
}

func cmd(player, do string, arg ...any) obj {
	return append(o("player", player, "do", do), o(arg...)...)
}
func opened() obj                     { return o("open", trURL) }
func got() obj                        { return o("get", trPublic) }
func closed(code int64) obj           { return o("close", trURL, "code", code) }
func done(id string, more ...any) obj { return append(o("id", id), o(more...)...) }
func state(fields ...any) obj         { return o(fields...) }
func objs(items ...obj) []obj {
	if items == nil {
		return []obj{}
	}
	return items
}

func evAction(kind string, by any, byMe bool, rev int64) obj {
	return o("type", "action", "kind", kind, "byPeerId", by, "by", by, "byMe", byMe, "room", rev)
}
func evJoined(peerID string) obj         { return o("type", "participantJoined", "peerId", peerID) }
func evLeft(peerID string) obj           { return o("type", "participantLeft", "peerId", peerID) }
func evEnded(reason string, err any) obj { return o("type", "ended", "reason", reason, "error", err) }

func choice(id, ref string) obj            { return o("id", id, "ref", ref) }
func ranked(id string, deltaMs any) obj    { return o("id", id, "deltaMs", deltaMs) }
func candidate(id string, more ...any) obj { return append(o("id", id), o(more...)...) }

// ---------------------------------------------------------------------------------------------------------------------
// Prefixes

var (
	ann = newPeer("ann", trName, false)
	ben = newPeer("ben", "Ben", true)
	cat = newPeer("cat", "Cat", false)
)

// hostJoined creates a room on Relays.at's one relay and answers it: a new room with this party as host. One relay
// means connect and send create at once, no GET and no pong wait (SDK.md §3.1, §6); status follows joined (§7.3,
// §10.3). It returns the room.
func (t *trace) hostJoined() snap {
	return t.created("c1", opened())
}

// created creates a room with call id and net the create step's net outputs, then answers it as hostJoined does.
func (t *trace) created(id string, net ...obj) snap {
	host := ann
	host.Host = true
	room := newRoom(1, t.relayNow(), host)
	t.do(call("create", "id", id), out{
		Net:   objs(net...),
		Send:  objs(o("type", "create", "name", trName, "client", trClient)),
		State: state("connection", "connecting"),
	})
	t.do(recv(joinedMsg("ann", room, nil, t.relayNow())), out{
		Done:  objs(done(id, "value", trCode)),
		Send:  objs(t.status(0, "idle", nil)),
		State: state("connection", "connected", "peerId", "ann", "room", room.Rev, "isHost", true, "player", "idle"),
	})
	return room
}

// guestJoined joins with typed and answers with room and media. Other media starts the search (SDK.md §9.1) before
// the first status (§7.3).
func (t *trace) guestJoined(typed string, room snap, media *Media) {
	t.do(call("join", "id", "c1", "code", typed), out{
		Net:   objs(opened()),
		Send:  objs(o("type", "join", "code", trCode, "name", trName, "client", trClient)),
		State: state("connection", "connecting"),
	})
	res := out{Done: objs(done("c1")), State: state("connection", "connected", "peerId", "ann", "room", room.Rev)}
	if media == nil {
		res.Send = objs(t.status(0, "idle", nil))
	} else {
		res.Resolver = objs(o("resolve", media.Title))
		res.Send = objs(t.status(room.Epoch, "loading", matchOf("searching")))
		res.State = append(res.State, o("player", "loading", "match", matchOf("searching"))...)
	}
	t.do(recv(joinedMsg("ann", room, media, t.relayNow())), res)
}

// playingRoom is the room a guest joins at local 0 in the following traces: Heat playing at rate 1 from 600000 ms
// since relay time 990000, so the room position is 610000 + local time. Ben is the host; control is as given.
func playingRoom(control string) snap {
	r := newRoom(7, 990_000, ben.reported("ready", matchOf("matched", 0)), ann)
	r.Epoch, r.Phase, r.PositionMs, r.Control = 2, "playing", 600_000, control
	return r
}

// guestFollowing joins playingRoom with player p1 attached before the match, as an app that opens state.choice
// would. The resolver's one candidate is 500 ms longer than Heat: matched, the adapter binds (SDK.md §9.5, §10.2),
// the bind's setRate runs first, then the step seeks 250 ms ahead of the room and plays (§11.1, §11.2). The status
// waits for the 250 ms spacing after joined's and goes out with the values of that moment (§10.3). It ends at local
// 250 with p1 at 610500 playing and the room at 610250. playThrows makes p1 refuse to play.
func (t *trace) guestFollowing(control string, playThrows bool) snap {
	room := playingRoom(control)
	t.guestJoined(trCode, room, heat())
	t.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs+500, "canSetRate", true,
		"playThrows", playThrows), out{})
	t.do(call("attachPlayer", "player", "p1", "ref", "heat-a"), out{})
	res := out{
		Command: objs(cmd("p1", "setRate", "rate", 1), cmd("p1", "seek", "positionMs", 610_250), cmd("p1", "play")),
		State: state("match", matchOf("matched", 500), "choice", choice("heat-a", "heat-a"),
			"candidates", objs(ranked("heat-a", 500))),
	}
	reported := "ready"
	if playThrows {
		reported = "loading"
		res.Event = objs(o("type", "playBlocked"))
	}
	res.State = append(res.State, kv{"player", reported})
	t.do(answer(candidate("heat-a", "durationMs", heatMs+500)), res)
	t.do(advance(250), out{Send: objs(t.status(2, reported, matchOf("matched", 500)))})
	return room
}

// ---------------------------------------------------------------------------------------------------------------------
// The traces

func partyTable() Table {
	t := Table{Name: "party"}
	add := func(tr *trace) { t.Cases = append(t.Cases, tr.cas()) }

	// SDK.md §3.1, §6, §7.2, §7.3, §10.3: Relays.at is one relay and fetches nothing: create connects and sends create
	// at once; status follows joined; the heartbeat repeats it 15000 ms after the last one, not earlier.
	{
		tr := newTrace("create on Relays.at's one relay without a GET, status after joined, heartbeat 15000 ms later", setup())
		tr.hostJoined()
		tr.do(advance(14_999), out{})
		tr.do(advance(1), out{Send: objs(tr.status(0, "idle", nil))})
		add(tr)
	}

	// SDK.md §4.1, §7.2, §12, §15: join sends the normalized code; checks run member, media, then controller, and host
	// commands need the host; participant events come joined first, then left, then the action; a promotion makes
	// this party host.
	{
		tr := newTrace("join sends the normalized code; command checks; participant and host events in order", setup())
		room := newRoom(4, 990_000, ben, ann)
		tr.guestJoined("k7m-q2x", room, nil)
		tr.do(call("play", "id", "c2"), out{Done: objs(done("c2", "error", "invalid_message"))})
		tr.do(call("setControl", "id", "c3", "mode", "everyone"), out{Done: objs(done("c3", "error", "forbidden"))})
		room.Rev = 5
		room.Participants = []peer{ben, ann, cat}
		tr.do(recv(room.msg()), out{Event: objs(evJoined("cat")), State: state("room", 5)})
		host := ann
		host.Host = true
		room.Rev = 6
		room.Participants = []peer{host, cat}
		tr.do(recv(room.acted("host", nil).msg()), out{
			Event: objs(evLeft("ben"), evAction("host", nil, false, 6)),
			State: state("room", 6, "isHost", true, "canControl", true),
		})
		add(tr)
	}

	// SDK.md §8, §9.1, §10.2, §10.3, §11.1, §11.2, §12, §15: setMedia unbinds until its epoch arrives; the room that
	// starts it carries the media: the snapshot is replaced first, then own media is matched with choice "" and binds
	// at once with no resolver call and no pause, the bind setting the new room's rate; its status skips the spacing
	// and reports ready at once; then the action; a playing room with atMs ahead holds and the play timer plays at
	// atMs.
	{
		tr := newTrace("host: own media binds without the resolver, status per epoch, play at the scheduled start", setup())
		room := tr.hostJoined()
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(100), out{})
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{
			Send: objs(o("type", "setMedia", "media", heat(), "positionMs", 0)),
		})
		// SPEC.md §7.10: the relay's snapshot of setMedia, waiting on everyone; Ann's entry is still the old status.
		room.Rev, room.Epoch, room.Phase, room.AtMs = 2, 1, "waiting", tr.relayNow()
		room.WaitingOn = []string{"ann"}
		tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			Event:   objs(evAction("media", "ann", true, 2)),
			State: state("room", 2, "media", "Heat", "match", matchOf("matched", 0), "choice", choice("", "heat"),
				"player", "ready"),
		})
		// SPEC.md §12.6: Ann's ready status released the room, to play 750 ms from now.
		room.Rev, room.Phase, room.AtMs = 3, "playing", tr.relayNow()+750
		room.WaitingOn = []string{}
		room = room.withPeer(0, room.Participants[0].reported("ready", matchOf("matched", 0)))
		tr.do(recv(room.msg()), out{State: state("room", 3, "player", "ready")})
		tr.do(advance(749), out{})
		tr.do(advance(1), out{Command: objs(cmd("p1", "play"))})
		add(tr)
	}

	// SDK.md §10.2, §11.1, §11.2: an adapter attached before the match binds when the choice is published; the step
	// seeks to the room position + 250 and plays; drift 600 ms after playerChanged() nudges at 0.96; the nudge ends
	// with the room's rate once the drift is under 150 ms (at local 12000: 140 ms).
	{
		tr := newTrace("guest: binds on the match, seeks with the lead and plays, nudges at 0.96 until under 150 ms", setup())
		tr.guestFollowing("host", false)
		tr.do(advance(250), out{State: state("roomPositionMs", 610_500)})
		tr.do(player("p1", "positionMs", 611_100), out{})
		tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
		tr.do(advance(11_499), out{})
		tr.do(advance(1), out{Command: objs(cmd("p1", "setRate", "rate", 1))})
		add(tr)
	}

	// SDK.md §8, §12, §15: with control everyone a guest is a controller; pause pauses the bound adapter and sends
	// its position; no step runs during the hold, also on a tick or a room without action; the room carrying the
	// pause's action ends the hold and the paused room holds the player where it stopped.
	{
		tr := newTrace("a controller's pause pauses the player and holds steps until a room carrying an action", setup())
		room := tr.guestFollowing("everyone", false)
		tr.do(call("pause"), out{
			Command: objs(cmd("p1", "pause")),
			Send:    objs(o("type", "pause", "positionMs", 610_500)),
		})
		tr.do(advance(250), out{})
		room.Rev = 8
		room = room.withPeer(1, ann.reported("ready", matchOf("matched", 500)))
		tr.do(recv(room.msg()), out{State: state("room", 8)})
		room.Rev, room.Phase, room.PositionMs = 9, "paused", 610_500
		tr.do(recv(room.acted("pause", "ann").msg()), out{
			Event: objs(evAction("pause", "ann", true, 9)),
			State: state("room", 9, "player", "ready"),
		})
		tr.do(advance(2000), out{})
		add(tr)
	}

	// SDK.md §10.2 rule 8, §11.2, §15: a step's play that throws while the adapter reads paused blocks the player:
	// loading, and PlayBlocked once; the next step plays again without another event; the adapter reading not paused
	// unblocks it, so the next tick reports ready.
	{
		tr := newTrace("a refused play reports loading and emits PlayBlocked once; reading not paused makes it ready", setup())
		tr.guestFollowing("host", true)
		tr.do(advance(250), out{Command: objs(cmd("p1", "play"))})
		tr.do(player("p1", "paused", false, "playThrows", false), out{})
		tr.do(advance(500), out{Send: objs(tr.status(2, "ready", matchOf("matched", 500)))})
		add(tr)
	}

	// SDK.md §10.2 rule 5, §10.3, §11.1: a seek the SDK issued makes the player loading while it runs; a step
	// requested meanwhile, by playerChanged() or the tick, waits for the lane and runs once when the seek finishes.
	{
		tr := newTrace("a step requested while a command runs happens when it finishes, not at the next tick", setup())
		tr.guestFollowing("host", false)
		tr.do(advance(2000), out{})
		tr.do(hold("p1"), out{})
		tr.do(player("p1", "positionMs", 618_000), out{})
		tr.do(call("playerChanged"), out{
			Command: objs(cmd("p1", "seek", "positionMs", 612_500)),
			Send:    objs(tr.status(2, "loading", matchOf("matched", 500))),
			State:   state("player", "loading"),
		})
		tr.do(player("p1", "paused", true), out{})
		tr.do(call("playerChanged"), out{})
		tr.do(advance(500), out{})
		tr.do(finish("p1"), out{
			Command: objs(cmd("p1", "play")),
			Send:    objs(tr.status(2, "ready", matchOf("matched", 500))),
			State:   state("player", "ready"),
		})
		add(tr)
	}

	// SDK.md §10.2, §11.2, §12: a seek queued behind a running command makes the player loading at once (rule 5);
	// detachPlayer releases the nudged adapter: the queued local seek is dropped, the running pause finishes, then one
	// setRate of the room's rate; the completion settles after it; away reports idle once the 250 ms spacing is up;
	// nothing else reaches the adapter, also when the hold ends.
	{
		tr := newTrace("detachPlayer drops queued commands, restores the rate once after the running one, then settles", setup())
		tr.guestFollowing("everyone", false)
		tr.do(player("p1", "positionMs", 610_850), out{})
		tr.do(call("playerChanged"), out{Command: objs(cmd("p1", "setRate", "rate", 0.96))})
		tr.do(hold("p1"), out{})
		tr.do(call("pause"), out{
			Command: objs(cmd("p1", "pause")),
			Send:    objs(o("type", "pause", "positionMs", 610_850)),
		})
		tr.do(call("seek", "ms", 700_000), out{
			Send:  objs(o("type", "seek", "positionMs", 700_000)),
			State: state("player", "loading"),
		})
		tr.do(call("detachPlayer", "id", "c2"), out{State: state("player", "idle")})
		tr.do(finish("p1"), out{Command: objs(cmd("p1", "setRate", "rate", 1)), Done: objs(done("c2"))})
		tr.do(advance(2250), out{Send: objs(tr.status(2, "idle", matchOf("matched", 500)))})
		add(tr)
	}

	// SDK.md §8, §10.2, §14: a drop starts attempt 1 after 500 ms × u (u = 0.5); it resumes at the URL joined on; the
	// joined with the same epoch is followed by status, then the pending setMedia again; the room starting its epoch
	// binds the adapter, and that status skips the spacing.
	{
		s := setup()
		s.Random = []float64{0}
		tr := newTrace("a setMedia lost on a drop is sent again after the same-epoch resume's status and binds on its media", s)
		room := tr.hostJoined()
		tr.do(player("p1", "positionMs", 0, "paused", true, "durationMs", heatMs, "canSetRate", true), out{})
		tr.do(call("attachPlayer", "player", "p1", "ref", "heat"), out{})
		tr.do(advance(1000), out{})
		setMedia := o("type", "setMedia", "media", heat(), "positionMs", 0)
		tr.do(call("setMedia", "media", heat(), "ref", "heat"), out{Send: objs(setMedia)})
		tr.do(drop(1006), out{State: state("connection", "reconnecting")})
		tr.do(advance(249), out{})
		tr.do(advance(1), out{
			Net:  objs(opened()),
			Send: objs(o("type", "resume", "code", trCode, "peerId", "ann", "token", token("ann"))),
		})
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{
			Send:  objs(tr.status(0, "idle", nil), setMedia),
			State: state("connection", "connected", "room", 2),
		})
		room.Rev, room.Epoch, room.Phase, room.AtMs = 3, 1, "waiting", tr.relayNow()
		room.WaitingOn = []string{"ann"}
		tr.do(recv(room.withMedia(heat()).acted("media", "ann").msg()), out{
			Command: objs(cmd("p1", "setRate", "rate", 1)),
			Send:    objs(tr.status(1, "ready", matchOf("matched", 0))),
			Event:   objs(evAction("media", "ann", true, 3)),
			State:   state("match", matchOf("matched", 0), "choice", choice("", "heat"), "player", "ready"),
		})
		add(tr)
	}

	// SDK.md §9.4, §9.5, §9.6: a counting durationMs ranks before probes and is kept as the closest; probes run one at
	// a time in ranked order; a probe that returns nothing skips its candidate; the next probe within 1500 ms matches.
	{
		tr := newTrace("the walk ranks counting durations first, skips a failed probe and matches the next", setup())
		room := newRoom(3, 990_000, ben.reported("ready", matchOf("matched", 0)), ann)
		room.Epoch, room.Phase = 1, "paused"
		tr.guestJoined(trCode, room, heat())
		tr.do(answer(candidate("a", "probe", true), candidate("b", "durationMs", heatMs+4000), candidate("c", "probe", true)), out{
			Resolver: objs(o("probe", "a")),
			State:    state("candidates", objs(ranked("b", 4000), ranked("a", nil), ranked("c", nil)), "match", matchOf("searching")),
		})
		tr.do(probeAnswer("a", nil), out{Resolver: objs(o("probe", "c"))})
		tr.do(advance(1000), out{})
		tr.do(probeAnswer("c", heatMs-300), out{
			Send: objs(tr.status(1, "loading", matchOf("matched", -300))),
			State: state("match", matchOf("matched", -300), "choice", choice("c", "c"),
				"candidates", objs(ranked("b", 4000), ranked("a", nil), ranked("c", -300))),
		})
		add(tr)
	}

	// SDK.md §2, §9.2, §9.6, §10.2, §10.3: the resolve timeout is 30000 ms; when it passes the SDK cancels the call and
	// emits ResolveFailed; no candidates make the match unavailable and the player idle, which the next status
	// reports. The offset set at local 1000 moves the heartbeats to 16000 and 31000, clear of the timeout.
	{
		tr := newTrace("a resolver slower than 30000 ms is cancelled, emits ResolveFailed and leaves the match unavailable", setup())
		room := newRoom(3, 990_000, ben.reported("ready", matchOf("matched", 0)), ann)
		room.Epoch, room.Phase = 1, "paused"
		tr.guestJoined(trCode, room, heat())
		status := func(player string, match obj) obj {
			return o("type", "status", "epoch", 1, "player", player, "match", match, "offsetMs", 100, "rttMs", 0)
		}
		tr.do(advance(1000), out{})
		tr.do(call("setOffset", "ms", 100), out{Send: objs(status("loading", matchOf("searching")))})
		tr.do(advance(15_000), out{Send: objs(status("loading", matchOf("searching")))})
		tr.do(advance(13_999), out{})
		tr.do(advance(1), out{
			Resolver: objs(o("resolveCancelled", "Heat")),
			Event:    objs(o("type", "resolveFailed", "timeout", true)),
			Send:     objs(status("idle", matchOf("unavailable"))),
			State:    state("match", matchOf("unavailable"), "candidates", objs(), "player", "idle"),
		})
		add(tr)
	}

	// SDK.md §7.4, §12: closed with reason ended ends the party: it closes the connection with 1000, publishes ended
	// and emits Ended; commands then throw not_in_room and no timer sends anything.
	{
		tr := newTrace("closed ended ends the party, closes with 1000 and stops every timer", setup())
		tr.hostJoined()
		tr.do(advance(1000), out{})
		tr.do(call("end"), out{Send: objs(o("type", "end"))})
		tr.do(recv(o("type", "closed", "reason", "ended")), out{
			Net:   objs(closed(1000)),
			Event: objs(evEnded("ended", nil)),
			State: state("connection", "ended", "endReason", "ended", "peerId", nil, "room", nil, "isHost", false),
		})
		tr.do(call("play", "id", "c2"), out{Done: objs(done("c2", "error", "not_in_room"))})
		tr.do(advance(20_000), out{})
		add(tr)
	}

	// SDK.md §14: attempt n waits min(8000, 500 × 2^(n−1)) × u from the failure before it, u = 0.5 + 0.5 × the
	// random draw: 250 ms after the drop (draw 0), then a refused connection, then 750 ms (draw 0.5).
	{
		s := setup()
		s.Random = []float64{0, 0.5}
		tr := newTrace("a failed reconnect attempt is retried after the doubled backoff times the random factor", s)
		room := tr.hostJoined()
		tr.do(drop(nil), out{State: state("connection", "reconnecting")})
		tr.do(nextOpen("fail"), out{})
		tr.do(advance(250), out{Net: objs(opened())})
		tr.do(advance(749), out{})
		tr.do(advance(1), out{
			Net:  objs(opened()),
			Send: objs(o("type", "resume", "code", trCode, "peerId", "ann", "token", token("ann"))),
		})
		room.Rev = 2
		tr.do(recv(joinedMsg("ann", room, nil, tr.relayNow())), out{
			Send:  objs(tr.status(0, "idle", nil)),
			State: state("connection", "connected", "room", 2),
		})
		add(tr)
	}

	pickTraces(add)
	t.Cases = append(t.Cases, partyMatchTraces()...)
	t.Cases = append(t.Cases, partyResumeTraces()...)
	t.Cases = append(t.Cases, partyFollowTraces()...)
	t.Cases = append(t.Cases, partyLifeTraces()...)
	t.Cases = append(t.Cases, partyGapTraces()...)

	if err := checkTraces(t.Cases); err != nil {
		panic(err)
	}
	return t
}
