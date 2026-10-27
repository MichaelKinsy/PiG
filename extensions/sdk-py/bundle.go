// SPDX-License-Identifier: MIT

// Package pysdk carries the Python extension SDK sources so an installed pig can
// stage a buildable copy to disk. See extensions/sdk/bundle.go for the rationale;
// this is the Python analogue. The Python SDK is not a Go package, so this small
// Go file colocated with it provides the embed. Staging writes these files under
// <config-root>/state/pigsdk/sdk-py-<hash16>, a directory per build named for
// their content hash, so packed Python cells resolve the SDK on a clean host
// (findPythonSDKRoot's staged-source strategy).
package pysdk

import (
	"io/fs"
	"slices"
)

// bundle_embed.go lists the package's modules, its license and pyproject.toml. A file of 32 KiB or more is embedded as its gzip, <name>.gz, which go generate writes beside it and staging writes back under the name without .gz (coding/extension/pigsdk): the sources are most of the weight the bundle adds to every binary that stages it. go:embed cannot compress, so the list is generated. The unicode property tables (pig_sdk/tui/_tables.py) are the largest of them. TestEveryPackageSourceIsBundled fails when it and the source tree disagree.
//
//go:generate go run ../../automation/gen/embed-bundle.go -pkg pysdk -out bundle_embed.go -gzip-over 32768 LICENSE pyproject.toml pig_sdk/*.py pig_sdk/tui/*.py

// BundledFiles lists the embedded files, relative to the SDK root, in a stable
// order. Paths use forward slashes; a stager recreates subdirectories. A name
// ending in .gz is the gzip of the file the stager writes under the name
// without it.
//
// pig_sdk/__init__.py imports pig_sdk.tui at load, so the subpackage is staged with the package.
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
