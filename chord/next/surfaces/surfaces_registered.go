package surfaces

import "github.com/MichaelKinsy/PiG/internal/jsstring"

// Ports packages/chord/src/surfaces/registered.ts, its server side (RegisteredSource). Plugin scripts that register themselves
// (slice 11 §1, §2, decision "Slice 11's points" (P2)): a facet's built script served as `chordFacet("<plugin>", "<digest>",
// function (require, module, exports) { … })`, loaded with `<script src>` (a page under a `'self'` CSP: no `eval`, no
// `new Function`) or run by the terminal's runtime. Evaluating a registered function (`evaluateRegistered`, `interop`, the
// RegisteredScript and ClientModules types) happens where the script runs, in the client: it is the vendored TypeScript of
// nextnode/surfaces/client (D601).

// Register is the global function a registered script calls: defined by the client before any plugin script runs (Pi's
// `REGISTER`).
const Register = "chordFacet"

// RegisteredSource is the text of a registered script for plugin's facet built as source (one function expression, the build's
// wrapped CommonJS script) with digest. Its source map comment stays on its own line, before the closing call.
func RegisteredSource(plugin, digest, source string) (string, error) {
	name, err := jsstring.MarshalJSON(plugin)
	if err != nil {
		return "", err
	}
	hash, err := jsstring.MarshalJSON(digest)
	if err != nil {
		return "", err
	}
	return Register + "(" + string(name) + "," + string(hash) + "," + source + "\n);\n", nil
}
