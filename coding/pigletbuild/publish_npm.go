package pigletbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/npmpublish"
)

const publishNPMUsageLine = "Usage: pig piglet publish <name|path> --to npm [--yes|--dry-run] [--tag <dist-tag>] [--access public|restricted] [--otp <code>] [--npm-name <name>] [--package-map <alias>=npm:<name>@<range>]... [--binaries github:<owner/repo>|--no-binaries]"

const publishNPMUsage = publishNPMUsageLine + `

Publish a Piglet's source to npm so that anyone can add it with
pig piglet add npm:<name>. PiG writes the npm package, checks that the name and
version are free, and runs npm publish. Without --yes, publish is a dry run: it
shows what npm would pack and the exact command, and publishes nothing. npm does
the authentication (npm login, a one-time password, a passkey, or trusted
publishing in CI). PiG never reads or stores an npm token.

The package takes its name from a package.json beside the Piglet, else from the
Piglet's name, and its version from release.version. A local Package in the
Piglet's packages becomes npm:<name>@^<version> using that Package's own
package.json, so publish a local Package first (pig package publish) or map it.

Options:
  --to npm                       Publish Piglet source to npm
  --yes                          Run npm publish (the default is a dry run)
  --dry-run                      Show the plan only (default)
  --tag <dist-tag>               npm dist-tag (npm uses latest; a prerelease needs one)
  --access public|restricted     Package access (npm's default applies when omitted)
  --otp <code>                   One-time password from your authenticator
  --npm-name <name>              Package name, such as @scope/name
  --package-map <alias>=<ref>    npm reference for a Package alias; repeatable
  --binaries github:<owner/repo> Record the signed GitHub Binary release in package.json
                                 (default: the release recorded by publish --to github)
  --no-binaries                  Record no signed Binary release
  --workspace <path>             Resolve workspace-bound Piglet inputs from this path
  --no-input                     Do not prompt
  -h, --help                     Show this help

Set ` + npmpublish.TrustedPublishingEnv + ` (GitHub Actions with id-token: write) to publish with
provenance and no stored credential.
`

// githubOnlyPublishFlags are the options of --to github that mean nothing for npm.
var githubOnlyPublishFlags = []string{"--repo", "--sign-key", "--targets", "--artifacts", "--builder", "--tag-prefix", "--commit", "--source-ref"}

// npmPublishDeps are the host services publication uses.
type npmPublishDeps struct {
	// client downloads signed release indexes. nil selects the default.
	client *http.Client
	// command is the npm argv. nil selects the npmCommand setting, else npm.
	command []string
	stdin   io.Reader
}

type publishNPMRequest struct {
	pigletRef  string
	flags      npmpublish.Flags
	npmName    string
	workspace  string
	packageMap map[string]string
	binaries   string
	noBinaries bool
}

