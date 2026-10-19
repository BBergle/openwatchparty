package main

// The reference implementation of the client rules in spec/SDK.md. Every table in conformance/vectors/ is generated
// from these functions. Floating-point expressions convert each intermediate result with float64(...) so the Go
// compiler can't fuse a multiply and an add: SDKs compute in plain IEEE-754 doubles, one rounding per operation.

import (
	"errors"
	"math"
	"slices"
	"strings"
)

// Alphabet holds the 31 room-code symbols.
const Alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// SDK constants (spec/SDK.md).
const (
	clockWindow      = 8
	clockMaxRttMs    = 1000
	clockStepSlackMs = 100

	driftDeadbandMs  = 350
	driftNudgeExitMs = 150
	driftSeekMs      = 2000
	driftSeekOnlyMs  = 1000
	nudgeFactor      = 0.04
	seekLeadMs       = 250
	seekCooldownMs   = 2000
	heldSeekMs       = 500
	endMarginMs      = 1000
	rateEpsilon      = 0.001
	matchToleranceMs = 1500
	maxProbes        = 4
	probeBudgetMs    = 60000
)

// ---------------------------------------------------------------------------------------------------------------------
// Timeline

// Room holds the snapshot fields the timeline reads.
type Room struct {
	Phase      string  `json:"phase"`
	PositionMs int64   `json:"positionMs"`
	AtMs       int64   `json:"atMs"`
	Rate       float64 `json:"rate"`
}

// Position is the room position at relay time t.
func Position(r Room, durationMs, t int64) int64 {
	if r.Phase != "playing" {
		return r.PositionMs
	}
	elapsed := max(0, t-r.AtMs)
	moved := float64(float64(elapsed) * r.Rate)
	p := int64(math.Floor(float64(float64(r.PositionMs) + moved)))
	return min(p, durationMs)
}

// Running reports whether the room's position moves at relay time t.
func Running(r Room, t int64) bool {
	return r.Phase == "playing" && t >= r.AtMs
}

// ---------------------------------------------------------------------------------------------------------------------
// Clock sync

type clockSample struct{ offset, rtt int64 }

// Clock estimates the relay's clock from ping/pong pairs.
type Clock struct {
	pending map[int64]int64
	samples []clockSample
}

func NewClock() *Clock { return &Clock{pending: map[int64]int64{}} }

func (c *Clock) Ping(id, atMs int64) { c.pending[id] = atMs }

// Pong feeds an answer and reports what happened to it: "accepted", "stepped" (the window was cleared first, then
// the sample kept) or "ignored" (an unknown ping, or a round trip outside 0..1000 ms).
func (c *Clock) Pong(id, serverTime, atMs int64) string {
	sentAt, ok := c.pending[id]
	if !ok {
		return "ignored"
	}
	delete(c.pending, id)
	rtt := atMs - sentAt
	if rtt < 0 || rtt > clockMaxRttMs {
		return "ignored"
	}
	offset := serverTime - (sentAt + rtt/2)
	result := "accepted"
	if best, ok := c.Best(); ok {
		tolerance := (rtt+best.rtt)/2 + clockStepSlackMs
		if abs(offset-best.offset) > tolerance {
			c.samples = c.samples[:0]
			result = "stepped"
		}
	}
	c.samples = append(c.samples, clockSample{offset, rtt})
	if len(c.samples) > clockWindow {
		c.samples = c.samples[1:]
	}
	return result
}

// Best is the sample with the lowest round trip; the oldest wins a tie.
func (c *Clock) Best() (clockSample, bool) {
	if len(c.samples) == 0 {
		return clockSample{}, false
	}
	best := c.samples[0]
	for _, s := range c.samples[1:] {
		if s.rtt < best.rtt {
			best = s
		}
	}
	return best, true
}

// ---------------------------------------------------------------------------------------------------------------------
// Reconciliation

// Player is what the reconciliation step reads from the local player. SeekAccuracyMs is how far from the requested
// position a seek may land (an int ≥ 0; 0 when absent).
type Player struct {
	PositionMs     int64   `json:"positionMs"`
	Paused         bool    `json:"paused"`
	Buffering      bool    `json:"buffering"`
	DurationMs     *int64  `json:"durationMs"`
	CanSetRate     bool    `json:"canSetRate"`
	Rate           float64 `json:"rate"`
	SeekAccuracyMs int64   `json:"seekAccuracyMs,omitempty"`
}

