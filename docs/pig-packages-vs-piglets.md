# Packages versus Piglets

Read the [ecosystem ontology](piglet-resource-ontology.md) first.

Pig has two authored management poles:

- a **Package** distributes available Pig/Pi Resources;
- a **Piglet** selects, scopes, launches, and builds one agent. Planned distribution publishes that source and its release facets.

Piglet Binary, Piglet Image, and Piglet release are generated Piglet facets,
not peer authored entities.

Return to the [maintainer docs router](README.md). Read
[Package structure](pig-package-spec.md) for layout and
[Piglet design](pig-piglet-spec.md) for schema, component closure, and carriers.

## Decision table

| Question | Package | Piglet |
|---|---|---|
| What is it? | versioned Resource distribution envelope | independent named agent declaration |
| Primary job | make Resources available | select/scope Resources and define runtime behavior |
| Upstream status | Pi-compatible Package model with governed Pig additions | Pig-only governed addition |
| Contains/references | owned extensions, skills, prompts, themes, hooks, MCP definitions, agents, environments | Package dependencies, direct origins, model/prompt settings, scopes, `agentEnv`, release/build defaults |
| Main action | install/update/remove | create/edit/run/build/remove |
| Runs by itself | no | yes through Host Piglet or Piglet Binary; Image is planned |
| Direct `pig install` | records Package in settings; Pi discovery/filtering exposes enabled members | not applicable |
| Piglet dependency | materialized without settings mutation | only explicitly selected Package members activate |
| Marketplace kind | `package` | independent Piglet source; npm catalog listing is planned |

## One-way relationship

```text
Resources ── optionally grouped by Package ──┐
Plugin Resources ────────────────────────────┼──> Piglet ──> Piglet release
standalone direct origins ───────────────────┘                   ├── Binary
                                                                └── Image
```

Packages do not contain or own Piglets. Validation rejects Piglet membership,
avoiding cyclic ownership, implicit activation, and ambiguous removal.

## Availability versus activation

A Package may contain many Resources. Direct `pig install` preserves upstream Pi
behavior by adding the Package to settings; discovery exposes members, defaulting
to all unless Package filters narrow them.

A Piglet dependency does not mutate settings. It declares the Package source
once and selects members explicitly:

```yaml
packages:
  base: npm:@example/base-coding@^1

extensions:
  - name: trace
    origins:
      - package:base
      - npm:@example/fallback-trace@^1
```

The section kind plus `trace` selects the Package member. Package filesystem
paths remain implementation details. Ordered origins remain independent. The
resolution graph de-duplicates identical canonical sources.

## Package origin feeds component closure

When a Piglet is built, the selected Package/Resource origin becomes a component
record:

```text
Package member identity
  -> exact source/version/content digest
  -> Piglet capability scope
  -> realization: fused | subprocess | external
  -> materialization: binary | release | agentEnv | external
  -> Piglet record and managed release closure
```

This is how a Piglet Binary can safely use ordinary subprocess extensions. The
binary does not accept whatever happens to be installed. It verifies the exact
origin-pinned component from its embedded assets, registered Piglet release,
required environment, or explicit external mapping.

Fusing a compatible Go factory is an optimization. It does not create a
different Piglet Binary ontology.

## Piglet source stays explicit

Package and Resource inventory can inform the author, but Pig does not infer a
Piglet from installed state. The user, editor, or agent writes explicit Package
aliases and member origins in Piglet YAML. Installing a Package alone never
creates or activates a Piglet.

## Planned (not in this release): publication flow

```text
install/reference Resources
          |
          v
available direct/Plugin/Package inventories
          |
          v
author independent Piglet YAML
          |
          +---- publish source + exact resolution/component closure
          |
          v
build optional Piglet Binary and/or Piglet Image
          |
          v
publish one Piglet release with selected artifacts and records
```

| Intent | Catalog entity/action |
|---|---|
| cross-harness capability product | Plugin |
| grouped Pig/Pi Resources | Package |
| editable whole-agent composition | Piglet source/release |
| target-native agent executable | Piglet Binary facet |
| environment-complete runnable agent | Piglet Image facet |

Do not wrap a Piglet in a Package to publish it. Do not merge Plugin and Package schemas when both point to one source root. Source and Binary outputs will be facets of one published Piglet release. Piglet Image publication remains reserved until an Image producer exists.

## Authoring safety

Package source is authored directly as ordinary `package.json`; Pig provides
side-effect-free `package validate` rather than a second membership editor. A
running session is never an implicit Package source because it may contain
third-party Resources whose licenses, provenance, or layout prohibit
rebundling. Piglets may use session/repository discovery because they write
references and selections rather than copying source.

## Source identity

Install, Package, Plugin, and Piglet origins use `coding/source`. Exact version,
commit, digest, membership, and content identity belong in Piglet resolution and
artifact records. Materialization/cache paths never become public identity.
