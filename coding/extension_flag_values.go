package coding

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// ApplyExtensionFlagValues checks ServicesOptions.ExtensionFlagValues against the flags the loaded extensions registered and
// returns the values that extensions read. A boolean flag is true whatever the value; a string flag takes its string
// value, and a non-string value is an error ("requires a value"); a name no extension registered is reported with the other unknown names in one
// "Unknown option" error. The diagnostics are also appended to [Services.Diagnostics]. Ports
// packages/coding-agent/src/core/agent-session-services.ts applyExtensionFlagValues.
func (s *AgentSessionServices) ApplyExtensionFlagValues(extensions []extension.Extension) (map[string]any, []AgentSessionRuntimeDiagnostic) {
	if s.extensionFlagValues == nil {
		return nil, nil
	}
	registered := map[string]extension.FlagType{}
	for _, ext := range extensions {
		for name, flag := range ext.Flags {
			registered[name] = flag.Type
		}
	}
	values := map[string]any{}
	var diagnostics []AgentSessionRuntimeDiagnostic
	var unknown []string
	for _, entry := range s.extensionFlagValues {
		name := entry.Name
		flagType, ok := registered[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if flagType == extension.FlagBoolean {
			values[name] = true
			continue
		}
		if value, isString := entry.Value.(string); isString {
			values[name] = value
			continue
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf("Extension flag \"--%s\" requires a value", name)})
	}
	if len(unknown) > 0 {
		plural := "s"
		if len(unknown) == 1 {
			plural = ""
		}
		names := make([]string, len(unknown))
		for i, name := range unknown {
			names[i] = "--" + name
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{Type: "error", Message: "Unknown option" + plural + ": " + strings.Join(names, ", ")})
	}
	s.diagnostics = append(s.diagnostics, diagnostics...)
	return values, diagnostics
}
