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
