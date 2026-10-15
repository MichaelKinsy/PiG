// Package boundary checks that the production core links none of the reflection-driven standard-library packages TinyGo cannot
// build faithfully (go list -deps over the core packages, the Go and the Wasm configuration).
package boundary

import (
	"bytes"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// ForbiddenPackages are the packages the core must not link: each is reflection or reflection-driven (TinyGo supports them
// only partially, and they pull large runtime support into the Wasm module), so a core package that reached one would build
// under Go and break or bloat under TinyGo.
var ForbiddenPackages = []string{
	"encoding/json", "encoding/json/v2", "encoding/json/jsontext", "reflect", "text/template", "text/template/parse", "html/template",
}

// StdRoutes are the standard-library edges to a forbidden package the core accepts: encoding/binary imports reflect for
// Read and Write on arbitrary values, and the core calls only the byte-order and Append functions, which the linker keeps
// without reflect's support. Every other standard-library route (fmt, encoding/gob, sort on Go versions that used reflect...)
// is a violation.
var StdRoutes = map[string][]string{"encoding/binary": {"reflect"}}

// CorePackages are the packages built into the Wasm module (ADR D15, LAYOUT.md), as import paths relative to durable/core.
var CorePackages = []string{"abi", "jv", "internal/delta", "rec", "history", "store", "session/hopcost", "session", "docs", "inbox", "turn", "."}

// WasmMain is the reactor's main package (the one TinyGo builds); it is a root of the wasip1 closure only.
const WasmMain = "cmd/corewasm"

// NonCorePackages are the durable/core packages that are not built into the Wasm module: hosts, drivers, tooling and the
// test-only helpers. They may use the whole standard library.
var NonCorePackages = []string{
	"abi/abitest", "abi/payload", "abi/spec", "abi/spec/cmd/abigen", "budget/coldhints", "budget/tinygo/patchdata", "cmd/corenative", "contracttest", "contracttest/binding", "contracttest/boundary",
	"contracttest/cmd/benchhost", "contracttest/cmd/countgate", "contracttest/cmd/replayhost", "contracttest/cmd/scprovider", "contracttest/counts",
	"contracttest/fuel", "contracttest/guard", "contracttest/storehost", "driver",
	"history/internal/buildcheck", "probe", "session/cmd/tgprobe", "session/sessiontest", "sqlhost", "sqlhost/cmd/bench",
}

// Violation is one forbidden dependency and the import chain that reaches it.
type Violation struct {
	Root      string
	Forbidden string
	Chain     []string // Root ... Forbidden
}

func (v Violation) String() string {
	return fmt.Sprintf("%s reaches %s: %s", v.Root, v.Forbidden, strings.Join(v.Chain, " -> "))
}

// Graph maps an import path to the packages it imports (non-test files of one build configuration).
type Graph map[string][]string

// CheckGraph returns a violation for every root that reaches a forbidden package, by the shortest chain. A standard-library
// package (an import path without a dot in its first element) may import a forbidden package only along a StdRoutes edge;
// that edge is not followed, so the route is neither reported nor counted as the root reaching the package.
func CheckGraph(g Graph, roots, forbidden []string) []Violation {
	var out []Violation
	for _, root := range roots {
		prev := map[string]string{root: ""}
		queue := []string{root}
		found := map[string]bool{}
		for len(queue) > 0 {
			at := queue[0]
			queue = queue[1:]
			for _, next := range g[at] {
				if _, seen := prev[next]; seen {
					continue
				}
				if slices.Contains(forbidden, next) && isStd(at) && slices.Contains(StdRoutes[at], next) {
					continue
				}
				prev[next] = at
				if slices.Contains(forbidden, next) {
					if !found[next] {
						found[next] = true
						out = append(out, Violation{Root: root, Forbidden: next, Chain: chain(prev, next)})
					}
					continue
				}
				queue = append(queue, next)
			}
		}
	}
	return out
}

func isStd(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

func chain(prev map[string]string, to string) []string {
	var rev []string
	for at := to; at != ""; at = prev[at] {
		rev = append(rev, at)
	}
	slices.Reverse(rev)
	return rev
}

// ListGraph runs `go list -deps` for the patterns in dir with the extra environment (GOOS, GOARCH) and returns the import
// graph of the non-test packages and the roots' import paths. cgo is off so the graph is the one a pure-Go build links.
func ListGraph(dir string, env []string, patterns ...string) (Graph, []string, error) {
	rootsOut, err := goList(dir, env, append([]string{"-f", "{{.ImportPath}}"}, patterns...))
	if err != nil {
		return nil, nil, err
	}
	depsOut, err := goList(dir, env, append([]string{"-deps", "-f", `{{.ImportPath}} {{join .Imports " "}}`}, patterns...))
	if err != nil {
		return nil, nil, err
	}
	g := Graph{}
	for line := range strings.SplitSeq(strings.TrimSpace(depsOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			g[fields[0]] = fields[1:]
		}
	}
	return g, strings.Fields(rootsOut), nil
}

func goList(dir string, env []string, args []string) (string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), append([]string{"CGO_ENABLED=0"}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// CheckImportBoundary lists the patterns' dependency closure and returns its violations of ForbiddenPackages.
func CheckImportBoundary(dir string, env []string, patterns ...string) ([]Violation, error) {
	g, roots, err := ListGraph(dir, env, patterns...)
	if err != nil {
		return nil, err
	}
	return CheckGraph(g, roots, ForbiddenPackages), nil
}
