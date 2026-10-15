// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Package upstreampackages is the one list of the upstream monorepo packages that the parity tools account for. packages.json is read by the Go tools, automation/ci/check-port-map-drift.py and the interface extractor; a test requires it to equal the package directories of the upstream mirror, so a package that upstream adds fails every gate until it is listed.
package upstreampackages

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
)

//go:embed packages.json
var packagesJSON []byte

// Package is one upstream monorepo package.
type Package struct {
	// Key is the directory name under packages/.
	Key string `json:"key"`
	// Name is the npm package name.
	Name string `json:"name"`
	// Root is the package directory in the monorepo, for example packages/agent.
	Root string `json:"root"`
}

// SourceRoot is the directory PORT_MAP keys on, for example packages/agent/src.
func (p Package) SourceRoot() string { return path.Join(p.Root, "src") }

// All returns every listed package ordered by key.
func All() []Package {
	var list struct {
		Packages []Package `json:"packages"`
	}
	if err := json.Unmarshal(packagesJSON, &list); err != nil {
		panic(fmt.Sprintf("upstreampackages: packages.json: %v", err))
	}
	sort.Slice(list.Packages, func(i, j int) bool { return list.Packages[i].Key < list.Packages[j].Key })
	return list.Packages
}

// SourceRoots returns the SourceRoot of every package.
func SourceRoots() []string {
	all := All()
	roots := make([]string, len(all))
	for i, p := range all {
		roots[i] = p.SourceRoot()
	}
	return roots
}

// ByName maps each npm package name to its package.
func ByName() map[string]Package {
	all := All()
	byName := make(map[string]Package, len(all))
	for _, p := range all {
		byName[p.Name] = p
	}
	return byName
}
