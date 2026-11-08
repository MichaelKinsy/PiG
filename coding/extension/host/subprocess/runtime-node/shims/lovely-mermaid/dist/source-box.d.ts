/**
 * The raw source in a framed box.
 *
 * What to show when `render` returns `null`, or returns art too wide for the
 * space at hand. Both are the caller's call, so this is theirs to invoke — and
 * theirs to caption, since only they know whether some other view of the
 * diagram exists to point the reader at.
 */
import type { MermaidArt } from './types.ts';
/**
 * Frame `src` in a titled box, hard-wrapping its lines to `maxWidth` columns.
 *
 * The result can still exceed `maxWidth`: the body wraps to
 * `max(8, maxWidth - 4)` and the ` mermaid: <kind> ` title is never truncated,
 * so a long first token sets a floor. Check `width` if it matters.
 */
export declare function sourceBox(src: string, maxWidth?: number): MermaidArt;
//# sourceMappingURL=source-box.d.ts.map