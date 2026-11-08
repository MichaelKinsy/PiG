/**
 * The shared statement layer: source text to statements, plus the small
 * string-reading helpers every grammar leans on.
 */
/**
 * Split one source line into statements on `;`, stopping at a `%%` comment.
 *
 * Quoted spans are opaque, so a label may contain `;` and `%%`.
 */
export declare function splitStatements(line: string, out: string[]): void;
/**
 * Index just past a leading YAML frontmatter block (`---` … `---`), or 0 when
 * there is none. While the block is still unterminated everything is
 * frontmatter, so a streamed diagram stays blank until it closes.
 */
export declare function frontmatterEnd(lines: string[]): number;
/**
 * All statements in a source block, in order. A leading YAML frontmatter
 * block, part of the mermaid grammar since v10, is skipped.
 */
export declare function statementsOf(src: string): string[];
/**
 * The `title:` of a leading frontmatter block, or null. The one frontmatter
 * key with terminal meaning — `config` and friends style mermaid's own
 * renderers and are deliberately ignored.
 */
export declare function frontmatterTitle(src: string): string | null;
/** Per-char flags: 1 where the char lies inside a double-quoted span (quotes included). */
export declare function quoteMask(chars: string[]): Uint8Array;
/**
 * Split on separator chars sitting outside double quotes and parentheses,
 * dropping empty segments — the one splitter every grammar shares, so quote
 * rules cannot drift between them.
 */
export declare function splitTop(s: string, isSep: (c: string) => boolean): string[];
/**
 * Split `head : rest` at the first label colon, skipping `:::` tag runs so
 * `A:::hot : desc` keeps its tag with the id. `null` when there is no colon.
 */
export declare function splitColon(s: string): [string, string] | null;
/** Strip trailing `:::name` tags from an id token: `A:::hot` → id `A`, classes `[hot]`. */
export declare function takeTags(token: string): {
    id: string;
    classes: string[];
};
/**
 * The body of a `class A,B name` statement → `[ids, names]`. The last
 * whitespace-separated token is the name list, everything before it the ids —
 * so a space after a comma (`class A, B warn`) still reads as two ids.
 */
export declare function parseClassAssign(rest: string): [string[], string[]] | null;
/**
 * `A "url" [tooltip]` / `A href "url" …` → `[id, url]`. The callback forms
 * (`call`/`callback`) return null — their quoted string is a tooltip.
 */
export declare function parseHref(rest: string): [string, string] | null;
export declare const firstWord: (s: string) => string;
export declare const words: (s: string) => string[];
/** Split on the first occurrence of `sep`, Rust's `split_once`. */
export declare function splitOnce(s: string, sep: string): [string, string] | null;
export declare const nonEmpty: (s: string) => string | null;
/** Diagram kind from the header statement, lowercased. */
export declare function headerKind(statements: string[]): string | null;
/**
 * Parse the body of a `classDef` statement: `name[,name2] k1:v1,k2:v2`.
 * Values are kept verbatim; malformed pairs are skipped.
 */
export declare function parseClassDef(rest: string): {
    names: string[];
    props: Record<string, string>;
} | null;
//# sourceMappingURL=statements.d.ts.map