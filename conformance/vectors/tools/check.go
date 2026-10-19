package main

// The checks an SDK runs on JSON from the relay and on descriptors it sends: spec/SDK.md §8 (snapshots) and §9.7
// (descriptors).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------------------------------------------------
// Decoding JSON as the SDKs do

// unpaired marks an unpaired surrogate in a decoded string. Go's decoder turns an unpaired surrogate escape ("\uD800")
// into U+FFFD, while the Kotlin, Dart and TypeScript decoders keep the unpaired surrogate in the string. decode
// rewrites each such escape to this noncharacter first, so a decoded string holds U+FDD0 exactly where an SDK's string
// holds an unpaired surrogate. No vector input contains U+FDD0 itself.
const unpaired = '\uFDD0'

var fdd0Escape = regexp.MustCompile(`(?i)\\ufdd0`)

// decode parses JSON text the way the SDKs see it: numbers keep their text and unpaired surrogates are marked.
func decode(raw []byte) any {
	if bytes.ContainsRune(raw, unpaired) || fdd0Escape.Match(raw) {
		panic(fmt.Sprintf("vector input holds U+FDD0, which marks unpaired surrogates: %s", raw))
	}
	d := json.NewDecoder(bytes.NewReader(markSurrogates(raw)))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		panic(fmt.Sprintf("vector input is not JSON: %v: %s", err, raw))
	}
	return v
}

// markSurrogates rewrites every \uXXXX escape in D800-DFFF that isn't a high surrogate directly followed by a low
// surrogate escape to \uFDD0. raw must be valid JSON.
func markSurrogates(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case !inString:
			inString = c == '"'
			out = append(out, c)
		case c == '"':
			inString = false
			out = append(out, c)
		case c == '\\' && raw[i+1] == 'u':
			v := hex4(raw[i+2 : i+6])
			switch {
			case v >= 0xd800 && v <= 0xdbff && i+12 <= len(raw) && raw[i+6] == '\\' && raw[i+7] == 'u' &&
				hex4(raw[i+8:i+12]) >= 0xdc00 && hex4(raw[i+8:i+12]) <= 0xdfff:
				out = append(out, raw[i:i+12]...)
				i += 11
			case v >= 0xd800 && v <= 0xdfff:
				out = append(out, `\ufdd0`...)
				i += 5
			default:
				out = append(out, raw[i:i+6]...)
				i += 5
			}
		case c == '\\':
			out = append(out, raw[i:i+2]...)
			i++
		default:
			out = append(out, c)
		}
	}
	return out
}

func hex4(b []byte) uint64 {
	v, err := strconv.ParseUint(string(b), 16, 16)
	if err != nil {
		return math.MaxUint64
	}
	return v
}

// holdsUnpaired reports whether any string in v, member names included, holds an unpaired surrogate.
func holdsUnpaired(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.ContainsRune(v, unpaired)
	case []any:
		for _, x := range v {
			if holdsUnpaired(x) {
				return true
			}
		}
	case map[string]any:
		for k, x := range v {
			if holdsUnpaired(k) || holdsUnpaired(x) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------------------------------
// Values

type object = map[string]any

const maxInt = 1<<53 - 1

// numberOf reads a JSON number as an IEEE 754 double. A number too large for a double isn't one (the SDKs' decoders
// give Infinity, which no rule accepts either).
func numberOf(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	return f, err == nil
}

// intOf reads a SPEC.md §2 int: a number whose value is integral and at most 2^53 − 1 in magnitude, so 1.0 and 1e3
// are ints.
func intOf(v any) (int64, bool) {
	f, ok := numberOf(v)
	if !ok || f != math.Trunc(f) || math.Abs(f) > maxInt {
		return 0, false
	}
	return int64(f), true
}

func intAtLeast(min int64) func(any) bool { return intIn(min, maxInt) }

// intIn: an int from min to max, both included.
func intIn(min, max int64) func(any) bool {
	return func(v any) bool {
		n, ok := intOf(v)
		return ok && n >= min && n <= max
	}
}

var anyInt = intAtLeast(-maxInt)

// The fixed protocol bounds of SPEC.md §13.1: a descriptor's durationMs, a snapshot's positionMs and an offsetMs.
const (
	maxDurationMs = 604800000
	maxOffsetMs   = 604800000
)

var (
	durationMs = intIn(1, maxDurationMs)
	positionMs = intIn(0, maxDurationMs)
	offsetMs   = intIn(-maxOffsetMs, maxOffsetMs)
)

func isBool(v any) bool {
	_, ok := v.(bool)
	return ok
}

func oneOf(values ...string) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		for _, x := range values {
			if ok && s == x {
				return true
			}
		}
		return false
	}
}

