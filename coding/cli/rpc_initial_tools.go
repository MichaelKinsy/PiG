package cli

import "github.com/MichaelKinsy/PiG/coding"

// rpcSetInitialActiveTools selects the startup loadout without shrinking the Session registry.
func rpcSetInitialActiveTools(session *coding.Session, names []string) {
	seen := make(map[string]struct{}, len(names))
	var selected []string
	for _, name := range names {
		if _, duplicate := seen[name]; !duplicate {
			selected = append(selected, name)
			seen[name] = struct{}{}
		}
	}
	session.SetActiveToolsByName(selected)
}
