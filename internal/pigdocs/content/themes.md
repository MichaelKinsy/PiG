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

## Create a custom theme

A theme is one JSON file. Write the file, name it, and select it.

1. Save the file as `~/.pig/agent/themes/my-theme.json`.
2. Set `name` to `my-theme`.
3. Give every required role in `colors` a value. Reuse values through `vars`.
4. Select `my-theme` in `/settings`, or run `pig --use-theme my-theme`.

A minimal file:

```json
{
  "name": "my-theme",
  "appearance": "dark",
  "vars": { "primary": "#00aaff", "ink": "#e6e6e6" },
  "colors": { "accent": "primary", "text": "ink" }
}
```

This file is not complete. The `colors` object needs all 51 required roles that the next sections list. Name the file after the theme. PiG reloads the active user theme at once only from `~/.pig/agent/themes/<name>.json`. Run `/reload` after you change a theme from any other source. To test a file without installing it, run `pig --theme ./my-theme.json`.

## Understand the theme file

| Property | Required | Responsibility |
|---|---|---|
| `name` | Yes | Names the theme in selectors and settings. It must be unique. It cannot contain `/`, because `light/dark` pairs use that mark. |
| `appearance` | No | `"dark"` or `"light"`: the background the theme is made for. PiG detects it from the colors when you omit it. |
| `vars` | No | Reusable color values. A variable can refer to another variable. |
| `colors` | Yes | One color for each terminal UI role. |
| `export` | No | `pageBg`, `cardBg`, and `infoBg` for HTML exports. PiG derives them from `userMessageBg` when you omit them. |
| `$schema` | No | A schema URL for editor completion. |

A color can have one of six forms:

| Form | Example | Meaning |
|---|---|---|
| RGB hexadecimal | `"#0af"` or `"#00aaff"` | A three-digit or six-digit sRGB color. |
| OKLCH | `"oklch(62% 0.1 200)"` | Lightness, chroma, and hue. |
| OKHSL | `"okhsl(250 60% 55%)"` | Hue, saturation, and lightness. Every value is inside the sRGB range. |
| 256-color index | `39` | An ANSI palette index from 0 to 255. |
| Variable reference | `"primary"` | The value of an entry in `vars`. |
| Terminal default | `""` | The foreground or background color of the terminal. |

A missing variable or a circular reference makes the theme invalid. A color name that is not in the lists below also makes the theme invalid: a typo such as `accentt` fails with `must not have additional properties`. A missing required role fails too. The interactive screen reports an invalid theme at startup and at `/reload`. Print mode and `pig status --json` do not check theme files, so open `pig` and select the theme to test it. If colors look wrong, check the terminal color support in [Terminal setup](terminal-setup.md).

## Find the color to change

Theme colors name interface roles, not single components.

| Area | Color names |
|---|---|
| General interface | `accent`, `border`, `borderAccent`, `borderMuted`, `text`, `muted`, `dim`, `success`, `error`, `warning` |
| Selection | `selectedBg` |
| Messages | `userMessageBg`, `userMessageText`, `customMessageBg`, `customMessageText`, `customMessageLabel`, `thinkingText` |
| Tool execution | `toolPendingBg`, `toolSuccessBg`, `toolErrorBg`, `toolTitle`, `toolOutput` |
| Markdown | `mdHeading`, `mdLink`, `mdLinkUrl`, `mdCode`, `mdCodeBlock`, `mdCodeBlockBorder`, `mdQuote`, `mdQuoteBorder`, `mdHr`, `mdListBullet` |
| Tool diffs | `toolDiffAdded`, `toolDiffRemoved`, `toolDiffContext` |
| Syntax highlighting | `syntaxComment`, `syntaxKeyword`, `syntaxFunction`, `syntaxVariable`, `syntaxString`, `syntaxNumber`, `syntaxType`, `syntaxOperator`, `syntaxPunctuation` |
| Thinking levels and shell mode | `thinkingOff`, `thinkingMinimal`, `thinkingLow`, `thinkingMedium`, `thinkingHigh`, `thinkingXhigh`, `bashMode` |

These are the 51 required roles. Five more roles are optional and use a fallback when you omit them:

| Optional color | Fallback |
|---|---|
| `scrollbarTrack` | `muted` |
| `scrollbarThumb` | `text` |
| `searchMatchBg` | `selectedBg` |
| `searchMatchText` | `text` |
| `thinkingMax` | `thinkingXhigh` |

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

The theme file format above is the same as upstream Pi's, with PiG paths.

## Related documentation

- [Settings](settings.md)
- [Packages](packages.md)
- [Piglets](piglets.md)
- [Terminal UI](tui.md)
