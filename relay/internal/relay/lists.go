package relay

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// normalizePrefixes upper-cases --prefixes (ASCII) and requires 1-31 distinct
// code symbols.
func normalizePrefixes(s string) (string, error) {
	s = asciiUpper(s)
	if s == "" {
		return "", errors.New("prefixes must not be empty")
	}
	for i := range len(s) {
		if !isCodeSymbol(s[i]) {
			return "", fmt.Errorf("prefixes must be symbols of %s", codeAlphabet)
		}
		if strings.IndexByte(s[:i], s[i]) >= 0 {
			return "", fmt.Errorf("prefix %q is listed twice", s[i])
		}
	}
	return s, nil
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c - 'A' + 'a'
		}
	}
	return string(b)
}

// validRelayID: 1-32 characters of a-z, 0-9 and -, starting with a letter or digit.
func validRelayID(id string) bool {
	if id == "" || len(id) > 32 || id[0] == '-' {
		return false
	}
	for _, c := range []byte(id) {
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// parseDenyList reads one client name per line; blank lines and lines
// starting with # are ignored. Names compare case-insensitively.
func parseDenyList(data []byte) map[string]bool {
	deny := make(map[string]bool)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, len(data)+1)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			deny[asciiLower(line)] = true
		}
	}
	return deny
}

// watchedFile notices changes to a file by polling its identity, modification
// time and size, which also catches an atomic rename over it.
type watchedFile struct {
	path string
	info fs.FileInfo // nil while the file is missing
}

// read returns the file's contents when it changed since the last call, and
// changed=false otherwise. A missing file reads as empty.
func (w *watchedFile) read() (data []byte, changed bool, err error) {
	info, err := os.Stat(w.path)
	if errors.Is(err, fs.ErrNotExist) {
		changed = w.info != nil
		w.info = nil
		return nil, changed, nil
	}
	if err != nil {
		return nil, false, err
	}
	if prev := w.info; prev != nil && os.SameFile(prev, info) && prev.ModTime().Equal(info.ModTime()) &&
		prev.Size() == info.Size() {
		return nil, false, nil
	}
	data, err = os.ReadFile(w.path)
	if err != nil {
		return nil, false, err
	}
	w.info = info
	return data, true, nil
}
