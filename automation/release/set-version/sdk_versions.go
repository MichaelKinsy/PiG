// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
)

// The Python package and the TypeScript declarations publish at the PiG release version. The Rust crate does not: Pig resolves an un-pathed `pig-sdk`
// requirement to the staged SDK through a crates-io patch, Cargo applies a patch only when the patched version satisfies the requirement, and every
// scaffolded extension requires "0.1.0" (coding/cli/extension_init_command.go rustCargoTemplate). Advancing the crate past 0.1.x would stop those
// extensions from building. The Go SDK is a nested module versioned by its tag; --release-modules pins its requirers.
var (
	pyprojectVer    = regexp.MustCompile(`(?m)^(\[project\]\r?\nname = "pig-sdk"\r?\nversion = )"[^"]+"`)
	packageJSONVer  = regexp.MustCompile(`(?m)^(  "version": )"[^"]+"`)
	packageLockVers = regexp.MustCompile(`(?m)^((?:  | {6})"version": )"[^"]+"`)
)

const (
	pythonManifest = "extensions/sdk-py/pyproject.toml"
	tsManifest     = "extensions/sdk-ts/package.json"
	tsLock         = "extensions/sdk-ts/package-lock.json"
)

// updateSDKVersions writes version into the Python and TypeScript SDK manifests. A manifest that is absent is skipped, so a checkout that does not carry an
// SDK is unaffected; one that is present and has no version to replace is an error, because the drift check would then fail it.
func (p *plan) updateSDKVersions(version string) error {
	replace := func(file string, re *regexp.Regexp, limit int) error {
		data, err := p.read(file)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		count := 0
		out := re.ReplaceAllStringFunc(string(data), func(match string) string {
			count++
			if limit > 0 && count > limit {
				return match
			}
			return re.ReplaceAllString(match, `${1}"`+version+`"`)
		})
		if count == 0 {
			return fmt.Errorf("%s: no SDK version to replace", file)
		}
		p.after[file] = []byte(out)
		return nil
	}
	for _, step := range []struct {
		file  string
		re    *regexp.Regexp
		limit int
	}{
		{pythonManifest, pyprojectVer, 0},
		{tsManifest, packageJSONVer, 1},
		{tsLock, packageLockVers, 2},
	} {
		if err := replace(step.file, step.re, step.limit); err != nil {
			return err
		}
	}
	return nil
}
