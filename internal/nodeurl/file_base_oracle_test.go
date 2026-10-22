package nodeurl

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// A file base keeps its first path segment as a drive letter only when the segment is exactly a letter and ":" (ada 4, Node 24.22.1 and 26.12.1). Node 22.19 and 24.19 keep any segment that starts with them ("C:ab", "u:p@[::1]:9000"), so this runs on the qualified Node (.node-version).
func TestResolveOnFileBaseDriveLettersMatchesNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	bases := []string{
		"file:///C:", "file:///C:/", "file:///C:/c", "file:///C|/c", "file:///c:/a/b", "file:///C:a", "file:///C:ab", "file:///C:ab/c", "file:///C:a/b", "file:///ab:/c", "file:///u:p@h/c",
		"file:///u:p@[::1]:9000", "file:///u:p@[::1]:9000?a=b#f", "file:///u:p@0x7f.1?a=%zz", "file://host/C:/x", "file://host/C:ab/x", "file:///%43:/x", "file:///C:%2f/x", "file:///1:/x", "file:///:/x",
	}
	refs := []string{"/x", "/v1/config", "/D:/x", "/d:", "x", "../x", "x/../y", "./x", "x?q", ".."}
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
		t.Fatalf("node output %q: %v", output, err)
	}
	for i, p := range probes {
		base, err := ParseURL(p.Base)
		if err != nil {
			if want[i] != nil {
				t.Errorf("ParseURL(%q): %v, Node resolves %q", p.Base, err, *want[i])
			}
			continue
		}
		var got string
		if strings.HasPrefix(p.Ref, "/") && !strings.HasPrefix(p.Ref, "//") {
			got, err = base.ResolveAbsolutePath(p.Ref)
		} else {
			var resolved URL
			if resolved, err = base.ResolveRelativePath(p.Ref); err == nil {
				got = resolved.Href()
			}
		}
		switch {
		case want[i] == nil && err == nil:
			t.Errorf("resolve(%q, %q) = %q, Node throws", p.Ref, p.Base, got)
		case want[i] != nil && (err != nil || got != *want[i]):
			t.Errorf("resolve(%q, %q) = %q (%v), Node %q", p.Ref, p.Base, got, err, *want[i])
		}
	}
}

// Node 24.22.1's answers, recorded for where Node is not installed.
func TestResolveOnFileBaseDriveLettersNodeSamples(t *testing.T) {
	for _, tc := range []struct{ base, ref, want string }{
		{"file:///C:ab/c", "/v1/config", "file:///v1/config"},
		{"file:///C:/c", "/v1/config", "file:///C:/v1/config"},
		{"file:///C|/c", "/x", "file:///C:/x"},
		{"file:///C:/c", "/D:/x", "file:///D:/x"},
		{"file:///u:p@[::1]:9000", "/systemone", "file:///systemone"},
		{"file:///u:p@h/c", "/x", "file:///x"},
		{"file:///C:ab", "x", "file:///x"},
		{"file:///C:ab/b", "x", "file:///C:ab/x"},
		{"file:///C:", "x", "file:///C:/x"},
		{"file:///C:a/b", "../x", "file:///x"},
	} {
		base, err := ParseURL(tc.base)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if tc.ref[0] == '/' {
			got, err = base.ResolveAbsolutePath(tc.ref)
		} else {
			var resolved URL
			if resolved, err = base.ResolveRelativePath(tc.ref); err == nil {
				got = resolved.Href()
			}
		}
		if err != nil || got != tc.want {
			t.Errorf("resolve(%q, %q) = %q (%v), want %q", tc.ref, tc.base, got, err, tc.want)
		}
	}
}
