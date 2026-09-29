import type { Cls, MermaidArt } from './types.ts';
/**
 * SGR parameter per semantic class, e.g. `'2'` for dim, `'36'` for cyan,
 * `'38;5;244'` for a 256-colour index. A class left out is printed unstyled.
 */
export type AnsiTheme = Partial<Record<Cls, string>>;
/** Dim frame, plain labels, cyan connectors. Readable on light and dark. */
export declare const DEFAULT_THEME: AnsiTheme;
/**
 * Render art to ANSI-coloured lines.
 *
 * A convenience over mapping `art.styled` yourself — reach for that directly
 * when your TUI has its own styling model.
 */
export declare function toAnsi(art: MermaidArt, theme?: AnsiTheme): string[];
//# sourceMappingURL=ansi.d.ts.map