package extension

// Settings is the effective settings object [API.GetSettings] returns: the JSON object of the global and project settings merged, with overrides. It is a copy, so a change to it does not change the settings.
//
// Go mechanic (not a divergence): the settings manager lives in a package that imports this one, so the extension boundary carries the object as decoded JSON, the way the subprocess wire does.
//
// upstream: .upstream/v0.99.1/packages/coding-agent/src/core/settings-manager.ts (Settings)
type Settings = map[string]any
