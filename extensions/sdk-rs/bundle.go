// SPDX-License-Identifier: MIT

// Package rssdk carries the Rust extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Rust analogue. The Rust SDK is a cargo crate, not a Go package, so
// this small Go file colocated with it provides the embed. Staging writes these
// files under <config-root>/state/pigsdk/sdk-rs-<hash16>, a directory per build
// named for their content hash, so packed Rust cells resolve the SDK on a clean
// host (findRustSDKRoot's staged-source strategy).
package rssdk

import (
	"io/fs"
	"slices"
)

// bundle_embed.go lists the crate's files without its unit-test modules (src/*tests.rs, each declared under #[cfg(test)], which cargo does not load in a build): go:embed cannot leave files out of a directory, so the list is generated. TestBundledFilesMatchRustSourceTree fails when it and the source tree disagree.
//
//go:generate go run ../../automation/gen/embed-bundle.go -pkg rssdk -out bundle_embed.go -skip tests.rs -skip bundle.go -skip bundle_embed.go -walk src LICENSE Cargo.toml Cargo.lock

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
