package main

import (
	"fmt"
	"path"
	"strings"
)

// baseImageCommands are the commands the bundle mode's pod image provides itself; every other bare command resolves
// to the bundle's bin/, which leads the pod's PATH.
var baseImageCommands = map[string]bool{"node": true}

// rootPathEnd ends a {root}/ path inside an argument such as -wasm={root}/dist/a.wasm or {root}/a:{root}/b.
const rootPathEnd = ":,;= \"'"

// checkBundlePrograms refuses, before any Job runs, a session whose profile runs a program the bundle does not carry.
// In the pod {root} is the unpacked bundle and PATH starts at its bin/, so each target's seed and sample command must
// name, for every {root}/ path (the pod fills {root} in anywhere in an argument) and for a bare command other than
// the base image's own, a file the bundle lists in ARTIFACTS.sha256 and so verifies before the session starts, or a
// directory that holds such a file.
func checkBundlePrograms(prof *profile, targets []string, b *bundleInfo) error {
	listed := map[string]bool{}
	for _, a := range b.artifacts {
		listed[a[0]] = true
		for dir := path.Dir(a[0]); dir != "."; dir = path.Dir(dir) {
			listed[dir] = true
		}
	}
	var missing []string
	need := func(t target, p string) {
		if p = path.Clean(p); p != "." && !listed[p] {
			missing = append(missing, fmt.Sprintf("%s needs %s", t.Name, p))
		}
	}
	for _, name := range targets {
		t, ok := prof.byName(name)
		if !ok {
			continue
		}
		for _, argv := range [][]string{t.Seed, t.Sample} {
			for i, arg := range argv {
				if i == 0 && !strings.ContainsAny(arg, "/{") && !baseImageCommands[arg] {
					need(t, "bin/"+arg)
				}
				for rest := arg; ; {
					_, after, ok := strings.Cut(rest, "{root}/")
					if !ok {
						break
					}
					p := after
					if end := strings.IndexAny(after, rootPathEnd); end >= 0 {
						p = after[:end]
					}
					need(t, p)
					rest = after[len(p):]
				}
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the bundle %s (%s) cannot run profile %s: %s; build the bundle from a tree that has the profile's programs", b.dir, b.commit, prof.Name, strings.Join(dedupe(missing), "; "))
	}
	return nil
}

func dedupe(xs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