// publishDestination returns the value of --to, or "" when it is absent.
func publishDestination(args []string) string {
	for i := range args {
		if value, ok := strings.CutPrefix(args[i], "--to="); ok {
			return value
		}
		if args[i] == "--to" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func parsePublishNPMArgs(args []string) (publishNPMRequest, bool, error) {
	request := publishNPMRequest{packageMap: map[string]string{}}
	help := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		value := func() (string, error) {
			if hasInline {
				if inline == "" {
					return "", fmt.Errorf("%s requires a value", name)
				}
				return inline, nil
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", fmt.Errorf("%s requires a value", name)
			}
			i++
			return args[i], nil
		}
		if handled, err := request.flags.Parse(args, &i); handled || err != nil {
			if err != nil {
				return publishNPMRequest{}, false, err
			}
			continue
		}
		var err error
		switch {
		case arg == "-h" || arg == "--help":
			help = true
		case arg == "--no-input":
		case arg == "--no-binaries":
			request.noBinaries = true
		case name == "--to":
			var to string
			if to, err = value(); err == nil && to != "npm" {
				err = fmt.Errorf("--to must be npm here, not %q", to)
			}
		case name == "--npm-name":
			request.npmName, err = value()
		case name == "--workspace":
			request.workspace, err = value()
		case name == "--binaries":
			request.binaries, err = value()
		case name == "--package-map":
			var mapping string
			if mapping, err = value(); err == nil {
				alias, ref, ok := strings.Cut(mapping, "=")
				if !ok || alias == "" || ref == "" {
					err = fmt.Errorf("--package-map %q must be <alias>=npm:<name>@<range>", mapping)
				} else if _, dup := request.packageMap[alias]; dup {
					err = fmt.Errorf("--package-map names %q twice", alias)
				} else {
					request.packageMap[alias] = ref
				}
			}
		case slices.Contains(githubOnlyPublishFlags, name):
			err = fmt.Errorf("%s applies to --to github, not --to npm", name)
		case strings.HasPrefix(arg, "-"):
			err = fmt.Errorf("unknown option %q", arg)
		case request.pigletRef == "":
			request.pigletRef = arg
		default:
			err = fmt.Errorf("unexpected argument %q", arg)
		}
		if err != nil {
			return publishNPMRequest{}, false, err
		}
	}
	if help {
		return request, true, nil
	}
	switch {
	case request.pigletRef == "":
		return publishNPMRequest{}, false, errors.New("Piglet name or path is required")
	case request.binaries != "" && request.noBinaries:
		return publishNPMRequest{}, false, errors.New("--binaries and --no-binaries cannot be combined")
	}
	if err := request.flags.Validate(); err != nil {
		return publishNPMRequest{}, false, err
	}
	if request.binaries != "" {
		repository, ok := strings.CutPrefix(request.binaries, "github:")
		if !ok || !pigletrelease.ValidGitHubRepository(repository) {
			return publishNPMRequest{}, false, fmt.Errorf("--binaries %q must be github:<owner>/<repo>", request.binaries)
		}
	}
	return request, false, nil
}

// pig additive (D18): Stock Pig publishes Piglet source to npm through the author's own npm and never handles an npm token.
func runPublishNPM(ctx context.Context, args []string, stdout, stderr io.Writer, deps npmPublishDeps) int {
	request, help, err := parsePublishNPMArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n%s\n", err, publishNPMUsageLine)
		return 2
	}
	if help {
		_, _ = io.WriteString(stdout, publishNPMUsage)
		return 0
	}
	if err := publishNPM(ctx, request, stdout, stderr, deps); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func publishNPM(ctx context.Context, request publishNPMRequest, stdout, stderr io.Writer, deps npmPublishDeps) error {
	if piglet.DistributionOffline() {
		return errors.New("cannot publish a Piglet to npm while offline; unset PIG_OFFLINE and PI_OFFLINE to run npm")
	}
	workspace := request.workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	path, err := resolvePigletPath(request.pigletRef)
	if err != nil {
		return fmt.Errorf("piglet %q: %w", request.pigletRef, err)
	}
	authored, err := piglet.Parse(path)
	if err != nil {
		return fmt.Errorf("piglet %q: %w", request.pigletRef, err)
	}
	var binaries *piglet.NPMBinaries
	var binariesNote string
	if authored.Release != nil && authored.Release.Version != "" && !request.noBinaries {
		if binaries, binariesNote, err = resolveNPMBinaries(ctx, request.binaries, authored.Name, authored.Release.Version, deps.client); err != nil {
			return err
		}
	}
	source, err := piglet.BuildNPMSource(piglet.NPMSourceOptions{
		Path: path, Workspace: workspace, NPMName: request.npmName, PackageMap: request.packageMap, Binaries: binaries,
	})
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(source.Dir) }()

	notes := make([]string, 0, len(source.Packages)+len(source.Warnings)+2)
	for _, alias := range slices.Sorted(maps.Keys(source.Packages)) {
		notes = append(notes, "Package "+alias+": "+source.Packages[alias])
	}
	if binaries != nil {
		notes = append(notes, "Signed Binary: "+binaries.Ref+" (signer "+binaries.Signer+"; "+binariesNote+")")
	} else {
		notes = append(notes, "Signed Binary: none recorded")
	}
	for _, warning := range source.Warnings {
		notes = append(notes, "Warning: "+warning)
	}
	command := deps.command
	if len(command) == 0 {
		if command, err = npmpublish.Command(); err != nil {
			return err
		}
	}
	missing, err := missingNPMPackages(ctx, command, source.Packages)
	if err != nil {
		return err
	}
	if request.flags.Yes && len(missing) > 0 {
		return errors.New(strings.Join(missing, "; ") + "; publish it first, or the published Piglet cannot be added")
	}
	for _, line := range missing {
		notes = append(notes, "Warning: "+line+"; publish it before the Piglet")
	}
	stdin := deps.stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	return npmpublish.Request{
		Package: npmpublish.Package{Name: source.Name, Version: source.Version, Dir: source.Dir, IgnoreScripts: true},
		Flags:   request.flags, Label: "Piglet", Notes: notes, Command: command,
		Next: []string{
			"Add it with: pig piglet add npm:" + source.Name,
			"npm lists it under the keyword " + piglet.NPMKeyword + "; catalogs that search that keyword show it after their next index build.",
		},
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	}.Run(ctx)
}

