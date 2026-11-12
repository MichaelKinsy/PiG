// SPDX-License-Identifier: MIT

// Command modulepins fails when an in-tree Go module that replaces the PiG root module or the extension SDK module with local source requires either at a version other than the one the root go.mod requires for the SDK. A stale pin makes the generated packed-cell go.mod select an older SDK than the replaced root needs, and `go build` stops with "updates to go.mod needed" in every strict and packed Go cell of the conformance suite. `make set-version SET_VERSION_ARGS=--release-modules` pins them at a release; between releases, bumping the root's SDK pseudo-version means bumping these too (go mod edit -require=<module>@<version> <go.mod>). It also fails on a Go source that writes such a requirement at run time with pigversion.PigVersion or a literal version instead of internal/testenv.RootSDKRequirement (templates.go).
//
// It also fails when the root go.mod pins the SDK at a commit that is not in the history of the branch it is run on: the 12 hex digits of a pseudo-version, or the commit of the tag extensions/sdk/<version>, must be an ancestor of HEAD (git merge-base --is-ancestor). A pin of another line is public and passes every module check, then dangles when the branch is squash-merged. The check needs the history; ci.yml's bookkeeping job checks out with fetch-depth 0. In a shallow checkout that lacks the commit or cannot reach it, the result is a failure that says so (see reachable.go).
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/mod/modfile"
)

const (
	sdkPath  = "github.com/MichaelKinsy/PiG/extensions/sdk"
	rootPath = "github.com/MichaelKinsy/PiG"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	problems, err := check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "modulepins:", err)
		os.Exit(2)
	}
	reachable, err := checkReachable(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "modulepins:", err)
		os.Exit(2)
	}
	problems = append(problems, reachable...)
	for _, problem := range problems {
		fmt.Fprintln(os.Stderr, problem)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "modulepins: %d pin problem(s)\n", len(problems))
		os.Exit(1)
	}
}

func parse(path string) (*modfile.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return modfile.Parse(path, data, nil)
}

func pinned(file *modfile.File, path string) string {
	for _, requirement := range file.Require {
		if requirement.Mod.Path == path {
			return requirement.Mod.Version
		}
	}
	return ""
}

// check returns one line per in-tree module (other than the root) that replaces the root or SDK module and requires either at a version other than the root's SDK requirement.
func check(root string) ([]string, error) {
	rootFile, err := parse(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	want := pinned(rootFile, sdkPath)
	if want == "" {
		return nil, fmt.Errorf("root go.mod does not require %s", sdkPath)
	}
	var problems []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "target":
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "go.mod" || path == filepath.Join(root, "go.mod") {
			return nil
		}
		file, err := parse(path)
		if err != nil {
			return err
		}
		replaced := false
		for _, replacement := range file.Replace {
			if replacement.Old.Path == sdkPath || replacement.Old.Path == rootPath {
				replaced = true
			}
		}
		if !replaced {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, module := range []string{sdkPath, rootPath} {
			if got := pinned(file, module); got != "" && got != want {
				problems = append(problems, fmt.Sprintf("%s requires %s %s, want the root module's SDK pin %s (go mod edit -require=%s@%s %s)", rel, module, got, want, module, want, rel))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	templates, err := checkTemplates(root)
	problems = append(problems, templates...)
	sort.Strings(problems)
	return problems, err
}
