// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package rssdk carries the Rust extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Rust analogue. The Rust SDK is a cargo crate, not a Go package, so
// this small Go file colocated with it provides the embed. Staging writes these
// files under <config-root>/state/pigsdk/sdk-rs so packed Rust cells resolve
// the SDK on a clean host (findRustSDKRoot's staged-source strategy).
package rssdk

import "embed"

//go:embed LICENSE Cargo.toml Cargo.lock src/lib.rs src/context.rs src/extension.rs src/login.rs src/oauth.rs src/protocol.rs src/transport.rs
var Source embed.FS

// BundledFiles lists the embedded files, relative to the crate root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories.
func BundledFiles() []string {
	return []string{"LICENSE", "Cargo.toml", "Cargo.lock", "src/lib.rs", "src/context.rs", "src/extension.rs", "src/login.rs", "src/oauth.rs", "src/protocol.rs", "src/transport.rs"}
}
