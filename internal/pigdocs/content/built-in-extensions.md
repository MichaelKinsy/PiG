# Stock PiG and PiG Standard

Stock PiG contains the Pi-compatible coding agent and generic support for
extensions, Packages, Piglets, Piglet Binaries, and local reference documents.
It does not activate product extensions or PiG artwork.

## Stock capabilities

| Capability | Purpose |
|---|---|
| `pig docs` | Materialize and read the documentation that matches the binary. |
| `pig extension ...` | Author, inspect, validate, and run ordinary extensions. |
| `pig install` | Install Packages that distribute Resources. |
| `pig piglet ...` | Validate, register, inspect, and build Piglets. |
| Piglet Binary verification | Verify the embedded Piglet and component closure before startup. |

These capabilities do not select a product composition.

## PiG Standard

PiG Standard is an explicit Piglet under `piglets/standard/`. It selects
ordinary extension Resources through public Piglet contracts. Bare `pig` does
not load those Resources.

Run the source composition from a PiG source checkout:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

Build a directly executable composition:

```bash
pig piglet build piglets/standard/pig-standard.yaml \
  --format binary \
  --out ./pig-standard
```

Installing a Package can make Standard Resources available. It does not create,
select, or activate the PiG Standard Piglet.

## Login identity

Stock PiG starts with its text header and has no built-in mascot catalogue.
PiG Standard selects the `piglogin` extension. The extension installs its login
through `ui.setLogin` and owns the `/sprite` command and selected-variant state.

Use `/sprite` in a PiG Standard session to select a variant. Use `/sprite list`
or `/sprite set <id>` for text-based control.

## PiG Runner

PiG Standard selects the `pigrunner` extension. Use `/runner` or `/pig-runner`
to open it. The extension owns timer-driven custom UI and stores the high score
at `<config-root>/state/pig-standard/pigrunner.json`.

The host gives one focused extension exclusive terminal input. Timer redraws
use the generic invalidation callback and do not block the TUI render loop.

## Additional batteries

Each additional battery must be an ordinary fuse-compatible Go extension
Resource selected by the Standard Piglet. It must work as a source subprocess
extension and as a fused component in the PiG Standard Binary. The Standard
build fails instead of using a subprocess fallback. Stock PiG must not import or
activate it.
