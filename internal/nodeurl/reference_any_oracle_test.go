package nodeurl

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestResolveReferenceMatchesNode compares ResolveReference with Node's `new URL(input, base).href` for bases of every kind (special, file with and without a drive letter or host, non-special with and without an authority, opaque) against references of every form.
func TestResolveReferenceMatchesNode(t *testing.T) {
	node := testenv.RequireQualifiedNode(t)
	bases := []string{
		"https://json-schema.invalid/root.json", "https://ex/a/b?x#y", "http://EX:80/a/./b", "HTTP://Example.COM:80/a/./b/../c.json", "ws://h/a/b", "ftp://u:p@h/a/b",
		"file:///C:/a/b.json", "file:///a/b", "file://localhost/a/b.json", "file://host/a/b", "file:///C:", "file:///c:/a/b?q#f",
		"custom://host/p/q.json", "custom://Host:80/p/q.json?z#f", "custom:/a/b", "custom:/a/b?q", "custom://host", "custom://host/a/../b",
		"urn:example:root", "urn:a:b#f", "foo:bar", "data:text/plain,hello", "mailto:a@b.c",
	}
	refs := []string{
		"", "#f", "#", "#é #", "?q", "?q#f", "x", "x y", "../x", "../../x.json", "../../../../x", "/x", "/x?y#z", "//h/x", "//", "///x", "c\\d.json", "\\x", "\\\\h\\x", "..", ".", "./x", "x/./y/../z",
		"https://other/z", "HTTP://H:80/x", "http:x", "http:/x", "http://", "file:///z", "file:x", "file:/x", "file:///C:/z", "custom://h/z", "custom:x", "custom:/x", "urn:x:y", "urn:a:b#g",
		"%41", "é", "a%2Fb", "x#%zz", "a:b", "1:2", "+x:y", "x:", "c:\\a", "c:/a",
	}
	type probe struct{ Ref, Base string }
	var probes []probe
	for _, base := range bases {
		for _, ref := range refs {
			probes = append(probes, probe{ref, base})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--input-type=module", "--eval",
		`import { readFileSync } from "node:fs"; process.stdout.write(JSON.stringify(JSON.parse(readFileSync(0, "utf8")).map(({ Ref, Base }) => { try { return new URL(Ref, Base).href; } catch { return null; } })));`)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(probes) {
		t.Fatalf("node output: %v (%d results for %d probes)", err, len(want), len(probes))
	}
	mismatches := 0
	for i, p := range probes {
		resolved, err := ResolveReference(p.Ref, p.Base)
		switch {
		case want[i] == nil && err == nil:
			mismatches++
			t.Errorf("new URL(%q, %q) throws in Node; ResolveReference gives %q", p.Ref, p.Base, resolved.Href())
		case want[i] != nil && err != nil:
			mismatches++
			t.Errorf("new URL(%q, %q) = %q in Node; ResolveReference fails: %v", p.Ref, p.Base, *want[i], err)
		case want[i] != nil && resolved.Href() != *want[i]:
			mismatches++
			t.Errorf("new URL(%q, %q) = %q in Node; ResolveReference gives %q", p.Ref, p.Base, *want[i], resolved.Href())
		}
	}
	if mismatches > 0 {
		t.Logf("%d of %d probes differ", mismatches, len(probes))
	}
}
