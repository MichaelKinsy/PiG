// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"
)

// checkReachable returns one line when the root go.mod pins the extension SDK at a commit that this branch's own history does not contain.
// The pin names the commit of a pseudo-version (its 12 hex digits) or the commit of the tag extensions/sdk/<version>, and that commit must
// be an ancestor of HEAD (git merge-base --is-ancestor). A pin on another line goes green in the module checks (the commit is public) and
// then breaks when the branch is squash-merged or the other line is dropped: the SDK content the root builds against is no longer in the
// history that ships it. The check needs the history: ci.yml's bookkeeping job (make ci-bookkeeping, which runs module-pins) checks out with
// fetch-depth 0, which also fetches the tags. In a shallow checkout a pin whose commit is missing, or present but not reachable within the
// fetched depth, cannot be decided: the result is a failure that says so, never a pass.
func checkReachable(root string) ([]string, error) {
	rootFile, err := parse(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	version := pinned(rootFile, sdkPath)
	if version == "" {
		return nil, fmt.Errorf("root go.mod does not require %s", sdkPath)
	}
	pin := "the root go.mod pins " + sdkPath + " " + version
	g := gitRepo{root}
	if _, code, err := g.run("rev-parse", "--git-dir"); err != nil {
		return nil, err
	} else if code != 0 {
		return []string{pin + ", but " + root + " is not a git checkout, so the pin cannot be checked against the branch's history"}, nil
	}
	shallow := false
	if out, code, err := g.run("rev-parse", "--is-shallow-repository"); err != nil {
		return nil, err
	} else if code == 0 && out == "true" {
		shallow = true
	}
	const fullHistory = " (actions/checkout fetch-depth: 0, or git fetch --unshallow --tags)"

	var what, revision string
	if module.IsPseudoVersion(version) {
		revision, err = module.PseudoVersionRev(version)
		if err != nil {
			return nil, err
		}
		what = "commit " + revision
	} else {
		tag := "extensions/sdk/" + version
		what = "tag " + tag
		revision = tag
	}
	sha, code, err := g.run("rev-parse", "--verify", "--quiet", revision+"^{commit}")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		switch {
		case shallow:
			return []string{pin + ": " + what + " is not in this checkout, which is shallow; fetch the full history" + fullHistory + " so the pin can be checked"}, nil
		case strings.HasPrefix(what, "tag "):
			return []string{pin + ": " + what + " is not in this checkout; fetch the tags (git fetch --tags) or publish the tag first"}, nil
		}
		return []string{pin + ": " + what + " is not in this repository: it was never pushed, or a squash merge or a force-push dropped it; pin a commit of this branch's own history (the commit directly below the bump commit)"}, nil
	}
	if strings.HasPrefix(what, "tag ") {
		what += " (commit " + sha[:12] + ")"
	}
	_, code, err = g.run("merge-base", "--is-ancestor", sha, "HEAD")
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
		return nil, nil
	case 1:
		if shallow {
			return []string{pin + ": " + what + " is not reachable from HEAD within the fetched depth of this shallow checkout, which cannot show whether it is an ancestor; fetch the full history" + fullHistory + " so the pin can be checked"}, nil
		}
		return []string{pin + ": " + what + " is not an ancestor of HEAD; it is on another line (git branch -r --contains " + sha[:12] + "); pin a commit of this branch's own history, the commit directly below the bump commit"}, nil
	}
	return nil, fmt.Errorf("git merge-base --is-ancestor %s HEAD exited with status %d in %s", sha[:12], code, root)
}

type gitRepo struct{ dir string }

// run runs git in the repository and returns its trimmed standard output and its exit status; err is set only when git cannot run at all.
func (g gitRepo) run(args ...string) (string, int, error) {
	//nolint:gosec // G204 (class a): program is literal git; args are the callers' fixed subcommands with -C root, and a revision made of 12 hex digits or of extensions/sdk/ plus a version that modfile parsed.
	cmd := exec.Command("git", append([]string{"-C", g.dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return strings.TrimSpace(out.String()), 0, nil
	case errors.As(err, &exit):
		return strings.TrimSpace(out.String()), exit.ExitCode(), nil
	}
	return "", -1, err
}
