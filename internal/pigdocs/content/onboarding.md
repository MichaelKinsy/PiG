# Get started with PiG

Stock Pig includes the reference documentation required to configure models,
author extensions, use Packages, and compose Piglets. It does not register an
onboarding extension.

On a first interactive start, PiG shows a first-time setup before the session opens (D88). It appears when `PIG_CODING_AGENT_DIR` is not set and the agent directory has no `settings.json` yet, which includes every fresh `PIG_HOME`. It has three steps: a theme (System, Dark or Light, with a live preview), a sprite (the pig the header shows) and a choice to share anonymous usage data. Finishing saves the answers to `settings.json` and to the sprite state, so the setup does not return. `Esc` skips it and saves no answer. The setup still does not return: the same start writes `settings.json` (with `lastChangelogVersion`), and the setup appears only while that file is missing. Print, JSON and RPC modes never show it. Pi 1.4 shows its own two-step version (theme and analytics) only for its official distribution with `PI_EXPERIMENTAL=1` (`startup-ui.ts` `shouldRunFirstTimeSetup`), so PiG shows it more often than Pi does. Change the theme later with `/settings`, the sprite with `/sprite`, and the usage-data choice by setting `enableAnalytics` in `settings.json`.

Ask the running agent a direct setup question when a model is available. Tell it
to read the matching local documentation before it changes files.

## Set up a project Piglet

Use this prompt when you want PiG to create a project-specific composition:

```text
Read the local PiG documentation. Set up a Piglet for this repository. Explain
each selected model preference, system prompt, built-in tool, skill, and
extension. Write the Piglet source, validate it with pig piglet validate, and
show the exact pig --piglet command.
```

The expected result is a Piglet source under `~/.pig/piglets/`, a project-local
`.pig/piglets/` directory, or another explicit path.

## Scaffold an extension

```bash
pig extension init ./my-extension --lang go
pig install ./my-extension --validate-only --json
pig -e ./my-extension
```

Edit the extension and use `/reload` in the session. Read `extensions.md` for
the source forms. Read `extension-api.md` for the host API.

## Use PiG Standard

PiG Standard is an explicit Piglet composition. It is not part of bare Stock
PiG. From a PiG source checkout, run:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

Build a directly executable composition when you do not want the target host to
compile compatible Go or Rust extension sources:

```bash
pig piglet build piglets/standard/pig-standard.yaml \
  --format binary \
  --out ./pig-standard
```

## Related documents

- [Concepts](concepts.md)
- [Extensions](extensions.md)
- [Extension API](extension-api.md)
- [Packages](packages.md)
- [Piglets](piglets.md)
- [Stock PiG and PiG Standard](built-in-extensions.md)
