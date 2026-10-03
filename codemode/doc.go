// Package codemode runs a model-written JavaScript script against a table of host tools and globals. It ports
// packages/codemode: the script runs in a QuickJS VM compiled to WebAssembly and hosted by wazero, and reaches the
// host only through one bridge function (docs/specs/builtin-codemode-tool-search.md).
package codemode
