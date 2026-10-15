package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Update discovers the highest stable version in the installed release's signed GitHub namespace, or selects Options.Version explicitly. It preserves the installed target by default and refuses rollback, identity and unapproved signer changes. A current release returns its existing result without rewriting managed files.
// pig additive (D18): installed signed identity selects the update namespace and remains pinned across release discovery.
func Update(ctx context.Context, name string, options Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if !validGitHubPart(name) {
		return Result{}, fmt.Errorf("invalid Piglet name")
	}
	installed, err := currentInstalled(name)
	if err != nil {
		return Result{}, fmt.Errorf("read installed Piglet %s: %w", name, err)
	}
	github := installed.Index.GitHub
	if github == nil {
		return Result{}, fmt.Errorf("Piglet %s has no signed GitHub release identity; pull an explicit release reference", name)
	}
	trust, err := signature.LoadTrust(signature.TrustDir())
	if err != nil {
		return Result{}, err
	}
	if trust.Revoked[installed.Index.Signer.KeyID] {
		return Result{}, fmt.Errorf("installed Piglet signer is revoked")
	}
	if trust.RequireSignature {
		if _, known := trust.Keys[installed.Index.Signer.KeyID]; !known {
			return Result{}, fmt.Errorf("installed Piglet signer is not trusted and your policy requires a trusted signature")
		}
	}
	if options.Target == "" {
		options.Target = installed.Manifest.Target
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: pullTimeout}
	}
	version := strings.TrimPrefix(options.Version, "v")
	if version == "" {
		version, err = discoverGitHubVersion(ctx, options.Client, *github)
		if err != nil {
			return Result{}, err
		}
	}
	if !validReleaseVersion(version) {
		return Result{}, fmt.Errorf("invalid Piglet release version")
	}
	if semver.Compare("v"+version, "v"+installed.Index.Version) < 0 {
		return Result{}, fmt.Errorf("Piglet update refuses rollback from %s to %s", installed.Index.Version, version)
	}
	if version == installed.Index.Version && options.Target == installed.Manifest.Target {
		lock, err := lockStore()
		if err != nil {
			return Result{}, err
		}
		defer func() { _ = lock.Close() }()
		current, err := readCurrent(name)
		if err != nil {
			return Result{}, err
		}
		if err := validateCurrent(name, current, []Installed{installed}); err != nil {
			return Result{}, fmt.Errorf("installed Piglet changed during update: %w", err)
		}
		if err := checkSignerContinuity(name, installed.Index.Signer.KeyID, options.AcceptSigner); err != nil {
			return Result{}, err
		}
		return Result{Piglet: name, Version: version, Target: options.Target, SignerKeyID: installed.Index.Signer.KeyID, Artifact: installed.ArtifactPath, Receipt: installed.ReceiptPath}, nil
	}
	options.Version = version
	return pull(ctx, github.Reference(version), options, name)
}

// pig additive (D18): discovery filters exact signed tag namespaces, never repository-wide latest. A bounded incomplete inventory fails rather than choosing from a truncated page set.
func discoverGitHubVersion(ctx context.Context, client *http.Client, github GitHubRelease) (string, error) {
	const pageSize, maxPages = 100, 100
	best := ""
	for page := 1; page <= maxPages; page++ {
		rawURL, err := githubURL("api.github.com", fmt.Sprintf("/repos/%s/releases?per_page=%d&page=%d", github.Repository, pageSize, page))
		if err != nil {
			return "", err
		}
		data, err := fetchBounded(ctx, client, rawURL, maxIndexBytes)
		if err != nil {
			return "", fmt.Errorf("discover GitHub Piglet releases: %w", err)
		}
		var releases []struct {
			TagName    string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := json.Unmarshal(data, &releases); err != nil {
			return "", fmt.Errorf("decode GitHub releases: %w", err)
		}
		for _, release := range releases {
			version, ok := strings.CutPrefix(release.TagName, github.TagPrefix+"v")
			if !ok || release.Draft || release.Prerelease || !validReleaseVersion(version) || semver.Prerelease("v"+version) != "" {
				continue
			}
			if best == "" || semver.Compare("v"+version, "v"+best) > 0 {
				best = version
			}
		}
		if len(releases) < pageSize {
			if best == "" {
				return "", fmt.Errorf("no stable release in GitHub Piglet tag namespace %q", github.TagPrefix)
			}
			return best, nil
		}
	}
	return "", fmt.Errorf("GitHub release discovery exceeds page limit")
}

func currentInstalled(name string) (Installed, error) {
	current, err := readCurrent(name)
	if err != nil {
		return Installed{}, err
	}
	installed, err := readInstalled(filepath.Join(codingagent.PigletRecordsDir(), filepath.FromSlash(current.Receipt)))
	if err != nil {
		return Installed{}, err
	}
	if err := validateCurrent(name, current, []Installed{installed}); err != nil {
		return Installed{}, err
	}
	return installed, nil
}

// pig additive (D18): verified current receipts prevent cross-repository substitution and rollback, including concurrent publication.
func checkReleaseContinuity(index Index, accepted string) error {
	if err := checkSignerContinuity(index.Piglet, index.Signer.KeyID, accepted); err != nil {
		return err
	}
	if _, err := readCurrent(index.Piglet); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	installed, err := currentInstalled(index.Piglet)
	if err != nil {
		return err
	}
	previous := installed.Index
	if semver.Compare("v"+index.Version, "v"+previous.Version) < 0 {
		return fmt.Errorf("Piglet release refuses rollback from %s to %s", previous.Version, index.Version)
	}
	if previous.GitHub != nil && (index.GitHub == nil || *index.GitHub != *previous.GitHub) {
		return fmt.Errorf("Piglet release GitHub repository or tag namespace changed")
	}
	return nil
}