// ReconcileIn is one step's input.
type ReconcileIn struct {
	Room        Room   `json:"room"`
	DurationMs  int64  `json:"durationMs"`
	ServerNowMs int64  `json:"serverNowMs"`
	OffsetMs    int64  `json:"offsetMs"`
	Player      Player `json:"player"`
	Nudging     bool   `json:"nudging"`
	SinceSeekMs *int64 `json:"sinceSeekMs"`
}

type Action struct {
	Do         string   `json:"do"`
	PositionMs *int64   `json:"positionMs,omitempty"`
	Rate       *float64 `json:"rate,omitempty"`
}

type ReconcileOut struct {
	Actions  []Action `json:"actions"`
	Nudging  bool     `json:"nudging"`
	PlayInMs *int64   `json:"playInMs"`
}

// Reconcile is one reconciliation step. Actions come in the order pause, setRate, seek, play. Local position is room
// position + offsetMs; every seek threshold is at least the player's seek accuracy.
func Reconcile(in ReconcileIn) ReconcileOut {
	out := ReconcileOut{Actions: []Action{}}
	r, p := in.Room, in.Player
	if r.Phase == "idle" {
		return out
	}
	cooled := in.SinceSeekMs == nil || *in.SinceSeekMs >= seekCooldownMs
	rate := r.Rate
	accuracy := p.SeekAccuracyMs
	hasEnd := p.DurationMs != nil && *p.DurationMs > 0
	var endLimit int64
	if hasEnd {
		endLimit = max(0, *p.DurationMs-endMarginMs)
	}
	clampLocal := func(x int64) int64 {
		x = max(0, x)
		if hasEnd {
			x = min(x, endLimit)
		}
		return x
	}
	setRate := func(rate float64) {
		if p.CanSetRate && math.Abs(float64(p.Rate-rate)) > rateEpsilon {
			out.Actions = append(out.Actions, Action{Do: "setRate", Rate: new(round6(rate))})
		}
	}
	seek := func(x int64) {
		out.Actions = append(out.Actions, Action{Do: "seek", PositionMs: new(x)})
	}

	anchorLocal := r.PositionMs + in.OffsetMs
	hold := r.Phase != "playing"
	if r.Phase == "playing" {
		lead := 0.0
		if anchorLocal < 0 {
			lead = float64(float64(-anchorLocal) / rate)
		}
		startAt := float64(float64(r.AtMs) + lead)
		if float64(in.ServerNowMs) < startAt {
			hold = true
			out.PlayInMs = new(int64(math.Ceil(float64(startAt - float64(in.ServerNowMs)))))
		}
	}

	if hold {
		holdAt := clampLocal(anchorLocal)
		if !p.Paused {
			out.Actions = append(out.Actions, Action{Do: "pause"})
		}
		setRate(rate)
		if abs(p.PositionMs-holdAt) > max(heldSeekMs, accuracy) && cooled {
			seek(holdAt)
		}
		return out
	}

	target := max(0, Position(r, in.DurationMs, in.ServerNowMs)+in.OffsetMs)
	nudging := in.Nudging && p.CanSetRate
	if hasEnd && target >= endLimit && p.PositionMs >= endLimit {
		setRate(rate)
		return out
	}
	if p.Buffering {
		out.Nudging = nudging
		return out
	}
	drift := p.PositionMs - target
	size := abs(drift)
	doSeek := false
	if p.CanSetRate {
		switch {
		case size > max(driftSeekMs, accuracy):
			nudging = false
			doSeek = cooled
		case nudging:
			if size < driftNudgeExitMs {
				nudging = false
			}
		case size > driftDeadbandMs:
			nudging = true
		}
	} else if size > max(driftSeekOnlyMs, accuracy) {
		doSeek = cooled
	}
	want := rate
	if nudging {
		if drift > 0 {
			want = float64(rate * (1 - nudgeFactor))
		} else {
			want = float64(rate * (1 + nudgeFactor))
		}
	}
	setRate(want)
	if doSeek {
		seek(clampLocal(target + seekLeadMs))
	}
	if p.Paused {
		out.Actions = append(out.Actions, Action{Do: "play"})
	}
	out.Nudging = nudging
	return out
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// ---------------------------------------------------------------------------------------------------------------------
// Media, same title, matching

type File struct {
	SizeBytes *int64 `json:"sizeBytes,omitempty"`
	Oshash    string `json:"oshash,omitempty"`
	Name      string `json:"name,omitempty"`
}

type Series struct {
	Title string            `json:"title"`
	Year  *int              `json:"year,omitempty"`
	Ids   map[string]string `json:"ids,omitempty"`
}

type Media struct {
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Year       *int              `json:"year,omitempty"`
	Ids        map[string]string `json:"ids,omitempty"`
	Series     *Series           `json:"series,omitempty"`
	Season     *int              `json:"season,omitempty"`
	Episode    *int              `json:"episode,omitempty"`
	DurationMs int64             `json:"durationMs"`
	File       *File             `json:"file,omitempty"`
}

// Namespaces are the v1 `ids` namespaces. IDs in any other namespace don't count for matching.
var Namespaces = []string{"imdb", "tmdb", "tvdb", "plex", "anilist", "anidb", "kitsu", "mal"}

func sharesID(a, b map[string]string) bool {
	for _, ns := range Namespaces {
		if a[ns] != "" && a[ns] == b[ns] {
			return true
		}
	}
	return false
}

// differsID reports whether some namespace holds different non-empty values on the two sides.
func differsID(a, b map[string]string) bool {
	for _, ns := range Namespaces {
		if a[ns] != "" && b[ns] != "" && a[ns] != b[ns] {
			return true
		}
	}
	return false
}

// foldEqual compares two non-empty strings with ASCII letters folded to lower case.
func foldEqual(a, b string) bool {
	return a != "" && b != "" && asciiLower(a) == asciiLower(b)
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func sameSize(a, b *int64) bool { return a != nil && b != nil && *a == *b }

// sameFile is the `video` identity rule: equal oshash, or equal size and name.
func sameFile(a, b *File) bool {
	if a == nil || b == nil {
		return false
	}
	return foldEqual(a.Oshash, b.Oshash) || (sameSize(a.SizeBytes, b.SizeBytes) && foldEqual(a.Name, b.Name))
}

func sameInt(a, b *int) bool { return a != nil && b != nil && *a == *b }

// SameTitle reports whether two descriptors name the same title.
func SameTitle(a, b Media) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "movie":
		return sharesID(a.Ids, b.Ids)
	case "episode":
		if sharesID(a.Ids, b.Ids) {
			return true
		}
		// Differing episode IDs beat equal numbers: the two sides may number the series differently.
		return !differsID(a.Ids, b.Ids) && a.Series != nil && b.Series != nil && sharesID(a.Series.Ids, b.Series.Ids) &&
			sameInt(a.Season, b.Season) && sameInt(a.Episode, b.Episode)
	case "video":
		return sameFile(a.File, b.File)
	}
	return false
}

