// Ports packages/coding-agent/src/config.ts
package codingagent

import "fmt"

// GetSelfUpdateCommand builds a command only for the proven package-manager owner. An unconfigured npm command retains the prefix of its proven lib/node_modules root.
func (p *SelfUpdateProvenance) GetSelfUpdateCommand(npmCommand []string, target SelfUpdatePackageTarget) *SelfUpdateCommand {
	// pig divergence (D39): native provenance selects the one mutation owner before command construction.
	if p == nil || p.Tier != TierPackageManager {
		return nil
	}
	command := npmCommand
	if p.PackageOwner == ownerNPM && len(command) == 0 && p.NpmPrefix != "" {
		command = []string{"npm", "--prefix", p.NpmPrefix}
	}
	return PackageManagerUpdateCommand(p.PackageOwner, p.PackageName, command, target)
}

// GetSelfUpdateUnavailableInstruction reports a concrete native ownership or permission failure instead of inventing an unproven package-manager command.
func (p *SelfUpdateProvenance) GetSelfUpdateUnavailableInstruction() string {
	// pig divergence (D39): native ownership diagnostics identify the running executable and its owning directory.
	if p == nil {
		return UnsupportedRemediation("")
	}
	if p.Tier == TierUnsupported && p.PackageOwner != "" && p.PackageDir != "" {
		return fmt.Sprintf("Cannot update %s automatically: the install path is not writable: %s.\n%s", p.PackageName, p.PackageDir, UnsupportedRemediation(p.ExePath))
	}
	return UnsupportedRemediation(p.ExePath)
}
