// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts
package codingagent

// shouldShowStartupHeader reports whether the startup header (logo, version, key hints) is shown. Only quietStartup true hides it, and verbose overrides it (interactive-mode.ts:1409-1412).
func (m *InteractiveMode) shouldShowStartupHeader() bool {
	return m.opts.Verbose || m.opts.Settings.GetQuietStartup() != QuietStartupTrue
}

// shouldShowStartupDetails reports whether the startup details (model scope, loaded resources) are shown. Quiet startup true and "header" hide them, and verbose overrides it (interactive-mode.ts:1414-1417).
func (m *InteractiveMode) shouldShowStartupDetails() bool {
	return m.opts.Verbose || m.opts.Settings.GetQuietStartup() == QuietStartupFalse
}
