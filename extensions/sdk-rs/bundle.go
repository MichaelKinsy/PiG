// SPDX-License-Identifier: MIT

// Package rssdk carries the Rust extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Rust analogue. The Rust SDK is a cargo crate, not a Go package, so
// this small Go file colocated with it provides the embed. Staging writes these
// files under <config-root>/state/pigsdk/sdk-rs-<hash16>, a directory per build
// named for their content hash, so packed Rust cells resolve the SDK on a clean
// host (findRustSDKRoot's staged-source strategy).
package rssdk

import "slices"

// The bundle is one file, sdk.zst: the crate's files without its unit-test modules (src/*tests.rs, each declared under #[cfg(test)], which cargo does not load in a build) as one zstd frame, in the layout internal/sdkpack documents. The unicode property tables (src/tui/tables.rs) are the largest of them. bundle_pack.go (go generate, automation/gen/packsdk) embeds the frame as Source and lists its files and their staging hash; coding/extension/pigsdk decodes it only when it stages the SDK. TestBundledFilesMatchRustSourceTree fails when the list and the source tree disagree.
//
//go:generate go run ../../automation/gen/packsdk -pkg rssdk -skip tests.rs -skip bundle.go -skip bundle_pack.go -walk src LICENSE Cargo.toml Cargo.lock

// BundledFiles lists the bundled files, relative to the crate root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories. The
// returned slice is the caller's to modify.
func BundledFiles() []string { return slices.Clone(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what coding/extension/pigsdk computes from the bundled files' names, sizes and contents.
func StagedHash() string { return stagedHash }
