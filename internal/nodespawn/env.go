// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-FileCopyrightText: Copyright Node.js contributors
// SPDX-License-Identifier: MIT

package nodespawn

import (
	"cmp"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// caseInsensitiveEnv reports that environment names ignore case, as on
// Windows.
const caseInsensitiveEnv = runtime.GOOS == "windows"

// ProcessEnv returns the entries of {...process.env} as Node builds it, in
// its key order: the names of PiG's environment block in block order, each
// once, with process.env's value for it. On Windows process.env skips the
// hidden names that start with "=", such as the per-drive "=C:", and reads a
// value case-insensitively (src/node_env_var.cc), so a name that appears in
// two casings has the first one's value.
func ProcessEnv() []string {
	environ := os.Environ()
	pairs := make([]string, 0, len(environ))
	seen := make(map[string]bool, len(environ))
	for _, entry := range environ {
		if caseInsensitiveEnv && strings.HasPrefix(entry, "=") {
			continue
		}
		name, _, ok := strings.Cut(entry, "=")
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		value, _ := os.LookupEnv(name)
		pairs = append(pairs, name+"="+value)
	}
	return pairs
}

// EnvProperty is one property of the object that Node's spawn takes as its env
// option. Unlike an entry of SetEnv, its name may contain "=".
type EnvProperty struct {
	Name, Value string
}

// EnvProperties is the properties that entries describe, each entry split at
// its first "=" (after a leading one) as SetEnv splits it.
func EnvProperties(entries []string) []EnvProperty {
	properties := make([]EnvProperty, len(entries))
	for i, entry := range entries {
		properties[i].Name, properties[i].Value = envName(entry)
	}
	return properties
}

// SetEnv is SetEnvProperties for entries of the form name=value, where the
// name ends at the first "=": an entry cannot describe a property whose name
// contains one.
func SetEnv(cmd *exec.Cmd, env []string) {
	SetEnvProperties(cmd, EnvProperties(env))
}

// SetEnvProperties gives cmd the environment block of a child that Node's
// spawn starts with the env option whose properties env holds, in the object's
// key order: a later property of the same name replaces the value in place, as
// an object property assignment does. Call it before SetProgram, which
// validates and searches that environment, and start cmd with Start.
//
// libuv writes each property into the block as "name=value", so two names that
// share their text up to the first "=" both reach the child. os/exec keeps only
// the last of such entries, comparing the text regardless of case on Windows
// (sharesEnvKey). Outside Windows, SetProgram starts such a child as os/exec
// cannot, as useTrampoline describes. On Windows, Start does.
//
// On Windows, Node's normalizeSpawnArguments (lib/child_process.js) sorts the
// names by UTF-16 code unit and keeps the first of the names whose
// toUpperCase values are equal, so "PATH" wins over "Path" and "Path" over
// "path". toUpperCase applies Unicode's full, locale-independent case
// mapping, so "SS" also wins over "ß". libuv's make_program_env
// (src/win/process.c) then orders the entries and adds libuv's required
// variables, as programEnv describes. cmd.Env holds that block. Elsewhere the
// child gets the entries in order.
//
// On Windows the child still gets the block in os/exec's order, which Start
// gives cmd.Env (createEnvBlockOrder). os/exec compares the text before each
// entry's first "=" byte by byte in UTF-8, with ASCII letters uppercased;
// libuv compares it by UTF-16 code unit after CompareStringOrdinal uppercases
// every letter. The two orders agree on names made of ASCII, and Start keeps
// libuv's order among the entries that os/exec compares equal, such as
// "A=one" and "A=B=two", so the child's getenv reads the value it reads under
// Node for every ASCII name. They disagree on some names with other letters:
// libuv puts PIG_é (U+00E9, which it uppercases to U+00C9) before PIG_Ê
// (U+00CA), and PIG_😀 (UTF-16 D83D DE00) before PIG_Ａ (U+FF21), while
// os/exec puts both pairs the other way round. Such variables reach the child
// in os/exec's order. A child sees that difference only when it lists its
// environment, or when two entries share a name only through the case of a
// letter outside ASCII, such as the names "PIG_é=1" and "PIG_É=2", and getenv
// reads the first of them.
func SetEnvProperties(cmd *exec.Cmd, env []EnvProperty) {
	properties := appendCoverageEnv(objectProperties(env))
	if caseInsensitiveEnv {
		slices.SortStableFunc(properties, func(a, b EnvProperty) int {
			return compareUTF16(a.Name, b.Name)
		})
		upperCase := cases.Upper(language.Und)
		seen := make(map[string]bool, len(properties))
		properties = slices.DeleteFunc(properties, func(property EnvProperty) bool {
			upper := upperCase.String(property.Name)
			if seen[upper] {
				return true
			}
			seen[upper] = true
			return false
		})
	}
	pairs := make([]string, len(properties))
	for i, property := range properties {
		pairs[i] = property.Name + "=" + property.Value
	}
	cmd.Env = programEnv(pairs)
}

// sharesEnvKey reports that os/exec would drop an entry of env: its
// dedupEnvCase (os/exec/exec.go) keeps only the last of the entries whose text
// up to the first "=" is equal, where that "=" is searched for after a leading
// one, and on Windows it lowercases that text (strings.ToLower) first. libuv
// writes the block as it is, and the child's getenv sees the first of such
// entries.
func sharesEnvKey(env []string) bool {
	seen := make(map[string]bool, len(env))
	for _, entry := range env {
		i := strings.IndexByte(entry, '=')
		if i == 0 {
			i = strings.IndexByte(entry[1:], '=') + 1
		}
		if i < 0 {
			continue
		}
		key := entry[:i]
		if caseInsensitiveEnv {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

// objectProperties is the properties of the object env describes: one per
// name at its first position with its last value.
func objectProperties(env []EnvProperty) []EnvProperty {
	properties := make([]EnvProperty, 0, len(env))
	index := make(map[string]int, len(env))
	for _, property := range env {
		if i, ok := index[property.Name]; ok {
			properties[i] = property
			continue
		}
		index[property.Name] = len(properties)
		properties = append(properties, property)
	}
	return properties
}

// appendCoverageEnv is Node's copyProcessEnvToEnv(env, 'NODE_V8_COVERAGE',
// options.env) (lib/child_process.js, Node 24.19.0): it appends PiG's non-empty
// value unless properties has a name equal to NODE_V8_COVERAGE, compared exactly
// as hasOwnProperty does, and reads PiG's value case-insensitively on Windows
// as process.env does.
func appendCoverageEnv(properties []EnvProperty) []EnvProperty {
	const name = "NODE_V8_COVERAGE"
	value := os.Getenv(name)
	if value == "" || slices.ContainsFunc(properties, func(property EnvProperty) bool {
		return property.Name == name
	}) {
		return properties
	}
	return append(properties, EnvProperty{name, value})
}

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's default
// sort does.
func compareUTF16(a, b string) int {
	for a != "" && b != "" {
		ra, na := utf8.DecodeRuneInString(a)
		rb, nb := utf8.DecodeRuneInString(b)
		if ra != rb {
			firstA, secondA := utf16Units(ra)
			firstB, secondB := utf16Units(rb)
			if c := cmp.Compare(firstA, firstB); c != 0 {
				return c
			}
			return cmp.Compare(secondA, secondB)
		}
		a, b = a[na:], b[nb:]
	}
	return cmp.Compare(len(a), len(b))
}

// utf16Units is r's UTF-16 encoding: one unit and 0, or a surrogate pair.
func utf16Units(r rune) (rune, rune) {
	if r < 0x10000 {
		return r, 0
	}
	return utf16.EncodeRune(r)
}
