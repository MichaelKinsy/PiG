package cli

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// currentPigletBinary is the executable's Piglet Binary state. Startup and the pre-session commands read it; tests replace it.
var currentPigletBinary = runningPigletBinary

// pigletBinaryClosed reports whether the executable is a Piglet Binary built with build.closed (D202): one that runs only what its
// signed closure lists.
func pigletBinaryClosed(binary pigletBinary) bool {
	return binary.Is && binary.OverlayBase() == nil
}

// pigletBinaryClosedRefusal words the one message every closed-Binary refusal prints (D202): it names what was refused, the Binary and
// build.closed, and points at the Piglet setting and at stock pig, as the overlay refusals do.
func pigletBinaryClosedRefusal(binary pigletBinary, refused string) string {
	return fmt.Sprintf("pig: this Piglet Binary (%s) was built with build.closed: true, so it runs only what its signed closure lists and does not accept %s; rebuild its Piglet without build.closed to accept it, or use stock pig", binary.Name(), refused)
}

// pigletBinaryExtensionFlagRefusal returns the refusal for -e/--extension (a path, npm: or git: source) on a closed Piglet Binary, or ""
// when the run may go on.
func pigletBinaryExtensionFlagRefusal(binary pigletBinary, extensions []string) string {
	if len(extensions) == 0 || !pigletBinaryClosed(binary) {
		return ""
	}
	return pigletBinaryClosedRefusal(binary, "-e/--extension")
}

// pigletBinarySettingsExtensionsRefusal returns the notice for a settings.extensions entry in the user or project settings of a closed
// Piglet Binary, or "". The Binary loads none of them (its ambient discovery is empty); the notice says so.
func pigletBinarySettingsExtensionsRefusal(binary pigletBinary, global, project []string) string {
	if (len(global) == 0 && len(project) == 0) || !pigletBinaryClosed(binary) {
		return ""
	}
	return pigletBinaryClosedRefusal(binary, "settings.extensions") + " (the entries are ignored)"
}

// pigletBinaryPackageCommandRefusal returns the refusal for `pig install`, `pig remove` and the updates that touch installed packages
// (`--extensions`, `--all`, `--extension <source>` or a source) on a closed Piglet Binary, or "". Updating pig itself and refreshing
// the model catalogs stay open.
func pigletBinaryPackageCommandRefusal(binary pigletBinary, opts *packageCLIOptions) string {
	if opts == nil || !pigletBinaryClosed(binary) {
		return ""
	}
	switch opts.command {
	case packageInstall:
		return pigletBinaryClosedRefusal(binary, "pig install")
	case packageRemove:
		return pigletBinaryClosedRefusal(binary, "pig remove")
	case packageUpdate:
		if opts.extensionsOnly || opts.allPackages || opts.extensionSource != "" || (opts.source != "" && !isSelfUpdateTarget(opts.source)) {
			return pigletBinaryClosedRefusal(binary, "pig update --extensions")
		}
	}
	return ""
}

// pigletBinaryMcpRefusal returns the refusal for `pig mcp add` on a closed Piglet Binary, or "".
func pigletBinaryMcpRefusal(binary pigletBinary, args []string) string {
	if len(args) < 2 || args[0] != "mcp" || args[1] != "add" || !pigletBinaryClosed(binary) {
		return ""
	}
	return pigletBinaryClosedRefusal(binary, "pig mcp add")
}

// pigletBinaryMcpConfigRefusal is the message a closed Piglet Binary reports in place of the servers of the user and project mcp.json.
func pigletBinaryMcpConfigRefusal(binary pigletBinary) string {
	if !pigletBinaryClosed(binary) {
		return ""
	}
	return pigletBinaryClosedRefusal(binary, "mcp.json (user or project)") + " (its servers are not started)"
}

// projectSettingsExtensions returns the project settings' extensions when the project is trusted, else nil.
func projectSettingsExtensions(sm *codingagent.SettingsManager, projectTrusted bool) []string {
	if !projectTrusted {
		return nil
	}
	return sm.GetProjectSettings().Extensions
}
