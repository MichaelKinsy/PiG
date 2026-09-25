# PiG Standard

PiG Standard is an explicit Piglet composition built from ordinary Resources.
It is separate from Stock PiG. Bare `pig` does not load this composition.

Run it from this source tree:

```bash
pig --piglet piglets/standard/pig-standard.yaml
```

Build a directly executable Piglet Binary:

```bash
pig piglet build piglets/standard/pig-standard.yaml \
  --format binary \
  --out ./pig-standard
```

The current composition selects `extensions/piglogin`, `extensions/pigrunner`,
and `extensions/angrypigs`. The login extension owns the PiG login, sprite
catalogue, `/sprite` command, and selected-sprite state. The default sprite is
`hpe-agentic`: the green pixel pig and "PiG." wordmark from the PiG website.
`/sprite` selects another sprite, and each sprite colors the wordmark period. The Runner extension
owns `/runner`, `/pig-runner`, and high-score state. PiG Runner is the original
half-block pixel-art runner, with the selected sprite's pig, drawn over the
whole terminal. The Angry Pigs extension in `extensions/angrypigs` owns `/angry-pigs` and
its high-score state. Angry Pigs is a pixel-art slingshot game over the whole
terminal: Up and Down pull the band back for power, Left and Right aim, and a
mouse drag aims when the terminal reports mouse input. The selected sprite's
pig flies at bird-topped towers of wood, stone, and ice that crack, shatter,
and collapse, with a gauge, a trajectory preview, and particles. Physics runs
at a fixed 120 Hz step; a camera pans over the fixed-size world, and a
minimap shows the whole field when the terminal is narrower than the world.
PiG Runner follows the Chrome Dino difficulty curve: its speed climbs to a
cap, and obstacles come faster, in groups, and flying as it speeds up.
Both games share `internal/arcade`: PiG Runner's night sky, hills, and turf,
the two HUD lines with score and high score, and the pixel-font title and
game-over cards.
Stock PiG only supplies generic extension UI contracts.

PiG Standard sets `build.extensionRealization: fused`. A Binary build fails if
any selected extension is not a fuse-compatible Go factory. The build does not
fall back to a subprocess component. Source execution uses the normal
subprocess host for development and verification. PiG serializes focused
extension overlays, keeps component work off the TUI loop, coalesces timer
redraws to the 16 ms frame interval, and detaches invalidation before disposal.
The games share `internal/pixel`, which draws RGBA canvases as half-block
lines in 24-bit color, or the nearest 256 color when the terminal lacks 24-bit
color, and reuses unchanged lines between frames. They open with
`internal/termgame.Overlay`, a modal overlay over the whole terminal whose
one-cell box frames the game.

PiG Standard currently includes identity, Runner UI, and Angry Pigs. It does not include the
removed onboarding, harness-discovery, hook-runner, marketplace, or host-auth
products. Generic extension events, Package installation, Piglets, and provider
OAuth remain Stock PiG infrastructure. Add a future product capability only as
an explicitly selected, fuse-compatible Standard Resource with its own state
and public host contract.
