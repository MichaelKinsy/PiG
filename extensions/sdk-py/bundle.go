// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package pysdk carries the Python extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Python analogue. The Python SDK is not a Go package, so this small
// Go file colocated with it provides the embed. Staging writes these files under
// <config-root>/state/pigsdk/sdk-py so packed Python cells resolve the SDK
// on a clean host (findPythonSDKRoot's staged-source strategy).
package pysdk

import "embed"

//go:embed LICENSE pig_sdk/__init__.py pyproject.toml
var Source embed.FS

// BundledFiles lists the embedded files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories.
func BundledFiles() []string {
	return []string{"LICENSE", "pig_sdk/__init__.py", "pyproject.toml"}
}
