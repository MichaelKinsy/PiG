// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hosting platform for https://pi-in-go.dev (the site build, the
// Cloudflare Worker, its wrangler configuration, and the deploy and R2
// mirror workflows) lives in a private repository, as pi.dev's does for Pi.
// This repository keeps the pig client, the user docs the site renders from
// docs/site/docs, and the installer at docs/site/public/install.sh.

// platformPaths must not exist here; the owner's release script refuses the
// same paths when it builds the public commit.
var platformPaths = []string{
	".github/workflows/site-deploy.yml",
	"docs/site/app",
	"docs/site/data",
	"docs/site/drizzle",
	"docs/site/package.json",
	"docs/site/pnpm-lock.yaml",
	"docs/site/scripts",
	"docs/site/vendor",
	"docs/site/worker",
	"docs/site/wrangler.jsonc",
}

func TestHostingPlatformStaysOutOfThePublicRepository(t *testing.T) {
	root := repoRoot(t)
	for _, path := range platformPaths {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
			t.Errorf("%s belongs to the private hosting repository", path)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	// docs/site/public holds only the installer the site serves.
	entries, err := os.ReadDir(filepath.Join(root, "docs", "site", "public"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "install.sh" {
			t.Errorf("docs/site/public/%s belongs to the private hosting repository", entry.Name())
		}
	}
}

// TestPublicWorkflowsHoldNoHostingDeployment keeps Cloudflare credentials and
// deploy tooling out of this repository's workflows, and every secret
// reference behind an env: assignment (a secret inlined into a run: string
// is a shell-injection and log-leak risk).
func TestPublicWorkflowsHoldNoHostingDeployment(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no workflows found; the check would pass vacuously")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		for number, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			for _, forbidden := range []string{"wrangler", "CLOUDFLARE_", "docs/site/worker"} {
				if strings.Contains(trimmed, forbidden) {
					t.Errorf("%s:%d: %q belongs to the private hosting repository: %s", name, number+1, forbidden, trimmed)
				}
			}
			if strings.Contains(trimmed, "secrets.") && !strings.HasPrefix(trimmed, "if:") && !strings.Contains(trimmed, ": ${{ secrets.") {
				t.Errorf("%s:%d: secrets reference outside an env: assignment or if: condition: %s", name, number+1, trimmed)
			}
		}
	}
}
