# go-factory: minimal Go extension

A conventional manifest-free Pig extension written as an importable Go package.
It registers the `hello` tool and `count_words`, a tool with a required `text` parameter that answers an error result when it is missing.

## Structure

| File | Purpose |
|------|---------|
| `go.mod` | Go module and SDK dependency |
| `extension.go` | `Extension() *sdk.Extension` factory |

## Validate or run

```bash
pig install --validate-only --json examples/extensions/go-factory
pig -e examples/extensions/go-factory
```

Pig discovers the factory from its exact Go signature and generates the runner.
The same package can run in an isolated subprocess, a packed subprocess cell, or
a fused Piglet Binary without source changes.
