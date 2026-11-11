package hosts

// Optional server defaults for every TUI visitor (`<data dir>/tui.json`): keybindings, a theme setting and display settings, below each visitor's own launcher
// preferences (section 4.2). Never privileged bindings, credentials or environment; unknown and invalid entries are dropped.
//
// pi: apps/p2/src/hosts/tui-preferences.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// tuiPreferencesFacet serves prefs.TuiPreferences from dataDir/tui.json.
func tuiPreferencesFacet(dataDir string) host.Facet {
	return p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(prefs.TuiPreferences, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, _ host.Self) (contracts.Implementation, error) {
			who, err := p2runtime.FromChord(principal)
			if err != nil || (!p2runtime.IsOwner(who) && !p2runtime.IsGuest(who)) {
				return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
			}
			return contracts.Implementation{Variant: "viewer", Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"read": func(_ delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						var configured any
						text, err := os.ReadFile(filepath.Join(dataDir, "tui.json"))
						switch {
						case errors.Is(err, fs.ErrNotExist):
						case err != nil:
							return nil, err
						case len(text) > prefs.MaxPreferenceFile:
							return nil, errors.New("tui.json exceeds 256 KiB")
						default:
							if err := json.Unmarshal(text, &configured); err != nil {
								return nil, fmt.Errorf("tui.json: %w", err)
							}
						}
						encoded, err := json.Marshal(prefs.SanitizeServerPreferences(configured))
						if err != nil {
							return nil, err
						}
						return delta.DecodeJson(encoded)
					},
				},
			}}, nil
		}), host.ServeOptions{})
	})
}
