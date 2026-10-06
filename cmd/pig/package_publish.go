package main

// pig additive (D18): `pig package publish` is optional sugar over `npm publish`
// for a Package. It validates the Package and the fields the pi-in-go.dev catalog
// reads, shows a dry run by default, and never edits the author's package.json.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/npmpublish"
)

// packageKeyword is the npm keyword that lists a package as a PiG Package. Pi's gallery finds Pi packages by `pi-package`.
const packageKeyword = "pig-package"

const packagePublishUsageLine = "Usage: pig package publish [<dir>] --to npm [--yes|--dry-run] [--tag <dist-tag>] [--access public|restricted] [--otp <code>] [--no-input]"

const packagePublishUsage = packagePublishUsageLine + `

Optional sugar over plain ` + "`npm publish`" + ` for the Package in <dir> (default: the current
directory). PiG validates the Package, checks that package.json has the fields the
pi-in-go.dev catalog reads (name, version, description, and the pig-package keyword),
checks that name@version is free on npm, and shows what npm would publish. Without
--yes it is a dry run. With --yes it runs npm publish in the Package directory, exactly
as you would, so scripts such as prepublishOnly and .npmrc apply. npm does the
authentication, and PiG never reads or stores an npm token. PiG does not edit
package.json.

Plain ` + "`npm publish`" + ` stays the supported way to publish a Package; this command only
adds the checks. Set ` + npmpublish.TrustedPublishingEnv + ` (GitHub Actions with id-token: write) to
publish with provenance and no stored credential.
`

type packagePublishRequest struct {
	dir   string
	flags npmpublish.Flags
}

func parsePackagePublishArgs(args []string) (packagePublishRequest, bool, error) {
	var request packagePublishRequest
	help, destination := false, ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if handled, err := request.flags.Parse(args, &i); handled || err != nil {
			if err != nil {
				return packagePublishRequest{}, false, err
			}
			continue
		}
		name, inline, hasInline := strings.Cut(arg, "=")
		switch {
		case arg == "-h" || arg == "--help":
			help = true
		case arg == "--no-input":
		case name == "--to":
			switch {
			case hasInline && inline != "":
				destination = inline
			case !hasInline && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
				i++
				destination = args[i]
			default:
				return packagePublishRequest{}, false, errors.New("--to requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return packagePublishRequest{}, false, fmt.Errorf("unknown option %q", arg)
		case request.dir == "":
			request.dir = arg
		default:
			return packagePublishRequest{}, false, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	if help {
		return request, true, nil
	}
	switch {
	case destination == "":
		return packagePublishRequest{}, false, errors.New("--to is required; use --to npm")
	case destination != "npm":
		return packagePublishRequest{}, false, fmt.Errorf("unsupported Package publish destination %q; use --to npm", destination)
	}
	if err := request.flags.Validate(); err != nil {
		return packagePublishRequest{}, false, err
	}
	if request.dir == "" {
		request.dir = "."
	}
	return request, false, nil
}

func runPackagePublish(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	request, help, err := parsePackagePublishArgs(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n%s\n", err, packagePublishUsageLine)
		return 2
	}
	if help {
		_, _ = io.WriteString(stdout, packagePublishUsage)
		return 0
	}
	if err := publishPackage(ctx, request, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func publishPackage(ctx context.Context, request packagePublishRequest, stdout, stderr io.Writer) error {
	root, err := filepath.Abs(request.dir)
	if err != nil {
		return err
	}
	resources, err := packagecontent.ValidatePackage(root)
	if err != nil {
		return fmt.Errorf("Package invalid: %w", err)
	}
	manifest, err := readPublishableManifest(root)
	if err != nil {
		return err
	}
	notes := []string{}
	for _, count := range []struct {
		kind string
		n    int
	}{
		{"extensions", len(resources.ExtensionEntries)}, {"skills", len(resources.SkillDirs)}, {"prompts", len(resources.PromptFiles)},
		{"themes", len(resources.ThemeFiles)}, {"agents", len(resources.AgentFiles)}, {"hooks", len(resources.HookFiles)},
		{"mcpServers", len(resources.MCPFiles)}, {"agentEnvironments", len(resources.AgentEnvironments)},
	} {
		if count.n > 0 {
			notes = append(notes, fmt.Sprintf("  %s: %d", count.kind, count.n))
		}
	}
	if len(manifest.Repository) == 0 {
		notes = append(notes, "Warning: package.json has no repository; catalogs link a Package to its source through it")
	}
	return npmpublish.Request{
		Package: npmpublish.Package{Name: manifest.Name, Version: manifest.Version, Dir: root, InPlace: true},
		Flags:   request.flags, Label: "Package", Notes: notes,
		Next: []string{
			"Install it with: pig install npm:" + manifest.Name,
			"npm lists it under the keyword " + packageKeyword + "; catalogs that search that keyword show it after their next index build.",
		},
		Stdin: os.Stdin, Stdout: stdout, Stderr: stderr,
	}.Run(ctx)
}

type publishableManifest struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Keywords    []string        `json:"keywords"`
	Private     bool            `json:"private"`
	Repository  json.RawMessage `json:"repository"`
}

// readPublishableManifest checks the package.json fields npm requires and the catalog reads. It never writes the file.
func readPublishableManifest(root string) (publishableManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return publishableManifest{}, fmt.Errorf("a Package is published from its package.json: %w", err)
	}
	var manifest publishableManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return publishableManifest{}, fmt.Errorf("package.json is not valid JSON: %w", err)
	}
	switch {
	case manifest.Private:
		return publishableManifest{}, errors.New("package.json is private, and npm refuses to publish a private package; remove \"private\" to publish it")
	case manifest.Name == "":
		return publishableManifest{}, errors.New("package.json needs a name")
	case !sourceref.ValidNPMPackageName(manifest.Name):
		return publishableManifest{}, fmt.Errorf("package.json name %q is not an npm package name (lowercase, optionally scoped as @scope/name)", manifest.Name)
	case manifest.Version == "":
		return publishableManifest{}, errors.New("package.json needs a version")
	case !sourceref.ValidNPMVersion(manifest.Version):
		return publishableManifest{}, fmt.Errorf("package.json version %q is not a version npm publishes; use a full semantic version such as 1.2.0", manifest.Version)
	case strings.TrimSpace(manifest.Description) == "":
		return publishableManifest{}, errors.New("package.json needs a description; the catalog shows it on the Package's card")
	case !slices.Contains(manifest.Keywords, packageKeyword):
		return publishableManifest{}, fmt.Errorf("package.json keywords lack %q; add it so catalogs list the Package, then run this again (PiG does not edit package.json)", packageKeyword)
	}
	return manifest, nil
}
