// Package fusepack holds Go extensions fused into a Piglet Binary. The build
// copies compatible extension source under exts/ and generates
// registry_generated.go with lazy factory serve functions. Register installs a
// FusedResolver so the host loads those extensions in-process (D31).
//
// Stock Pig compiles this package with an empty registry, so Register is a no-op.
package fusepack

import (
	"encoding/base64"
	"fmt"
	"net"
	"sort"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// fusedServes maps an extension name to its in-process serve func. Populated by
// generated code in registry_generated.go via registerFactory; empty in stock Pig.
var fusedServes = map[string]func(net.Conn) error{}

// registerFactory records one fused extension factory without constructing the
// extension. Construction is deferred until the host actually loads a session,
// so pre-session CLI commands and version/build operations have no extension
// initialization side effects.
func registerFactory(name string, factory sdk.Factory) {
	fusedServes[name] = func(conn net.Conn) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("construct fused extension %q: %v", name, recovered)
			}
			_ = conn.Close()
		}()
		// The connection is the factory's own while it runs, so a factory's pi.events.emit reaches the host at once, as a subprocess extension's does.
		var built bool
		err = sdk.ServeFactory(conn, func() *sdk.Extension {
			ext := factory()
			built = ext != nil
			return ext
		})
		if !built && err != nil {
			return fmt.Errorf("construct fused extension %q: factory returned nil", name)
		}
		return err
	}
}

type resolver struct{}

func (resolver) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	serve, ok := fusedServes[cfg.Name]
	return serve, ok
}

// Register installs the fused-extension resolver if this Piglet Binary compiled any in.
// No-op in stock pig (empty map), so the subprocess cell path is unaffected.
func Register() {
	if len(fusedServes) == 0 {
		return
	}
	subprocess.SetFusedResolver(resolver{})
	sdk.SetHostHelpers(HostHelpers())
}

// HostHelpers are the host's parsers and file mutation queue for a fused extension. Its factory runs in this process, before any connection to the host exists, so
// the SDK's frontmatter and Date.parse helpers call them directly, and its withFileMutationQueue is the process-wide queue the built-in edit and write tools use.
func HostHelpers() *sdk.HostHelpers {
	return &sdk.HostHelpers{
		ParseFrontmatter: func(content string) (sdk.ParsedFrontmatter, error) {
			frontmatter, body, err := subprocess.ParseFrontmatterJSON(content)
			return sdk.ParsedFrontmatter{Frontmatter: frontmatter, Body: body}, err
		},
		ParseDate: subprocess.ParseDateMs,
		GenerateDiffString: func(oldContent, newContent string, contextLines *int) sdk.DiffString {
			result := subprocess.GenerateDiffString(oldContent, newContent, contextLines)
			return sdk.DiffString{Diff: result.Diff, FirstChangedLine: result.FirstChangedLine}
		},
		GenerateUnifiedPatch: subprocess.GenerateUnifiedPatch,
		CopyToClipboard:      subprocess.CopyToClipboard,
		ConvertToPng: func(data, mimeType string) (sdk.ConvertedImage, bool) {
			result := subprocess.ConvertToPng(data, mimeType)
			if result.Image == nil {
				return sdk.ConvertedImage{}, false
			}
			return sdk.ConvertedImage{Data: result.Image.Data, MimeType: result.Image.MimeType}, true
		},
		ResizeImage: func(input []byte, mimeType string, options *sdk.ImageResizeOptions) (sdk.ResizedImage, bool, error) {
			args := subprocess.ResizeImageArgs{Data: base64.StdEncoding.EncodeToString(input), MimeType: mimeType}
			if options != nil {
				args.Options = &struct {
					MaxWidth    int `json:"maxWidth,omitempty"`
					MaxHeight   int `json:"maxHeight,omitempty"`
					MaxBytes    int `json:"maxBytes,omitempty"`
					JPEGQuality int `json:"jpegQuality,omitempty"`
				}{options.MaxWidth, options.MaxHeight, options.MaxBytes, options.JPEGQuality}
			}
			result, err := subprocess.ResizeImage(args)
			if err != nil || result.Image == nil {
				return sdk.ResizedImage{}, false, err
			}
			image := result.Image
			return sdk.ResizedImage{Data: image.Data, MimeType: image.MimeType, OriginalWidth: float64(*image.OriginalWidth), OriginalHeight: float64(*image.OriginalHeight), Width: float64(*image.Width), Height: float64(*image.Height), WasResized: *image.WasResized}, true, nil
		},
		BuiltinToolDefinition: subprocess.BuiltinToolDefinitionJSON,
		HighlightCode:         subprocess.HighlightCodeLines,
		LoadTheme:             subprocess.LoadThemeJSON,
		WithFileMutationQueue: func(filePath string, fn func() error) error {
			_, err := tools.WithFileMutationQueue(filePath, func() (struct{}, error) { return struct{}{}, fn() })
			return err
		},
	}
}

// FusedConfigs returns a name-only ExtConfig for each fused extension so the
// startup loader includes them even though their source is not on this machine.
// LoadAll routes these to LoadInProcess (no source needed). A richer piglet or
// CLI config for the same name still wins the config merge. Empty in stock pig.
func FusedConfigs() []subprocess.ExtConfig {
	names := make([]string, 0, len(fusedServes))
	for name := range fusedServes {
		names = append(names, name)
	}
	sort.Strings(names)
	cfgs := make([]subprocess.ExtConfig, 0, len(names))
	for _, name := range names {
		cfgs = append(cfgs, subprocess.ExtConfig{Name: name, Enabled: true})
	}
	return cfgs
}
