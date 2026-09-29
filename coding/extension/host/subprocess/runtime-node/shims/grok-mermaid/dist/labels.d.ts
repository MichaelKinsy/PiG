/** Node labels wrap to at most this many display columns per line ... */
export declare const WRAP_WIDTH = 24;
/** ... and at most this many lines; overflow is truncated with an ellipsis. */
export declare const MAX_LINES = 4;
/** Edge labels are truncated to this many columns. */
export declare const MAX_LABEL = 28;
/**
 * Identifier-boundary characters preferred as break points when a single word
 * is too wide to fit, so it is not sliced mid-segment.
 *
 * Mirrors `TOKEN_BREAK_CHARS` in grok-build's
 * `third_party/mermaid-to-svg/src/text_wrap.rs`; the two renderers are
 * deliberately independent, so keep these in sync.
 */
export declare const LABEL_BREAK_CHARS: string[];
/**
 * ASCII-only case folding, matching Rust's `to_ascii_lowercase`.
 *
 * `String.prototype.toLowerCase` can change a string's length (`İ` becomes two
 * code points), which would desync the byte offsets some parsers slice with.
 */
export declare const asciiLower: (s: string) => string;
export declare const asciiUpper: (s: string) => string;
/** Applied by every public entry point that takes untrusted source. */
export declare const stripControls: (src: string) => string;
/**
 * Split source into lines the way Rust's `str::lines()` does: on `\n`, with a
 * trailing `\r` stripped, and *without* a final empty line when the input ends
 * in a newline. `String.split` yields that extra element, which would show up
 * as a spurious blank row inside a source box.
 */
export declare function srcLines(src: string): string[];
/** Matches Rust's `char::is_alphanumeric`. */
export declare const isAlphanumeric: (c: string) => boolean;
/** Characters allowed in a bare node/state/class identifier. */
export declare const isIdChar: (c: string) => boolean;
/**
 * Decode HTML entities in label text. Called once per label: via `cleanLabel`
 * for bracketed labels, or explicitly at each direct-push sink.
 */
export declare function decodeHtmlEntities(s: string): string;
/** Strip markdown emphasis from a `` `backtick` `` label string. */
export declare function stripMarkdown(s: string): string;
export declare function stripHtmlTags(s: string): string;
/**
 * Normalise raw label text: strip markup, unquote, and decode entities.
 *
 * Decoding happens after tag-stripping so `<b>` is removed as markup while
 * `&lt;b&gt;` survives as the literal text `<b>`.
 */
export declare function cleanLabel(raw: string): string;
/**
 * Wrap a label to `width` columns over at most `maxLines` lines, truncating the
 * last line with an ellipsis if it overflows.
 *
 * A word too wide to fit is broken after the last identifier boundary
 * (`_-./`) that fits, falling back to a per-character break when it has none.
 */
export declare function wrapLabel(label: string, width: number, maxLines: number): string[];
/** Truncate to `inner` columns, leaving room for the ellipsis. */
export declare function fitLabel(label: string, inner: number): string;
//# sourceMappingURL=labels.d.ts.map