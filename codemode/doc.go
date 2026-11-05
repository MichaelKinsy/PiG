// Package codemode runs a model-written JavaScript script against a table of host tools and globals. It ports
// packages/codemode: the script runs in a QuickJS VM compiled to WebAssembly and hosted by wazero, and reaches the
// host only through one bridge function (docs/specs/builtin-codemode-tool-search.md).
//
// # Embedding in a macOS app signed with the Hardened Runtime
//
// By default wazero runs the module with its compiler, which writes machine code into memory, makes it executable and
// runs it. A process signed with the Hardened Runtime (`codesign --options runtime`) is killed (SIGKILL, no error reaches
// the program) at the first execution unless it carries the com.apple.security.cs.allow-unsigned-executable-memory
// entitlement. com.apple.security.cs.allow-jit alone is not enough, because wazero does not map its pages with MAP_JIT.
// Give the app that entitlement, or, when it cannot carry it, set SandboxOptions.Interpreter (ServeOptions.Interpreter for
// ServeRemote, and Options.Interpreter of the built-in Code Mode extension) to run on wazero's interpreter, which needs no
// entitlement and is about 50 times slower, with over a second to start each VM. The "Embedding codemode in a signed macOS
// app" section of docs/site/docs/codemode.md has the measurements.
package codemode
