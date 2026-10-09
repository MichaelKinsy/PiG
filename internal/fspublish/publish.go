// Package fspublish makes a file available at a new path without replacing a file that is already there: a fully written stage at its final path (Publish), or an existing file at a second path (Duplicate).
package fspublish

import "os"

// link is os.Link; tests replace it to refuse links.
var link = os.Link

// Publish makes stage visible at target and fails, leaving target untouched, when target exists. It links stage to target. Where hard links are refused, as Android's SELinux policy refuses them in an app's private data directory (Termux), it renames stage onto target with a rename that refuses to replace an existing file. After a link, stage still exists and the caller removes it; after a rename, it does not.
//
// pig additive (D18): Piglet sources, scripts, records, and Binaries publish through this on Android, where Termux refuses their hard links.
func Publish(stage, target string) error {
	err := link(stage, target)
	if err == nil || !linkRefused(err) {
		return err
	}
	return renameNoReplace(stage, target, err)
}
