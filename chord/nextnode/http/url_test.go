package http

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

// Probes Pi's URL step against Node: NodeIncoming.of (packages/chord/src/node/http/node.ts:103-117) makes `new URL("http://" + host + target)`
// of a request's Host and target, refuses with 400 when it throws and with 403 when the URL has userinfo, else keeps its href.
// testdata/url_corpus.jsonl is what testdata/url_probe.cjs recorded from the pinned Node (.node-version): [host, target, result]
// with the Latin-1 strings Node's parser hands out (one byte per character), and result {"href"} or {"status"}.

// latin1Bytes is s with each character as one byte.
func latin1Bytes(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return string(out)
}

func TestTheWireURLIsWhatTheURLParserMakesOfTheHostAndTarget(t *testing.T) {
	file, err := os.Open("testdata/url_corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	rows, diffs := 0, 0
	for scanner.Scan() {
		var row struct {
			host, target string
			result       struct {
				Href   string `json:"href"`
				Status int    `json:"status"`
			}
		}
		var raw [3]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(raw[0], &row.host) != nil || json.Unmarshal(raw[1], &row.target) != nil || json.Unmarshal(raw[2], &row.result) != nil {
			t.Fatalf("bad row %s", scanner.Bytes())
		}
		host, target := latin1Bytes(row.host), latin1Bytes(row.target)
		href, status := parseWireURL(host, target)
		rows++
		if href != row.result.Href || status != row.result.Status {
			diffs++
			shown := target
			if len(shown) > 60 {
				shown = shown[:60] + "..."
			}
			t.Errorf("host %q target %q: href %q status %d, node href %q status %d", row.host, shown, href, status, row.result.Href, row.result.Status)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 900 {
		t.Fatalf("only %d corpus rows", rows)
	}
	t.Logf("%d rows, %d diffs", rows, diffs)
}

// testdata/host_corpus.jsonl is what testdata/host_probe.cjs recorded from the pinned Node: [host, hostname or null] for a Host with no
// port, userinfo or path, as Unicode strings (the characters a Latin-1 Host header cannot hold reach parseHost through percent-encoding).
func TestTheHostIsWhatTheURLParserMakesOfItAsUnicode(t *testing.T) {
	file, err := os.Open("testdata/host_corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	rows, diffs := 0, 0
	for scanner.Scan() {
		var row [2]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		var host string
		var want *string
		if json.Unmarshal(row[0], &host) != nil || json.Unmarshal(row[1], &want) != nil {
			t.Fatalf("bad row %s", scanner.Bytes())
		}
		got, ok := parseHost(host)
		rows++
		if want == nil && ok || want != nil && (!ok || got != *want) {
			diffs++
			node := "refused"
			if want != nil {
				node = *want
			}
			t.Errorf("host %q: %q ok %v, node %s", host, got, ok, node)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 2000 {
		t.Fatalf("only %d corpus rows", rows)
	}
	t.Logf("%d rows, %d diffs", rows, diffs)
}
