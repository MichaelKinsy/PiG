package host

// The principals the Pi chord tests use (test/*.test.ts at aa9cf15b): the host's own is { kind: "host" } and what a
// child's parent acts as is { kind: "server" }.

func kindHost() Principal { return map[string]any{"kind": "host"} }

func kindServer() Principal { return map[string]any{"kind": "server"} }
