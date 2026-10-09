// SPDX-License-Identifier: MIT

// Package rssdk carries the Rust extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Rust analogue. The Rust SDK is a cargo crate, not a Go package, so
// this small Go file colocated with it provides the embed. Staging writes these
// files under <config-root>/state/pigsdk/sdk-rs so packed Rust cells resolve
// the SDK on a clean host (findRustSDKRoot's staged-source strategy).
package rssdk

import (
	"embed"
	"io/fs"
	"slices"
)

//go:embed LICENSE Cargo.toml Cargo.lock src
var Source embed.FS

// BundledFiles lists the embedded files, relative to the crate root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories.
func BundledFiles() []string {
	var files []string
	err := fs.WalkDir(Source, ".", func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		panic(err)
	}
	slices.Sort(files)
	return files
}
