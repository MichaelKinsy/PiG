/**
 * Best-effort interpretation of `classDef` styles for a cell grid.
 *
 * A terminal cell can express a foreground, a background and boldness —
 * nothing else. `fill` is the node background, `stroke` its border,
 * `color` its text; every other property is silently ignored.
 */
/** The terminal-expressible subset of a classDef; colors as `#rrggbb`. */
export interface ClassStyle {
    fill?: string;
    stroke?: string;
    color?: string;
    bold?: boolean;
}
/**
 * The merged style of a span's classes (later classes win), or null when
 * nothing terminal-expressible was declared.
 */
export declare function resolveClassStyle(classes: string[] | undefined, classDefs: Record<string, Record<string, string>>): ClassStyle | null;
/**
 * Black or white, whichever reads on the given `#rrggbb` background — the
 * guard that keeps `fill:#eee` legible on a dark terminal theme.
 */
export declare function contrastOn(fill: string): '#000000' | '#ffffff';
//# sourceMappingURL=class-style.d.ts.map