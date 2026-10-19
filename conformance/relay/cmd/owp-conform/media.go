package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SPEC §10: the media descriptor.

// scMediaValidation: setMedia refuses a descriptor that breaks §10 with invalid_message, forbidden characters in
// title, series.title and file.name, a file.name that could be a path and a durationMs past 1–604800000 included; it
// accepts one at the size limits and at both durationMs bounds and forwards it exactly as sent, unknown members
// included.
func scMediaValidation(x *sc) error {
	a, _, err := x.create("A")
	if err != nil {
		return err
	}
	maxMedia, maxExt := int(x.lim("maxMediaBytes")), int(x.lim("maxExtBytes"))
	movie := func(kv ...any) Msg {
		m := Msg{"kind": "movie", "title": "X", "durationMs": 1000}
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i+1] == nil {
				delete(m, kv[i].(string))
			} else {
				m[kv[i].(string)] = kv[i+1]
			}
		}
		return m
	}
	series := Msg{"title": "S"}
	// The examples sweep (examples.go) sends every descriptor in spec/examples. These are the cases those files don't
	// hold, and the size limits as this relay reports them in /v1/info.
	bad := []struct {
		what  string
		media any
	}{
		{"no durationMs", movie("durationMs", nil)},
		{"durationMs 1.5", movie("durationMs", 1.5)},
		{"no title", movie("title", nil)},
		{"no kind", movie("kind", nil)},
		{"a string", "x"},
		{"year as a string", movie("year", "2008")},
		{"episode without series", movie("kind", "episode")},
		{"series with an empty title", movie("kind", "episode", "series", Msg{"title": ""})},
		{"season -1", movie("kind", "episode", "series", series, "season", -1)},
		{"episode 1.5", movie("kind", "episode", "series", series, "episode", 1.5)},
		{"ids not an object", movie("ids", "tt1")},
		{"empty id", movie("ids", Msg{"imdb": ""})},
		{"file.sizeBytes -1", movie("file", Msg{"sizeBytes": -1})},
		{"15-digit oshash", movie("file", Msg{"oshash": "8e245d9679d31e1"})},
		{"empty file.name", movie("file", Msg{"name": ""})},
		{"U+009F in file.name", movie("file", Msg{"name": "a\u009f.mkv"})},
		{"ext member in upper case", movie("ext", Msg{"Com.Example": Msg{}})},
		{"ext not an object", movie("ext", []any{})},
		{fmt.Sprintf("ext of %d bytes", maxExt+1), movie("ext", sized(Msg{"app.pad": Msg{"p": ""}}, maxExt+1, "app.pad", "p"))},
		{fmt.Sprintf("descriptor of %d bytes", maxMedia+1), sized(movie("pad", ""), maxMedia+1, "pad")},
	}
	for _, s := range bad {
		_ = a.send(Msg{"type": "setMedia", "media": s.media})
		if err := expectErr(a, "invalid_message", "setMedia"); err != nil {
			return fmt.Errorf("media with %s: %w", s.what, err)
		}
	}
	good := []struct {
		what  string
		media Msg
	}{
		{fmt.Sprintf("descriptor of exactly %d bytes", maxMedia), sized(movie("pad", ""), maxMedia, "pad")},
		{fmt.Sprintf("ext of exactly %d bytes", maxExt), movie("ext", sized(Msg{"app.pad": Msg{"p": ""}}, maxExt, "app.pad", "p"))},
		{"episode with every field and unknown members", sampleMedia(7_200_000)},
		{"video without ids, unknown namespace, season 0", Msg{"kind": "video", "title": "Clip", "durationMs": 1,
			"ids": Msg{"somewhere": "x"}, "file": Msg{"sizeBytes": 0, "oshash": "0123456789abcdef", "name": "clip.mp4"},
			"xFuture": Msg{"deep": []any{nil, 1.5, "s"}}}},
		{"episode numbered 0", normalize(Msg{"kind": "episode", "title": "Special", "durationMs": 60000,
			"series": Msg{"title": "S", "xUnknown": true}, "season": 0, "episode": 0})},
		{"the file name ...", movie("file", Msg{"name": "..."})},
		{"a no-break space in series.title and a ZWJ in file.name", movie("kind", "episode",
			"series", Msg{"title": "S\u00a02"}, "file", Msg{"name": "a\u200db.mkv"})},
	}
	for _, s := range good {
		media := normalize(s.media)
		_ = a.send(Msg{"type": "setMedia", "media": media})
		m, err := a.waitFor(reply, "room with the media of a valid descriptor", func(m Msg) bool { return hasMedia(m) || isType("error")(m) })
		if err != nil {
			return err
		}
		if str(m, "type") != "room" {
			return fmt.Errorf("%s: got %s, want a room with the media", s.what, brief(m))
		}
		if err := mediaDiff(m["media"], media); err != nil {
			return fmt.Errorf("%s: %w", s.what, err)
		}
	}
	return nil
}

// sized pads the string at path inside m so that m's compact JSON text is exactly n bytes.
func sized(m Msg, n int, path ...string) Msg {
	set := func(v string) {
		o := m
		for _, k := range path[:len(path)-1] {
			o = o[k].(Msg)
		}
		o[path[len(path)-1]] = v
	}
	set("")
	b, _ := json.Marshal(m)
	set(strings.Repeat("p", n-len(b)))
	return m
}
