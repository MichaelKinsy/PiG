import type { Cls, MermaidArt } from './types.ts'

const ESC = String.fromCharCode(27)

/**
 * SGR parameter per semantic class, e.g. `'2'` for dim, `'36'` for cyan,
 * `'38;5;244'` for a 256-colour index. A class left out is printed unstyled.
 */
export type AnsiTheme = Partial<Record<Cls, string>>

/** Dim frame, plain labels, cyan connectors. Readable on light and dark. */
export const DEFAULT_THEME: AnsiTheme = {
  border: '2',
  edge: '36',
  edgeLabel: '2;36',
  title: '1',
}

/**
 * Render art to ANSI-coloured lines.
 *
 * A convenience over mapping `art.styled` yourself — reach for that directly
 * when your TUI has its own styling model.
 */
export function toAnsi(art: MermaidArt, theme: AnsiTheme = DEFAULT_THEME): string[] {
  return art.styled.map((row) =>
    row
      .map((span) => {
        const sgr = theme[span.cls]
        return sgr === undefined ? span.text : `${ESC}[${sgr}m${span.text}${ESC}[0m`
      })
      .join(''),
  )
}
