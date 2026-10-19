// Command check validates the OWP v1 JSON Schemas and examples against each other and against SPEC.md.
//
// It fails when a schema is not valid JSON Schema 2020-12, when a valid example breaks its schema or a wire rule or
// an invalid one breaks neither, when a schema has no valid or no invalid example, or when a message type in SPEC.md's
// message tables has no schema (or a message schema has no row there).
//
// Wire rules are the SPEC.md rules a schema can't express: valid UTF-8 (§3.1), no duplicate member names and no
// unpaired surrogate escapes (§4, §6.2), the message size of client messages (§3.1) and the descriptor and ext sizes
// (§10).
//
// Usage, from spec/check: go run . [-spec DIR] [-v]
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	idBase   = "https://openwatch.party/schema/v1/"
	draft    = "https://json-schema.org/draft/2020-12/schema"
	suffix   = ".schema.json"
	specFile = "SPEC.md"

	maxMessageBytes = 8192 // SPEC.md §13.1
	maxMediaBytes   = 4096
	maxExtBytes     = 1024
)

func main() {
	dir := flag.String("spec", "..", "path to the spec directory")
	verbose := flag.Bool("v", false, "print why each invalid example fails")
	flag.Parse()
	failures, summary := run(*dir, *verbose)
	for _, f := range failures {
		fmt.Fprintln(os.Stderr, "FAIL", f)
	}
	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "%d failure(s)\n", len(failures))
		os.Exit(1)
	}
	fmt.Println("ok:", summary)
}

