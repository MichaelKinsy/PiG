// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package docsdrift

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// User-facing install and version-pinning commands must never recommend a
// retracted module version (from go.mod retract directives) or an older PiG
// release version as if it were the current release.

var (
	// Matches runnable install/get/tag-pinning references to PiG's own module or installer:
	//   go install github.com/MichaelKinsy/PiG/...@vX.Y.Z
	//   go get github.com/MichaelKinsy/PiG/...@vX.Y.Z
	//   @vX.Y.Z when pinned as an exact PiG release (e.g. "such as `@v0.8.0`")
	//   PIG_VERSION=X.Y.Z or PIG_VERSION=vX.Y.Z
	goInstallPiGVersion = regexp.MustCompile(`github\.com/MichaelKinsy/PiG[^\s` + "`" + `"'()]*@(v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)`)
	pigVersionEnv       = regexp.MustCompile(`\bPIG_VERSION=(v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)`)
	bareAtPiGVersion    = regexp.MustCompile(`@(v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)`)
)

type retractedRange struct {
	Low  string
	High string
}

func (r retractedRange) contains(ver string) bool {
	v := canonicalV(ver)
	return semver.Compare(v, canonicalV(r.Low)) >= 0 && semver.Compare(v, canonicalV(r.High)) <= 0
}

func canonicalV(ver string) string {
	if strings.HasPrefix(ver, "v") {
		return ver
	}
	return "v" + ver
}

func readRetractedVersions(t *testing.T) []retractedRange {
	t.Helper()
	path := filepath.Join(repoRoot, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("parse go.mod: %v", err)
	}
	out := make([]retractedRange, 0, len(f.Retract))
	for _, r := range f.Retract {
		out = append(out, retractedRange{Low: r.Low, High: r.High})
	}
	if len(out) == 0 {
		t.Fatal("go.mod has no retract directives; update test if retractions are ever removed")
	}
	return out
}

func isHistoricalDoc(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	return generatedPage(rel) || strings.Contains(base, "changelog") || strings.Contains(base, "release-notes")
}

func isShellFenceLang(lang string) bool {
	switch lang {
	case "", "sh", "bash", "zsh", "shell", "console", "text":
		return true
	default:
		return false
	}
}

func checkReleaseVersionsInDoc(src docSource, currentPiG string, retracted []retractedRange) []string {
	if isHistoricalDoc(src.Rel) {
		return nil
	}
	currentV := canonicalV(currentPiG)
	var errs []string
	lines := strings.Split(src.Body, "\n")

	checkVersion := func(lineNo int, rawVer, context string) {
		v := canonicalV(rawVer)
		if !semver.IsValid(v) {
			return
		}
		for _, r := range retracted {
			if r.contains(v) {
				errs = append(errs, fmt.Sprintf("%s:%d: %s names retracted version %s (retracted in go.mod: [%s, %s]); use %s",
					src.Rel, lineNo, context, rawVer, r.Low, r.High, currentV))
				return
			}
		}
		if semver.Compare(v, currentV) < 0 {
			errs = append(errs, fmt.Sprintf("%s:%d: %s names older PiG release %s; current release is %s",
				src.Rel, lineNo, context, rawVer, currentV))
		}
	}

	inFence := false
	fence := ""
	fenceLang := ""
	for i, line := range lines {
		lineNo := i + 1
		if !inFence {
			if m := fenceOpen.FindStringSubmatch(line); m != nil {
				inFence = true
				fence = m[2]
				fenceLang = strings.ToLower(m[3])
				continue
			}
		} else if closesFence(line, fence) {
			inFence = false
			fence = ""
			fenceLang = ""
			continue
		}

		// Always check explicit PiG module @vX.Y.Z and PIG_VERSION=X.Y.Z anywhere in user-facing docs
		// (inline commands, prose examples, and fenced blocks).
		for _, m := range goInstallPiGVersion.FindAllStringSubmatch(line, -1) {
			checkVersion(lineNo, m[1], "PiG module command")
		}
		for _, m := range pigVersionEnv.FindAllStringSubmatch(line, -1) {
			checkVersion(lineNo, m[1], "PIG_VERSION")
		}

		// Inside runnable shell fences, also catch bare @vX.Y.Z on lines referencing PiG or go install/get.
		if inFence && isShellFenceLang(fenceLang) {
			if strings.Contains(line, "MichaelKinsy/PiG") || strings.Contains(line, "@pi-in-go/pig") {
				for _, m := range bareAtPiGVersion.FindAllStringSubmatch(line, -1) {
					checkVersion(lineNo, m[1], "runnable install command")
				}
			}
		}

		// Outside fences, catch prose that recommends an exact PiG release tag such as `@v0.2.0`.
		if !inFence && (strings.Contains(line, "exact release") || strings.Contains(line, "exact version")) {
			for _, m := range bareAtPiGVersion.FindAllStringSubmatch(line, -1) {
				checkVersion(lineNo, m[1], "release tag example")
			}
		}
	}
	return errs
}

