import { type ClassStyle } from './class-style.ts';
import type { MermaidArt, Role } from './types.ts';
/**
 * SGR parameter per role, e.g. `'2'` for dim, `'36'` for cyan,
 * `'38;5;244'` for a 256-colour index. A role left out is printed unstyled.
 */
export type AnsiTheme = Partial<Record<Role, string>>;
/** Dim frame, plain labels, cyan connectors. Readable on light and dark. */
export declare const DEFAULT_THEME: AnsiTheme;
/**
 * The truecolor SGR a class style gives a span of the given role, or
 * undefined when the style says nothing about it (fall back to the theme).
 * `stroke` colors borders, `color` text; `fill` backs every painted cell,
 * with a black/white foreground picked for contrast when none was declared.
 * A style that colors nothing for this role keeps `fallback` (the theme's
 * SGR), so a bold-only class bolds the themed look instead of replacing it.
 */
export declare function classSgr(st: ClassStyle, role: Role, fallback?: string): string | undefined;
/**
 * Render art to ANSI-coloured lines. Spans that carry author classes are
 * styled from `art.classDefs` (best effort — see `resolveClassStyle`),
 * overriding the role theme; everything else follows `theme`.
 *
 * A convenience over mapping `art.styled` yourself — reach for that directly
 * when your TUI has its own styling model.
 */
export declare function toAnsi(art: MermaidArt, theme?: AnsiTheme): string[];
//# sourceMappingURL=ansi.d.ts.map