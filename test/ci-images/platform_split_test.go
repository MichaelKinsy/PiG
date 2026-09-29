// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
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

// TestPublicWorkflowsHoldNoHostingDeployment keeps hosting credentials and deploy tooling out of public workflows. Provider inference keys follow Pi's env-api-keys data; secret references must remain env assignments or conditions.
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
			for _, forbidden := range hostingDeploymentReferences(trimmed) {
				t.Errorf("%s:%d: %q belongs to the private hosting repository: %s", name, number+1, forbidden, trimmed)
			}
			if strings.Contains(trimmed, "secrets.") && !strings.HasPrefix(trimmed, "if:") && !strings.Contains(trimmed, ": ${{ secrets.") {
				t.Errorf("%s:%d: secrets reference outside an env: assignment or if: condition: %s", name, number+1, trimmed)
			}
		}
	}
}

func hostingDeploymentReferences(line string) []string {
	var forbidden []string
	for _, token := range []string{"wrangler", "docs/site/worker"} {
		if strings.Contains(line, token) {
			forbidden = append(forbidden, token)
		}
	}
	for _, name := range regexp.MustCompile(`\bCLOUDFLARE_[A-Za-z0-9_]*\b`).FindAllString(line, -1) {
		// FindEnvKeys is the shared port of packages/ai/src/env-api-keys.ts. Probe only the candidate name with a synthetic value, never a worker credential.
		providerKey := false
		for _, provider := range ai.ListProviders() {
			if slices.Contains(ai.FindEnvKeys(provider, map[string]string{name: "guard-probe"}), name) {
				providerKey = true
				break
			}
		}
		if !providerKey {
			forbidden = append(forbidden, name)
		}
	}
	return forbidden
}

func TestHostingGuardDistinguishesInferenceFromDeployment(t *testing.T) {
	for _, tc := range []struct {
		line      string
		forbidden bool
	}{
		{`CLOUDFLARE_API_KEY: ${{ secrets.CLOUDFLARE_API_KEY }}`, false},
		{`CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}`, true},
		{`CLOUDFLARE_API_KEY_DEPLOY: ${{ secrets.CLOUDFLARE_API_KEY_DEPLOY }}`, true},
		{`CLOUDFLARE_ACCOUNT_ID: ${{ secrets.CLOUDFLARE_ACCOUNT_ID }}`, true},
		{`run: npx wrangler deploy`, true},
		{`working-directory: docs/site/worker`, true},
		{`run: npx wrangler deploy # CLOUDFLARE_API_KEY`, true},
	} {
		t.Run(tc.line, func(t *testing.T) {
			if got := hostingDeploymentReferences(tc.line); (len(got) != 0) != tc.forbidden {
				t.Fatalf("hosting references = %v, forbidden = %t", got, tc.forbidden)
			}
		})
	}
}
