// Command tools regenerates the client test vectors in conformance/vectors/ from the reference implementation in
// ref.go.
//
//	go run .          rewrite every table
//	go run . -check   exit 1 if any table differs from what this code generates
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

// Case is one row of a table.
type Case struct {
	Name   string `json:"name"`
	Input  any    `json:"input"`
	Expect any    `json:"expect"`
}

// Table is one vectors file.
type Table struct {
	Name  string
	Cases []Case
}

func tables() []Table {
	return []Table{
		timelineTable(),
		clockTable(),
		reconcileTable(),
		matchingTable(),
		sameTitleTable(),
		codeRoutingTable(),
		relayListTable(),
		namesTable(),
		mediaCheckTable(),
		roomCheckTable(),
		partyTable(),
	}
}

// render writes a table with one case per line, so diffs stay readable.
func render(t Table) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"table\": %q,\n  \"cases\": [\n", t.Name)
	for i, c := range t.Cases {
		line, err := compact(c)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", t.Name, c.Name, err)
		}
		b.WriteString("    ")
		b.Write(line)
		if i < len(t.Cases)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("  ]\n}\n")
	return b.Bytes(), nil
}

// compact encodes v on one line in printable ASCII: DEL and every non-ASCII character become \uXXXX escapes, so
// invisible characters in the code-routing and names tables stay visible and survive editors. JSON's own escaping
// already covers U+0000–U+001F.
func compact(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, r := range strings.TrimSuffix(b.String(), "\n") {
		switch {
		case r < 0x7f:
			out.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, hi, lo)
		}
	}
	return out.Bytes(), nil
}

func main() {
	dir := flag.String("dir", "..", "the conformance/vectors directory")
	check := flag.Bool("check", false, "compare instead of writing; exit 1 on any difference")
	flag.Parse()
	if err := run(*dir, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string, check bool) error {
	want := map[string][]byte{}
	for _, t := range tables() {
		data, err := render(t)
		if err != nil {
			return err
		}
		want[t.Name+".json"] = data
	}
	if !check {
		for name, data := range want {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	var problems []string
	for name, data := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		switch {
		case os.IsNotExist(err):
			problems = append(problems, name+": missing")
		case err != nil:
			return err
		case !bytes.Equal(got, data):
			problems = append(problems, name+": differs from the generator's output")
		}
	}
	existing, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	for _, path := range existing {
		if _, ok := want[filepath.Base(path)]; !ok {
			problems = append(problems, filepath.Base(path)+": not produced by the generator")
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("vectors are stale (run `go run .` in conformance/vectors/tools):\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}
