package desktop

import (
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// builtinPlugins are the Go plugins of the native client: registered scripts whose default export is the plugin's facet,
// the form the web and terminal clients' plugins take. The sessions screen and the others register theirs here as they
// land; each also appears in the set the server publishes for the desktop surface (surfacehost.SetOf).
var builtinPlugins []surfaces.RegisteredScript

// builtinModules are the host packages the builtin plugins' require answers.
var builtinModules = surfaces.ClientModules{}

// preferredLanguages are the device's languages, from the environment's locale variables (most specific first).
func preferredLanguages() []string {
	var tags []string
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value := os.Getenv(name)
		if value == "" || value == "C" || value == "POSIX" {
			continue
		}
		value, _, _ = strings.Cut(value, ".")
		value, _, _ = strings.Cut(value, "@")
		tags = append(tags, strings.ReplaceAll(value, "_", "-"))
	}
	return tags
}
