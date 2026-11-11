// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A go.mod a test writes at run time is a module like the committed ones: when it requires the PiG or SDK module and replaces it with local source, it must require the version the root go.mod requires for the SDK. The helper that returns it is internal/testenv.RootSDKRequirement; pigversion.PigVersion is the release version, which is not the pseudo-version the root requires between releases. checkTemplates reports a Go source that formats the requirement from the release version (a "v%s" verb after the module path, or PigVersion next to a go.mod template).
var (
	// requirement of the PiG or SDK module whose version is filled in with a "v%s" verb, the shape of a template that formats the release version. A literal version is left alone: the resolution tests spell older and newer versions on purpose.
	versionedRequirement = regexp.MustCompile(`github\.com/MichaelKinsy/PiG(?:/extensions/sdk)? v%s`)
)

func checkTemplates(root string) ([]string, error) {
	var problems []string
	self := filepath.Join("automation", "ci", "modulepins")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "target":
				return filepath.SkipDir
			}
			if rel == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		var lines []string
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 1<<16), 1<<24)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		for i, line := range lines {
			if versionedRequirement.MatchString(line) {
				problems = append(problems, fmt.Sprintf("%s:%d writes a PiG or SDK requirement with a version that is not testenv.RootSDKRequirement's", rel, i+1))
				continue
			}
			if !strings.Contains(line, "PigVersion") {
				continue
			}
			for j := max(0, i-2); j <= min(len(lines)-1, i+2); j++ {
				if strings.Contains(lines[j], "MichaelKinsy/PiG") && (strings.Contains(lines[j], "require") || strings.Contains(lines[j], "go.mod")) {
					problems = append(problems, fmt.Sprintf("%s:%d uses PigVersion in a go.mod template; use testenv.RootSDKRequirement", rel, i+1))
					break
				}
			}
		}
		return nil
	})
	return problems, err
}
