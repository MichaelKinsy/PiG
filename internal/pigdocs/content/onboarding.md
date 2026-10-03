# Get started with Pig

Stock Pig includes the reference documentation required to configure models,
author extensions, use Packages, and compose Piglets. It does not register an
onboarding extension.

PiG starts without a first-time setup screen, even on a fresh `PIG_HOME`. Pi 1.0 shows one (theme and usage-analytics choices) only when all of these hold (`startup-ui.ts` `shouldRunFirstTimeSetup`): the binary is Pi's own distribution (package `@earendil-works/pi-coding-agent`, app `pi`, config directory `.pi`), `PI_EXPERIMENTAL=1` is set, `PI_CODING_AGENT_DIR` is not set, and `~/.pi/agent/settings.json` does not exist yet. Like any other distribution of Pi's code, PiG never meets the first condition, so neither `PIG_EXPERIMENTAL=1` nor an empty home shows the screen. Choose a theme with `/settings` and a pig with `/sprite`.

Ask the running agent a direct setup question when a model is available. Tell it
to read the matching local documentation before it changes files.

## Set up a project Piglet

Use this prompt when you want Pig to create a project-specific composition:

```text
Read the local Pig documentation. Set up a Piglet for this repository. Explain
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
Pig. From a PiG source checkout, run:

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