func TestUserFacingDocsDoNotRecommendRetractedOrStaleReleaseVersions(t *testing.T) {
	pin := readPin(t)
	retracted := readRetractedVersions(t)
	for _, src := range handWritten(corpus(t)) {
		for _, msg := range checkReleaseVersionsInDoc(src, pin["PigVersion"], retracted) {
			t.Error(msg)
		}
	}
}

func TestReleaseVersionGuardCatchesRetractedAndStaleExamples(t *testing.T) {
	retracted := []retractedRange{{Low: "v0.5.0", High: "v0.5.0"}}
	current := "0.8.0"

	cases := []struct {
		name     string
		rel      string
		body     string
		wantFail bool
		wantSub  string
	}{
		{
			name:     "retracted go install v0.5.0",
			rel:      "docs/site/docs/quickstart.md",
			body:     "```bash\ngo install github.com/MichaelKinsy/PiG/cmd/pig@v0.5.0\n```\n",
			wantFail: true,
			wantSub:  "retracted version v0.5.0",
		},
		{
			name:     "stale exact release @v0.2.0 in README prose",
			rel:      "README.md",
			body:     "Use an exact release such as `@v0.2.0` when reproducibility matters.\n",
			wantFail: true,
			wantSub:  "older PiG release v0.2.0",
		},
		{
			name:     "stale PIG_VERSION=0.5.0 inline",
			rel:      "docs/site/docs/install-troubleshooting.md",
			body:     "Run `curl -fsSL https://pi-in-go.dev/install.sh | PIG_VERSION=0.5.0 sh`.\n",
			wantFail: true,
			wantSub:  "retracted version 0.5.0",
		},
		{
			name:     "stale PIG_VERSION=0.7.0 in code block",
			rel:      "docs/site/docs/install-troubleshooting.md",
			body:     "```bash\ncurl -fsSL https://pi-in-go.dev/install.sh | PIG_VERSION=0.7.0 sh\n```\n",
			wantFail: true,
			wantSub:  "older PiG release 0.7.0",
		},
		{
			name:     "current v0.8.0 passes",
			rel:      "docs/site/docs/quickstart.md",
			body:     "```bash\ngo install github.com/MichaelKinsy/PiG/cmd/pig@v0.8.0\n```\nUse an exact release such as `@v0.8.0`.\n`PIG_VERSION=0.8.0 sh`\n",
			wantFail: false,
		},
		{
			name:     "bug-ledger is exempt",
			rel:      "docs/site/docs/bug-ledger.md",
			body:     "go install github.com/MichaelKinsy/PiG/cmd/pig@v0.5.0\n",
			wantFail: false,
		},
		{
			name:     "third-party package version is ignored",
			rel:      "docs/site/docs/piglet-lifecycle.md",
			body:     "```bash\npig piglet pull github:acme/reviewer@0.1.0\n```\n",
			wantFail: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkReleaseVersionsInDoc(docSource{Rel: tc.rel, Body: tc.body}, current, retracted)
			if tc.wantFail && len(got) == 0 {
				t.Fatalf("expected failure containing %q, got none", tc.wantSub)
			}
			if !tc.wantFail && len(got) > 0 {
				t.Fatalf("expected pass, got: %v", got)
			}
			if tc.wantSub != "" && !strings.Contains(strings.Join(got, "\n"), tc.wantSub) {
				t.Fatalf("errors %v do not contain %q", got, tc.wantSub)
			}
		})
	}
}
