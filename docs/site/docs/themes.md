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

Extensions can list themes, inspect a named theme, and request a theme change through the public UI context.

A subprocess extension receives serializable theme data. It cannot transfer a live host theme object across the process boundary.

## Security

A theme is data, but the Package that distributes it can also contain executable extensions or instruction Resources. Review complete Package membership before installation.

## Upstream compatibility

PiG follows Pi theme behavior where the Go TUI exposes the same observable contract. The TypeScript package `@earendil-works/pi-tui` remains an upstream Pi package, not a PiG Go API.

See [upstream Pi's theme documentation](https://pi.dev/docs/latest/themes) for the reference implementation's complete theme format.

## Related documentation

- [Settings](/docs/latest/settings)
- [Packages](/docs/latest/packages)
- [Piglets](/docs/latest/piglets)
- [Terminal UI](/docs/latest/tui)
