# Knowledge graph

PiG is built from a small set of entities. This page lists each entity, where it lives, the command that inspects it, and how the entities relate. The same graph is published as JSON-LD at [pig-knowledge-graph.jsonld](/pig-knowledge-graph.jsonld) and as Mermaid source in `docs/knowledge-graph/pig.mmd`, and it ships inside every pig binary as `knowledge-graph.md` in the local docs (`pig docs show knowledge-graph`).

![Entity relationship diagram of PiG](/pig-knowledge-graph.svg)

| Entity | What it is | Where it lives | Inspect with | Read |
|---|---|---|---|---|
| Pi | Upstream TypeScript coding agent; the behavior oracle. | `https://github.com/earendil-works/pi` | `pig version` | `divergences.md` |
| PiG | The Go implementation; executable pig. | `coding/pigversion/pigversion.go pins Pi` | `pig verify` | `README.md` |
| Stock PiG | Product-neutral pig binary with no Piglet selected. | `cmd/pig` | `pig version` | `built-in-extensions.md` |
| Session | Append-only JSONL tree of entries for one conversation. | `~/.pig/agent/sessions` | `/tree, /session` | `sessions.md` |
| Model Runtime | Session-owned model lookup, auth, completion, and streaming used by every mode. | `coding/` | `/model, pig --list-models` | `models.md` |
| Provider | Turns a Transcript into one Event Stream for one API. | `ai/` | `pig --list-models` | `providers.md` |
| Mode | Interactive TUI, print, JSON event stream, or RPC over stdin/stdout. | `cmd/pig` | `pig --mode rpc` | `commands.md` |
| Resource | One capability: extension, skill, prompt, hook, MCP definition, theme, or environment. | `~/.pig/agent, .pig/ in a project` | `pig status --json` | `concepts.md` |
| Extension | Code Resource in Go, Rust, Python, or Node/TypeScript that registers tools, commands, events, and UI. | `extensions/sdk*, ~/.pig/agent/extensions` | `pig install --validate-only <dir>` | `extensions.md` |
| Extension SDK | One extension contract per language: Go, Rust, Python, TypeScript declarations. | `extensions/sdk, sdk-rs, sdk-py, sdk-ts` | `pig extension init` | `extension-api.md` |
| Extension realization | How an extension runs: isolated process, packed runtime cell, or fused into a Piglet Binary (Go only). | `coding/extension/host` | `pig status --json` | `runtime-cells.md` |
| Extension Host | Loads, isolates, reloads, and shuts down extension realizations over one wire contract. | `coding/extension/host/subprocess` | `/reload` | `runtime-cells.md` |
| Package | Versioned collection of Resources from npm, git, or a local path; never owns a Piglet. | `~/.pig/agent/npm, ~/.pig/agent/git` | `pig list` | `packages.md` |
| Piglet | One named agent: selects and scopes Resources and sets defaults. | `*.yaml Piglet file` | `pig piglet validate <file>` | `piglets.md` |
| PiG Standard | The optional curated Piglet: login identity, PiG Runner, Angry Pigs. | `piglets/standard/pig-standard.yaml` | `pig piglet validate piglets/standard/pig-standard.yaml` | `built-in-extensions.md` |
| Piglet release | Pins the exact Resource closure and component plan of a Piglet. | `release.version and managed records` | `pig piglet show --record <path>` | `piglets.md` |
| Piglet Binary | Target-native pig executable for one Piglet release; verifies its closure at startup. | `pig piglet build --format binary` | `pig verify` | `piglets.md` |
| Builder | Produces Piglet Binaries: native (needs Go and PiG source) or container (needs Docker or Podman). | `coding/pigletbuild` | `pig setup` | `piglets.md` |
| Pig Porter | Parity workbench Piglet Resource; proposes ports, never accepts its own work. | `piglets/porter` | `make porter-check (source checkout)` | `divergences.md` |
| Divergence | A numbered, approved, user-visible difference from pinned Pi. | `DIVERGENCES.md` | `pig docs show divergences` | `divergences.md` |

| From | Relation | To | Note |
|---|---|---|---|
| PiG | implements | Pi | exact pinned release |
| Stock PiG | builds | PiG | one binary per target |
| Session | owns | Model Runtime | one per Session |
| Model Runtime | streams through | Provider | one Provider per request |
| Mode | drives | Session | TUI, print, JSON, RPC share one Session path |
| Package | distributes | Resource | one to many |
| Extension | is a | Resource |  |
| Extension | written with | Extension SDK | one language per extension |
| Extension | realized as | Extension realization | isolated, packed, or fused |
| Extension Host | hosts | Extension realization | same contract in every realization |
| Piglet | selects | Resource | and scopes discovery |
| PiG Standard | is a | Piglet | explicit, never active in Stock |
| Piglet release | pins | Piglet | exact closure |
| Piglet Binary | realizes | Piglet release | per target |
| Builder | produces | Piglet Binary | no silent fallback between builders |
| Pig Porter | is a | Resource | selected by the Porter Piglet |
| Divergence | documents difference from | Pi | numbered D<N> |

The graph source is `docs/knowledge-graph/pig.graph.json`. Run `automation/gen/gen-knowledge-graph.py` after editing it.
