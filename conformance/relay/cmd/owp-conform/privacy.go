package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// SPEC §15: privacy.

// issued records, for the privacy scenario, every room code and token the relay issued during the run and every
// client address the suite sent.
var issued = struct {
	sync.Mutex
	codes, tokens, addrs map[string]bool
}{codes: map[string]bool{}, tokens: map[string]bool{}, addrs: map[string]bool{}}

func recordIssued(set map[string]bool, v string) {
	if v == "" {
		return
	}
	issued.Lock()
	set[v] = true
	issued.Unlock()
}

func issuedList(set map[string]bool) []string {
	issued.Lock()
	defer issued.Unlock()
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// scPrivacy: the relay's log holds no room code, token, name, client, descriptor or message content, and neither its
// log nor its state holds a client address (§15). It checks everything the run so far used, plus a room made here
// with marked names, media and messages, after a restart so the state is written.
func scPrivacy(x *sc) error {
	marker := fmt.Sprintf("Mk%x", time.Now().UnixNano())
	c, err := x.dial("P")
	if err != nil {
		return err
	}
	if _, err := enter(c, Msg{"type": "create", "name": "Name" + marker, "client": "App" + marker + "/1"}); err != nil {
		return err
	}
	media := Msg{"kind": "movie", "title": "Title" + marker, "durationMs": 60000,
		"ext": Msg{"app.marker": Msg{"v": "Ext" + marker}}}
	if err := setMediaAll([]*Client{c}, c, media); err != nil {
		return err
	}
	if err := sendRawCases(c, []rawCase{
		{`{"type":"bogus","x":"Junk` + marker + `"}`, "unsupported", "bogus"},
		{`Junk` + marker, "invalid_message", ""},
		{`{"type":"seek","positionMs":"Junk` + marker + `"}`, "invalid_message", "seek"},
	}); err != nil {
		return err
	}
	if err := x.relay.stop(); err != nil {
		return err
	}
	if err := closedWith(c, websocket.CloseGoingAway, false); err != nil {
		return err
	}
	if err := x.relay.start(); err != nil {
		return err
	}
	logText, err := os.ReadFile(x.relay.logPath())
	if err != nil {
		return err
	}
	var problems []string
	if i := strings.Index(string(logText), marker); i >= 0 {
		problems = append(problems, fmt.Sprintf("log line %q holds a name, client, title, ext or message content",
			lineAt(string(logText), i)))
	}
	for _, s := range []struct {
		what   string
		values []string
		word   func(byte) bool
	}{
		{"room code", issuedList(issued.codes), isCodeByte},
		{"token", issuedList(issued.tokens), func(byte) bool { return false }},
		{"client address", issuedList(issued.addrs), isAddrByte},
	} {
		if v, line := findWord(string(logText), s.values, s.word); v != "" {
			problems = append(problems, fmt.Sprintf("log holds the %s %s: %q", s.what, v, line))
		}
	}
	err = filepath.WalkDir(x.relay.stateDir(), func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if v, _ := findWord(string(b), issuedList(issued.addrs), isAddrByte); v != "" {
			rel, _ := filepath.Rel(x.relay.stateDir(), path)
			problems = append(problems, fmt.Sprintf("state file %s holds the client address %s", rel, v))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s (log %s)", strings.Join(problems, "; "), x.relay.logPath())
	}
	return nil
}

func isCodeByte(b byte) bool { return b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }

func isAddrByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F' || b == '.' || b == ':'
}

// findWord returns the first of values that occurs in text as a whole word (no word byte right before or after it)
// and the line it occurs in.
func findWord(text string, values []string, word func(byte) bool) (string, string) {
	for _, v := range values {
		for from := 0; ; {
			i := strings.Index(text[from:], v)
			if i < 0 {
				break
			}
			i += from
			end := i + len(v)
			if (i == 0 || !word(text[i-1])) && (end == len(text) || !word(text[end])) {
				return v, lineAt(text, i)
			}
			from = i + 1
		}
	}
	return "", ""
}

func lineAt(text string, i int) string {
	start := strings.LastIndexByte(text[:i], '\n') + 1
	end := strings.IndexByte(text[i:], '\n')
	if end < 0 {
		end = len(text) - i
	}
	return truncate(text[start:i+end], 200)
}