type Probe struct {
	DurationMs *int64 `json:"durationMs"`
	TakesMs    int64  `json:"takesMs"`
}

type Candidate struct {
	ID         string `json:"id"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	SizeBytes  *int64 `json:"sizeBytes,omitempty"`
	Oshash     string `json:"oshash,omitempty"`
	Name       string `json:"name,omitempty"`
	Probe      *Probe `json:"probe,omitempty"`
}

type MatchIn struct {
	Media      Media       `json:"media"`
	Candidates []Candidate `json:"candidates"`
}

type MatchOut struct {
	Order      []string    `json:"order"`
	Candidates []RankedOut `json:"candidates"`
	Probed     []string    `json:"probed"`
	Result     string      `json:"result"`
	Choice     *string     `json:"choice"`
	DeltaMs    *int64      `json:"deltaMs"`
}

// RankedOut is one entry of the published `candidates` after the walk: its delta once known, else null.
type RankedOut struct {
	ID      string `json:"id"`
	DeltaMs *int64 `json:"deltaMs"`
}

// Rank orders candidates, for every kind: equal oshash, equal size, equal name, known duration by smallest difference,
// unknown duration; ties keep the resolver's order. A duration outside 1..maxDurationMs, from durationMs or a probe, is
// unknown in the returned candidates.
func Rank(m Media, cs []Candidate) []Candidate {
	file := m.File
	if file == nil {
		file = &File{}
	}
	var kept []Candidate
	for _, c := range cs {
		c.DurationMs = knownDuration(c.DurationMs)
		if c.Probe != nil {
			c.Probe = &Probe{DurationMs: knownDuration(c.Probe.DurationMs), TakesMs: c.Probe.TakesMs}
		}
		kept = append(kept, c)
	}
	bit := func(b bool) int64 {
		if b {
			return 0
		}
		return 1
	}
	key := func(c Candidate) [5]int64 {
		k := [5]int64{
			bit(foldEqual(c.Oshash, file.Oshash)),
			bit(sameSize(c.SizeBytes, file.SizeBytes)),
			bit(foldEqual(c.Name, file.Name)),
			bit(c.DurationMs != nil),
		}
		if c.DurationMs != nil {
			k[4] = abs(*c.DurationMs - m.DurationMs)
		}
		return k
	}
	slices.SortStableFunc(kept, func(a, b Candidate) int {
		ka, kb := key(a), key(b)
		return slices.Compare(ka[:], kb[:])
	})
	return kept
}

// knownDuration returns d when it is a valid media duration (1..maxDurationMs, SPEC.md §13.1), else nil: the app's
// candidates and probes can't push an out-of-range delta onto the wire.
func knownDuration(d *int64) *int64 {
	if d == nil || *d < 1 || *d > maxDurationMs {
		return nil
	}
	return d
}

// Match runs the matching walk over the ranked candidates. A candidate with durationMs has its delta from the start;
// a probed one gets it when its probe answers in time with a duration.
func Match(in MatchIn) MatchOut {
	out := MatchOut{Order: []string{}, Candidates: []RankedOut{}, Probed: []string{}, Result: "unavailable"}
	ranked := Rank(in.Media, in.Candidates)
	for _, c := range ranked {
		out.Order = append(out.Order, c.ID)
		r := RankedOut{ID: c.ID}
		if c.DurationMs != nil {
			r.DeltaMs = new(*c.DurationMs - in.Media.DurationMs)
		}
		out.Candidates = append(out.Candidates, r)
	}
	probes, spent := 0, int64(0)
	budgetGone := false
	var closest *Candidate
	var closestDelta int64
	for i, c := range ranked {
		var d *int64
		switch {
		case c.DurationMs != nil:
			d = c.DurationMs
		case c.Probe != nil:
			if probes == maxProbes || budgetGone {
				continue
			}
			// The deadline is inclusive: a probe that answers with exactly the budget left is in time.
			if spent+c.Probe.TakesMs > probeBudgetMs {
				budgetGone = true
				continue
			}
			spent += c.Probe.TakesMs
			probes++
			out.Probed = append(out.Probed, c.ID)
			d = c.Probe.DurationMs
			if d != nil {
				out.Candidates[i].DeltaMs = new(*d - in.Media.DurationMs)
			}
		}
		if d == nil {
			continue
		}
		delta := *d - in.Media.DurationMs
		if abs(delta) <= matchToleranceMs {
			out.Result, out.Choice, out.DeltaMs = "matched", new(c.ID), new(delta)
			return out
		}
		if closest == nil || abs(delta) < abs(closestDelta) {
			closest, closestDelta = new(c), delta
		}
	}
	if closest != nil {
		out.Result, out.Choice, out.DeltaMs = "mismatch", new(closest.ID), new(closestDelta)
	}
	return out
}

// ---------------------------------------------------------------------------------------------------------------------
// Codes and routing

// codeSeparators are dropped from typed or pasted codes: spaces, line breaks, tabs, no-break spaces, hyphens, dashes
// and the minus sign.
var codeSeparators = []rune{
	' ', '\t', '\n', '\r', '\u00a0', '\u2007', '\u202f',
	'-', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2212',
}

var errInvalidCode = errors.New("invalid_code")

// NormalizeCode strips separators, upper-cases ASCII letters and checks the result is 6 alphabet symbols.
func NormalizeCode(input string) (string, error) {
	var b strings.Builder
	n := 0
	for _, r := range input {
		if slices.Contains(codeSeparators, r) {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if !strings.ContainsRune(Alphabet, r) {
			return "", errInvalidCode
		}
		b.WriteRune(r)
		n++
	}
	if n != 6 {
		return "", errInvalidCode
	}
	return b.String(), nil
}

type RouteRelay struct {
	ID       string `json:"id"`
	Prefixes string `json:"prefixes"`
}

// Route names the relay that owns the code's first symbol, or "" when none does.
func Route(code string, relays []RouteRelay) string {
	for _, r := range relays {
		if strings.IndexByte(r.Prefixes, code[0]) >= 0 {
			return r.ID
		}
	}
	return ""
}

// ---------------------------------------------------------------------------------------------------------------------
// Relay list

type ListedRelay struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Prefixes string `json:"prefixes"`
}

var errInvalidList = errors.New("invalid_list")

// ParseRelayList reads the `relays` array of a public relay list document (the embedded copy or a refresh's answer)
// and checks it against spec/SDK.md §3.2.
func ParseRelayList(raw []byte) ([]ListedRelay, error) {
	obj, ok := decode(raw).(map[string]any)
	if !ok {
		return nil, errInvalidList
	}
	items, ok := obj["relays"].([]any)
	if !ok || len(items) == 0 {
		return nil, errInvalidList
	}
	ids := map[string]bool{}
	owned := map[rune]bool{}
	var out []ListedRelay
	for _, item := range items {
		r, ok := item.(map[string]any)
		if !ok {
			return nil, errInvalidList
		}
		id, ok := r["id"].(string)
		if !ok || !validRelayID(id) || ids[id] {
			return nil, errInvalidList
		}
		ids[id] = true
		name, ok := r["name"].(string)
		if !ok || !validRelayName(name) {
			return nil, errInvalidList
		}
		url, ok := r["url"].(string)
		if !ok || !validRelayURL(url) {
			return nil, errInvalidList
		}
		prefixes, ok := r["prefixes"].(string)
		if !ok || prefixes == "" {
			return nil, errInvalidList
		}
		for _, c := range prefixes {
			if !strings.ContainsRune(Alphabet, c) || owned[c] {
				return nil, errInvalidList
			}
			owned[c] = true
		}
		out = append(out, ListedRelay{ID: id, Name: name, URL: url, Prefixes: prefixes})
	}
	return out, nil
}

// validRelayID: 1–32 characters of a–z, 0–9 and -, starting with a letter or digit.
func validRelayID(id string) bool {
	if len(id) == 0 || len(id) > 32 || id[0] == '-' {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// validRelayName: 1–64 code points, no forbidden characters.
func validRelayName(name string) bool { return ValidText(name, 1, 64) }

// Forbidden reports the forbidden characters of SPEC.md §2: general category Cc and the Bidi_Control characters.
func Forbidden(c rune) bool {
	switch {
	case c <= 0x1f, c >= 0x7f && c <= 0x9f:
		return true
	case c == 0x061c, c == 0x200e, c == 0x200f, c >= 0x202a && c <= 0x202e, c >= 0x2066 && c <= 0x2069:
		return true
	}
	return false
}

// ValidText reports whether s has min to max code points, no forbidden characters and no unpaired surrogates.
func ValidText(s string, minLen, maxLen int) bool {
	n := 0
	for _, c := range s {
		if Forbidden(c) || c == unpaired {
			return false
		}
		n++
	}
	return n >= minLen && n <= maxLen
}

// ValidName checks a participant's `name` (SPEC.md §6.3): 1–32 code points, no forbidden characters.
func ValidName(name string) bool { return ValidText(name, 1, 32) }

// ValidClient checks `client` (SPEC.md §6.3): 1–64 code points, no forbidden characters, `<app>/<version>` with both
// parts non-empty; `<app>` ends at the first `/`.
func ValidClient(client string) bool {
	app, version, ok := strings.Cut(client, "/")
	return ValidText(client, 1, 64) && ok && app != "" && version != ""
}

// validRelayURL checks a public relay's `url` (spec/SDK.md §3.2): `wss://`, a non-empty host, a path ending in
// `/v1/ws`, and only printable ASCII other than ? and #.
func validRelayURL(url string) bool {
	rest, ok := strings.CutPrefix(url, "wss://")
	if !ok || !strings.HasSuffix(rest, "/v1/ws") {
		return false
	}
	for _, c := range []byte(rest) {
		if c < 0x21 || c > 0x7e || c == '?' || c == '#' {
			return false
		}
	}
	host, _, _ := strings.Cut(rest, "/")
	return host != ""
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