func matches(re *regexp.Regexp) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && re.MatchString(s)
	}
}

func stringWith(valid func(string) bool) func(any) bool {
	return func(v any) bool {
		s, ok := v.(string)
		return ok && valid(s)
	}
}

func nullOr(check func(any) bool) func(any) bool {
	return func(v any) bool { return v == nil || check(v) }
}

// text: 1 or more code points, no forbidden characters, no unpaired surrogates.
var text = stringWith(func(s string) bool { return ValidText(s, 1, math.MaxInt) })

func required(m object, k string, check func(any) bool) bool {
	v, ok := m[k]
	return ok && check(v)
}

func optional(m object, k string, check func(any) bool) bool {
	v, ok := m[k]
	return !ok || check(v)
}

// ---------------------------------------------------------------------------------------------------------------------
// Descriptors (spec/SDK.md §9.7)

const (
	maxMediaBytes = 4096
	maxExtBytes   = 1024
)

var (
	oshashPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	extNamePattern = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+$`)
)

// CheckMedia applies the descriptor checks of spec/SDK.md §9.7 to a descriptor's JSON text: the shape and value rules
// always, the sizes on send only.
func CheckMedia(raw []byte, send bool) bool {
	m, ok := decode(raw).(object)
	if !ok {
		return false
	}
	ok = required(m, "kind", oneOf("movie", "episode", "video")) &&
		required(m, "title", text) &&
		optional(m, "year", anyInt) &&
		optional(m, "ids", ids) &&
		(m["kind"] != "episode" || required(m, "series", series)) &&
		optional(m, "series", series) &&
		optional(m, "season", intAtLeast(0)) &&
		optional(m, "episode", intAtLeast(0)) &&
		required(m, "durationMs", durationMs) &&
		optional(m, "file", file) &&
		optional(m, "ext", ext)
	if !ok || !send {
		return ok
	}
	whole, extBytes := SentSizes(m)
	return whole <= maxMediaBytes && extBytes <= maxExtBytes
}

func ids(v any) bool {
	m, ok := v.(object)
	if !ok {
		return false
	}
	for k, id := range m {
		s, ok := id.(string)
		if !ok || s == "" || holdsUnpaired(k) || holdsUnpaired(s) {
			return false
		}
	}
	return true
}

func series(v any) bool {
	m, ok := v.(object)
	return ok && required(m, "title", text) && optional(m, "year", anyInt) && optional(m, "ids", ids)
}

func file(v any) bool {
	m, ok := v.(object)
	return ok && optional(m, "sizeBytes", intAtLeast(0)) && optional(m, "oshash", matches(oshashPattern)) &&
		optional(m, "name", fileName)
}

// fileName: SPEC.md §10 File name. A hint compared with names the app found itself, never a path.
func fileName(v any) bool {
	s, ok := v.(string)
	return ok && text(s) && !strings.ContainsAny(s, `/\`) && s != "." && s != ".."
}

func ext(v any) bool {
	m, ok := v.(object)
	if !ok {
		return false
	}
	for k, member := range m {
		if !extNamePattern.MatchString(k) || holdsUnpaired(member) {
			return false
		}
	}
	return true
}

// SentSizes measures a valid descriptor as an SDK sends it: its known members only, ints written as integers, compact
// JSON, non-ASCII characters as UTF-8, except that Go escapes U+2028 and U+2029 where the SDKs write them raw (the
// generator keeps both out of send cases). It returns the bytes of the whole descriptor and of `ext` (0 without one).
func SentSizes(m object) (whole, extBytes int) {
	sent := object{}
	keep := func(dst, src object, keys ...string) {
		for _, k := range keys {
			v, ok := src[k]
			if !ok {
				continue
			}
			if n, isInt := intOf(v); isInt {
				v = n
			}
			dst[k] = v
		}
	}
	keep(sent, m, "kind", "title", "year", "ids", "season", "episode", "durationMs", "ext")
	for _, k := range []string{"series", "file"} {
		if src, ok := m[k].(object); ok {
			dst := object{}
			keep(dst, src, "title", "year", "ids", "sizeBytes", "oshash", "name")
			sent[k] = dst
		}
	}
	if e, ok := m["ext"]; ok {
		extBytes = encodedLen(e)
	}
	return encodedLen(sent), extBytes
}

func encodedLen(v any) int {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return b.Len() - 1 // Encode appends a newline
}

// ---------------------------------------------------------------------------------------------------------------------
// Snapshots (spec/SDK.md §8)

var (
	peerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)
	codePattern   = regexp.MustCompile(`^[` + Alphabet + `]{6}$`)
)

// CheckSnapshot reports whether a snapshot's fields (`joined.room`, or those of a `room` message) pass
// snapshot.schema.json.
func CheckSnapshot(raw []byte) bool {
	m, ok := decode(raw).(object)
	return ok && snapshot(m)
}

// CheckRoom reports whether a `room` message, its `media` member aside, passes SDK.md §8: the snapshot fields and an
// optional `action`. The party checks `media` only on a `room` that starts a new epoch.
func CheckRoom(raw []byte) bool {
	m, ok := decode(raw).(object)
	return ok && snapshot(m) && optional(m, "action", action)
}

func snapshot(m object) bool {
	ok := required(m, "rev", intAtLeast(1)) &&
		required(m, "code", matches(codePattern)) &&
		required(m, "epoch", intAtLeast(0)) &&
		required(m, "phase", oneOf("idle", "paused", "waiting", "playing")) &&
		required(m, "positionMs", positionMs) &&
		required(m, "atMs", intAtLeast(0)) &&
		required(m, "rate", rate) &&
		required(m, "control", oneOf("host", "everyone")) &&
		required(m, "locked", isBool) &&
		required(m, "waitingOn", waitingOn) &&
		required(m, "participants", participants)
	if !ok {
		return false
	}
	if pos, _ := intOf(m["positionMs"]); m["phase"] == "idle" && pos != 0 {
		return false
	}
	return m["phase"] == "waiting" || len(m["waitingOn"].([]any)) == 0
}

// rate: a number from minRate to maxRate (SPEC.md §13.1), fixed in v1.
func rate(v any) bool {
	f, ok := numberOf(v)
	return ok && f >= 0.5 && f <= 2
}

func waitingOn(v any) bool {
	a, ok := v.([]any)
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		s, ok := x.(string)
		if !ok || !peerIDPattern.MatchString(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

// action: an object with `kind` a non-empty string and `by` null or a peer ID (SPEC.md §9.1).
func action(v any) bool {
	m, ok := v.(object)
	return ok && required(m, "kind", stringWith(func(s string) bool { return s != "" })) &&
		required(m, "by", nullOr(matches(peerIDPattern)))
}

func participants(v any) bool {
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return false
	}
	hosts := 0
	for _, x := range a {
		p, ok := x.(object)
		if !ok || !participant(p) {
			return false
		}
		if p["host"] == true {
			hosts++
		}
	}
	return hosts == 1
}

func participant(p object) bool {
	return required(p, "peerId", matches(peerIDPattern)) &&
		required(p, "name", stringWith(ValidName)) &&
		required(p, "client", stringWith(ValidClient)) &&
		required(p, "host", isBool) &&
		required(p, "connected", isBool) &&
		required(p, "player", oneOf("idle", "loading", "ready", "buffering")) &&
		required(p, "match", nullOr(match)) &&
		required(p, "offsetMs", offsetMs)
}

func match(v any) bool {
	m, ok := v.(object)
	return ok && required(m, "state", oneOf("searching", "matched", "mismatched", "unavailable")) &&
		optional(m, "deltaMs", anyInt)
}