// run performs every check and returns the failures and, when there are none, a one-line summary.
func run(dir string, verbose bool) ([]string, string) {
	var fails []string
	fail := func(format string, args ...any) { fails = append(fails, fmt.Sprintf(format, args...)) }

	docs, err := loadSchemas(filepath.Join(dir, "schema"))
	if err != nil {
		return []string{err.Error()}, ""
	}
	spec, err := os.ReadFile(filepath.Join(dir, specFile))
	if err != nil {
		return []string{err.Error()}, ""
	}
	types, clientTypes := messageTypes(string(spec))

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(offline{})
	for name, doc := range docs {
		obj, _ := doc.(map[string]any)
		if obj["$schema"] != draft {
			fail("schema/%s%s: $schema must be %q", name, suffix, draft)
		}
		if want := idBase + name + suffix; obj["$id"] != want {
			fail("schema/%s%s: $id must be %q", name, suffix, want)
		}
		if err := c.AddResource(idBase+name+suffix, doc); err != nil {
			fail("schema/%s%s: %v", name, suffix, err)
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, name := range sortedKeys(docs) {
		sch, err := c.Compile(idBase + name + suffix)
		if err != nil {
			fail("schema/%s%s: %v", name, suffix, err)
			continue
		}
		compiled[name] = sch
	}

	examples := map[string]map[bool]int{} // schema name → want-valid → count
	for _, wantValid := range []bool{true, false} {
		kind := "invalid"
		if wantValid {
			kind = "valid"
		}
		paths, _ := filepath.Glob(filepath.Join(dir, "examples", kind, "*.json"))
		for _, path := range paths {
			rel := filepath.Join("examples", kind, filepath.Base(path))
			name, _, _ := strings.Cut(filepath.Base(path), ".")
			if _, ok := docs[name]; !ok {
				fail("%s: no schema/%s%s", rel, name, suffix)
				continue
			}
			sch := compiled[name]
			if sch == nil {
				continue // compile failure already reported
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				fail("%s: %v", rel, err)
				continue
			}
			doc, err := jsonschema.UnmarshalJSON(bufio.NewReader(bytes.NewReader(raw)))
			if err != nil {
				fail("%s: %v", rel, err)
				continue
			}
			if examples[name] == nil {
				examples[name] = map[bool]int{}
			}
			examples[name][wantValid]++
			wireErr := wireCheck(name, raw, clientTypes[name])
			schemaErr := sch.Validate(doc)
			switch {
			case wantValid && wireErr != nil:
				fail("%s: breaks a wire rule, want pass: %v", rel, wireErr)
			case wantValid && schemaErr != nil:
				fail("%s: fails %s%s, want pass:\n%v", rel, name, suffix, schemaErr)
			case !wantValid && wireErr == nil && schemaErr == nil:
				fail("%s: passes %s%s and the wire rules, want failure", rel, name, suffix)
			case !wantValid && verbose:
				fmt.Printf("%s: %v\n", rel, cmp.Or(wireErr, schemaErr))
			}
		}
	}
	nExamples := 0
	for _, name := range sortedKeys(docs) {
		if name == "common" {
			continue // definitions only; exercised through the schemas that use them
		}
		if examples[name][true] == 0 {
			fail("schema/%s%s: no example in examples/valid/", name, suffix)
		}
		if examples[name][false] == 0 {
			fail("schema/%s%s: no example in examples/invalid/", name, suffix)
		}
		nExamples += examples[name][true] + examples[name][false]
	}

	if len(types) == 0 {
		fail("%s: no message tables found (a table whose header starts with |type|)", specFile)
	}
	for _, t := range types {
		doc, ok := docs[t]
		if !ok {
			fail("%s: message type %q has no schema/%s%s", specFile, t, t, suffix)
			continue
		}
		if got := typeConst(doc); got != t {
			fail("schema/%s%s: properties.type.const is %q, want %q", t, suffix, got, t)
		}
	}
	for _, name := range sortedKeys(docs) {
		if t := typeConst(docs[name]); t != "" {
			if !slices.Contains(types, t) {
				fail("schema/%s%s: message type %q is not in %s's message tables", name, suffix, t, specFile)
			}
		} else if name != "common" && !strings.Contains(string(spec), name+suffix) {
			fail("schema/%s%s: not a message schema and not referenced in %s", name, suffix, specFile)
		}
	}

	if len(fails) > 0 {
		return fails, ""
	}
	return nil, fmt.Sprintf("%d schemas, %d examples, %d message types", len(docs), nExamples, len(types))
}

// wireCheck applies the SPEC.md rules that JSON Schema can't express to an example's raw bytes. name is the schema
// the example belongs to; client says whether it is a client → relay message.
func wireCheck(name string, raw []byte, client bool) error {
	if !utf8.Valid(raw) {
		return errors.New("not valid UTF-8 (SPEC.md §3.1)")
	}
	if err := unpairedSurrogate(raw); err != nil {
		return err
	}
	if err := walk(json.NewDecoder(bytes.NewReader(raw)), "$"); err != nil {
		return err
	}
	raw = bytes.TrimSpace(raw)
	if client && len(raw) > maxMessageBytes {
		return fmt.Errorf("%d bytes, over maxMessageBytes (SPEC.md §3.1)", len(raw))
	}
	var media json.RawMessage
	switch name {
	case "descriptor":
		media = raw
	case "setMedia":
		var msg map[string]json.RawMessage
		if json.Unmarshal(raw, &msg) == nil {
			media = msg["media"]
		}
	}
	if len(media) > maxMediaBytes {
		return fmt.Errorf("media is %d bytes, over maxMediaBytes (SPEC.md §10)", len(media))
	}
	var desc map[string]json.RawMessage
	if json.Unmarshal(media, &desc) == nil && len(desc["ext"]) > maxExtBytes {
		return fmt.Errorf("ext is %d bytes, over maxExtBytes (SPEC.md §10)", len(desc["ext"]))
	}
	return nil
}

// unpairedSurrogate fails on a \uXXXX escape in the range D800–DFFF that isn't a high surrogate directly followed by
// a low surrogate escape (SPEC.md §4). encoding/json decodes such escapes to U+FFFD without an error, so this scans
// the raw text.
func unpairedSurrogate(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ { // the body skips over escapes, so i is advanced by hand
		switch c := raw[i]; {
		case !inString:
			inString = c == '"'
		case c == '"':
			inString = false
		case c == '\\':
			u := escapeU(raw, i)
			switch {
			case u < 0:
				i++ // a one-character escape such as \" or \\
			case u < 0xD800 || u > 0xDFFF:
				i += 5
			case u <= 0xDBFF && isLow(escapeU(raw, i+6)):
				i += 11
			default:
				return fmt.Errorf("unpaired surrogate escape at byte %d (SPEC.md §4)", i)
			}
		}
	}
	return nil
}

// escapeU returns the value of the \uXXXX escape at raw[i:], or -1 if there is none.
func escapeU(raw []byte, i int) int {
	if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
		return -1
	}
	v, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
	if err != nil {
		return -1
	}
	return int(v)
}

func isLow(u int) bool { return u >= 0xDC00 && u <= 0xDFFF }

// walk reads one JSON value from dec and fails on an object with two members of the same name, compared after
// decoding escapes (SPEC.md §6.2).
func walk(dec *json.Decoder, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			k := key.(string)
			if seen[k] {
				return fmt.Errorf("%s: duplicate member %q (SPEC.md §6.2)", path, k)
			}
			seen[k] = true
			if err := walk(dec, path+"."+k); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for i := 0; dec.More(); i++ {
			if err := walk(dec, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	_, err = dec.Token() // the closing delimiter
	return err
}

// loadSchemas reads every *.schema.json in dir, keyed by the name before the suffix.
func loadSchemas(dir string) (map[string]any, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*"+suffix))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s: no schemas", dir)
	}
	docs := map[string]any{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bufio.NewReader(f))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("schema/%s: %w", filepath.Base(path), err)
		}
		docs[strings.TrimSuffix(filepath.Base(path), suffix)] = doc
	}
	return docs, nil
}

// messageTypes returns the first-column values of every Markdown table whose header row starts with |type|, in
// order of appearance and without duplicates, and which of them are client → relay messages (their table has an
// "allowed for" column).
func messageTypes(spec string) ([]string, map[string]bool) {
	var types []string
	client := map[string]bool{}
	inTable, clientTable := false, false
	for line := range strings.Lines(spec) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "|type|"):
			inTable, clientTable = true, strings.Contains(line, "|allowed for|")
		case !strings.HasPrefix(line, "|"):
			inTable = false
		case inTable && !strings.HasPrefix(line, "|---"):
			cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
			t := strings.Trim(strings.TrimSpace(cell), "`")
			if t != "" && !slices.Contains(types, t) {
				types = append(types, t)
				client[t] = clientTable
			}
		}
	}
	return types, client
}

// typeConst returns properties.type.const of a schema document, or "" if it has none.
func typeConst(doc any) string {
	obj, _ := doc.(map[string]any)
	props, _ := obj["properties"].(map[string]any)
	typ, _ := props["type"].(map[string]any)
	s, _ := typ["const"].(string)
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// offline refuses every load: all schemas are added as resources, and the 2020-12 metaschema is built in.
type offline struct{}

func (offline) Load(url string) (any, error) {
	return nil, errors.New("not loading " + url + ": every schema must be in spec/schema")
}