// missingNPMPackages describes each npm Package of the published Piglet that no version on the registry satisfies, so that a Piglet whose Package is not published is not published either. A Package with its own registry is not checked.
func missingNPMPackages(ctx context.Context, command []string, packages map[string]string) ([]string, error) {
	var missing []string
	for _, alias := range slices.Sorted(maps.Keys(packages)) {
		ref, err := sourceref.Parse(packages[alias], sourceref.Options{Bare: sourceref.BareReject})
		if err != nil || ref.Kind != sourceref.KindNPM || ref.NPMRegistry != "" {
			continue
		}
		ok, err := npmpublish.Satisfiable(ctx, command, ref.NPMName, ref.NPMVer, "")
		if err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, fmt.Sprintf("Package %q (%s) is not on npm", alias, packages[alias]))
		}
	}
	return missing, nil
}

// resolveNPMBinaries returns the signed Binary release to record in the npm package, with a note saying where it came from. An explicit --binaries reads the signed release index from GitHub. Otherwise a release this machine published with --to github is used. Without either, nothing is recorded.
func resolveNPMBinaries(ctx context.Context, explicit, name, version string, client *http.Client) (*piglet.NPMBinaries, string, error) {
	if explicit != "" {
		binaries, err := fetchNPMBinaries(ctx, strings.TrimPrefix(explicit, "github:"), name, version, client)
		return binaries, "read from the signed release index", err
	}
	record, err := readGitHubPublication(name, version)
	if err != nil || record == nil {
		return nil, "", err
	}
	release := pigletrelease.GitHubRelease{Repository: record.Repository, TagPrefix: record.TagPrefix}
	return &piglet.NPMBinaries{Ref: release.Reference(version), Signer: record.Signer}, "from the GitHub release you published", nil
}

// fetchNPMBinaries finds the signed index of name@version in repository, first under the Piglet's own tag namespace and then under unprefixed tags.
func fetchNPMBinaries(ctx context.Context, repository, name, version string, client *http.Client) (*piglet.NPMBinaries, error) {
	if piglet.DistributionOffline() {
		return nil, errors.New("--binaries reads the signed release from GitHub, which needs the network; unset PIG_OFFLINE and PI_OFFLINE")
	}
	var failures []string
	for _, ref := range []string{"github:" + repository + "/" + name + "@" + version, "github:" + repository + "@" + version} {
		verified, err := pigletrelease.FetchIndex(ctx, ref, pigletrelease.Options{Client: client, Version: version})
		if err != nil {
			failures = append(failures, ref+": "+err.Error())
			continue
		}
		if verified.Index.Piglet != name {
			failures = append(failures, ref+": the release is for Piglet "+verified.Index.Piglet)
			continue
		}
		return &piglet.NPMBinaries{Ref: verified.Index.GitHub.Reference(version), Signer: verified.Index.Signer.KeyID}, nil
	}
	return nil, fmt.Errorf("--binaries github:%s: no signed release of %s %s was found; publish it first with `pig piglet publish %s --to github`, or drop --binaries\n  %s", repository, name, version, name, strings.Join(failures, "\n  "))
}

// githubPublication records which GitHub release a Piglet version was published to, so a later npm publication can name its signed Binaries.
type githubPublication struct {
	Repository string `json:"repository"`
	TagPrefix  string `json:"tagPrefix"`
	Signer     string `json:"signer"`
}

func githubPublicationPath(name, version string) (string, error) {
	if name == "" || version == "" || strings.ContainsAny(name+version, `/\`) || name == ".." || version == ".." {
		return "", fmt.Errorf("invalid Piglet release identity %q %q", name, version)
	}
	return filepath.Join(codingagent.ConfigRoot(), "receipts", "piglet-publications", name, version+".json"), nil
}

// recordGitHubPublication writes the publication record after a GitHub release exists.
func recordGitHubPublication(release publishRelease) error {
	path, err := githubPublicationPath(release.piglet.Name, release.version)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(githubPublication{Repository: release.repo, TagPrefix: release.tagPrefix, Signer: release.keyID}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(path), ".publication-*.stage")
	if err != nil {
		return err
	}
	_, writeErr := stage.Write(append(data, '\n'))
	closeErr := stage.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(stage.Name())
		return err
	}
	if err := os.Rename(stage.Name(), path); err != nil {
		_ = os.Remove(stage.Name())
		return err
	}
	return nil
}

// readGitHubPublication returns the record for name@version, or nil when this machine published no such release.
func readGitHubPublication(name, version string) (*githubPublication, error) {
	path, err := githubPublicationPath(name, version)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record githubPublication
	if err := json.Unmarshal(data, &record); err != nil || record.Repository == "" || record.Signer == "" {
		return nil, fmt.Errorf("publication record %s is damaged; delete it or pass --binaries github:<owner/repo>", path)
	}
	return &record, nil
}
