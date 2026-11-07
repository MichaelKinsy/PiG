// SPDX-License-Identifier: MIT

// Package pysdk carries the Python extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Python analogue. The Python SDK is not a Go package, so this small
// Go file colocated with it provides the embed. Staging writes these files under
// <config-root>/state/pigsdk/sdk-py-<hash16>, a directory per build named for
// their content hash, so packed Python cells resolve the SDK on a clean host
// (findPythonSDKRoot's staged-source strategy).
package pysdk

import "slices"

// The bundle is one file, sdk.zst: the package's modules, its license and pyproject.toml as one zstd frame, in the layout internal/sdkpack documents. bundle_pack.go (go generate, automation/gen/packsdk) embeds it as Source and lists its files and their staging hash; coding/extension/pigsdk decodes it only when it stages the SDK. pig_sdk/__init__.py imports pig_sdk.tui at load, so the subpackage is staged with the package. TestEveryPackageSourceIsBundled fails when the list and the source tree disagree.
//
//go:generate go run ../../automation/gen/packsdk -pkg pysdk LICENSE pyproject.toml pig_sdk/*.py pig_sdk/tui/*.py

// BundledFiles lists the bundled files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories. The
// returned slice is the caller's to modify.
func BundledFiles() []string { return slices.Clone(bundledFiles) }

// StagedHash is the hash a staged copy is named and marked by: what coding/extension/pigsdk computes from the bundled files' names, sizes and contents.
func StagedHash() string { return stagedHash }
