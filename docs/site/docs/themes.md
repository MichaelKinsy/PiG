# Themes

PiG uses themes to style its terminal interface. Select a theme through `/settings` or the `theme` setting.

## Select a theme

Start PiG and open settings:

```text
/settings
```

Select the active theme. PiG writes the choice to the applicable settings file.

You can also set the name directly in `~/.pig/agent/settings.json`:

```json
{
  "theme": "dark"
}
```

## Worked example: choose a theme

Open the settings list, type `theme` to find the row and press `Enter`. Checked on PiG, a fresh start shows `system` (the theme made from your terminal's own colours):

```text
> theme
→ Theme                             system
  Default thinking level per model  none
  Color theme for the interface
  Type to search · Enter/Space to change · Esc to cancel
```

```text
Theme
Select a theme, or choose automatic to follow terminal appearance.
→ ✓ system     Theme created from your terminal's colors
    automatic  Use separate themes for light and dark terminal appearance
    dark
    light
  Enter to select · Esc to go back
```

The check mark shows the theme in use. Move to `dark` and press `Enter`. The colours change at once, the row now reads `Theme  dark`, and PiG has written the choice to `~/.pig/agent/settings.json`:

```json
{
  "lastChangelogVersion": "1.2.0",
  "theme": "dark"
}
```

To try a theme for one run without saving it, use `--use-theme`: `pig --use-theme light`. A theme file of your own goes in `~/.pig/agent/themes/` and appears in this list. The format is Pi's, and the complete list of colours it needs is in Pi's theme documentation (linked below).

PiG checks every theme file against Pi's theme schema when it loads it, at startup and on reload. Pi 1.3.0 made the schema strict, so a theme file that loaded in Pi 1.1.0 can now be rejected. A theme file may hold only the documented top-level fields: `$schema`, `name`, `appearance`, `vars`, `colors` and `export`. Every key under `colors` and `export` must be a documented colour token. Put reusable colours of your own under `vars`. Point `$schema` at `https://pi.dev/schemas/theme.schema.json` for editor validation and completion.

## Automatic switching

Choose **Automatic** in the theme submenu to select separate themes for light and dark terminal appearance. You can also set the pair directly:

```json
{
  "theme": "light/dark"
}
```

The first name is the light theme. The second name is the dark theme. Live switching requires terminal support for color-scheme notifications (DEC mode 2031). PiG preserves the pair when the terminal appearance changes. Selecting a single theme, including through an extension, disables automatic switching.

## Theme sources

PiG can load theme Resources from:

- `~/.pig/agent/themes/`;
- trusted project Resources;
- configured Package members;
- explicit settings paths;
- an active Piglet's permitted Resource set.

Project theme discovery requires project trust.

## Packages and Piglets

A Package can distribute a theme Resource. Installing the Package makes the theme available to normal Resource discovery and filters.

A Piglet can select Resources for one agent application and control ambient discovery. The Piglet does not copy or rewrite the theme.

A Package never activates a Piglet.

## Reload

In an interactive session, PiG watches the selected custom theme's file under `~/.pig/agent/themes/`. File notifications debounce for 100 ms before reload. A missing or invalid file leaves the last valid theme active. An operating-system watcher error stops notifications without terminating the session. Select the theme again to restart its watcher.

Theme previews do not replace the active watch registration. Themes from other Resource directories and Resource configuration changes still require `/reload`.

Use `/reload` after you change a theme file or Resource configuration:

```text
/reload
```

A failed Resource reload leaves the previous working extension set active. Theme parse errors are reported instead of silently selecting another authored theme.

## Extension access

Extensions can list themes, inspect a named theme, and request a theme change through the public UI context. `ctx.ui.theme.sourcePath` reports the source file of the active theme, also for a theme loaded from a file that the active registry does not hold.

A subprocess extension receives serializable theme data. It cannot transfer a live host theme object across the process boundary.

## Security

A theme is data, but the Package that distributes it can also contain executable extensions or instruction Resources. Review complete Package membership before installation.

## Upstream compatibility

PiG follows the theme behavior of Pi 1.3.0 where the Go TUI exposes the same observable contract. The TypeScript package `@earendil-works/pi-tui` is a Pi package, not a PiG Go API.

See [Pi's theme documentation](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/themes.md) for the complete theme format and its color tokens.

## Related documentation

- [Settings](/docs/latest/settings)
- [Packages](/docs/latest/packages)
- [Piglets](/docs/latest/piglets)
- [Terminal UI](/docs/latest/tui)
