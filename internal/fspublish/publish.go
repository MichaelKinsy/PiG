// Package fspublish makes a fully written stage file visible at its final path without replacing a file that is already there.
package fspublish

import "os"

// link is os.Link; tests replace it to refuse links.
var link = os.Link

// Publish makes stage visible at target and fails, leaving target untouched, when target exists. It links stage to target. Where the file system refuses hard links, as Android does in an app's private data directory (Termux), it renames stage onto target with a rename that refuses to replace an existing file. After a link stage still exists and the caller removes it; after a rename it does not.
func Publish(stage, target string) error {
	err := link(stage, target)
	if err == nil || !linkRefused(err) {
		return err
	}
	return renameNoReplace(stage, target, err)
}
