# Stock PiG and PiG Standard

Stock PiG contains the Pi-compatible coding agent and generic support for
extensions, Packages, Piglets, Piglet Binaries, and local reference documents.
It does not activate product extensions. Its startup header shows the PiG pig head
(D2), and its built-in `pig-login` extension registers `/sprite`.

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

Stock PiG's startup header shows the sprite's 7-line pixel pig, with the version and hints beside it, where Pi shows its logo (D2). The built-in `pig-login` extension registers `/sprite`, which chooses one of fifteen built-in sprites or a sprite an extension registered with `ui.registerSprite`, and saves the choice in `$PIG_HOME/state/pig-standard/login.json`. Use `/sprite` to open a picker, `/sprite list` and `/sprite set <id>` for text-based control, and `/sprite preview [id]` to see a sprite's full art.

PiG Standard selects its own `piglogin` extension. That extension installs its
login through `ui.setLogin` and replaces the built-in `/sprite` command.

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
