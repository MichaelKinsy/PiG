/**
 * `mindmap`: an indentation tree, drawn the way every TUI draws trees
 * (`├──`/`└──` guides). Parses raw lines rather than statements — the
 * indentation IS the grammar, and `statementsOf` trims it away.
 */
import { Canvas, drawText } from '../canvas.js';
import { MAX_NODES } from '../graph.js';
import { cleanLabel, fitLabel, srcLines, WRAP_WIDTH } from '../labels.js';
import { frontmatterEnd } from '../statements.js';
import { stringWidth } from '../width.js';
export const mindmap = {
    kind: 'mindmap',
    headers: ['mindmap'],
    render(src) {
        const parsed = parseMindmap(src);
        if (parsed === null)
            return null;
        const { roots, warnings } = parsed;
        /** [prefix, text] per row; prefixes carry the `│ ├ └` guides. */
        const rows = [];
        const walk = (node, prefix, childPrefix) => {
            rows.push([prefix, node.text]);
            node.children.forEach((child, i) => {
                const last = i === node.children.length - 1;
                walk(child, childPrefix + (last ? '└── ' : '├── '), childPrefix + (last ? '    ' : '│   '));
            });
        };
        for (const root of roots)
            walk(root, '', '');
        const width = Math.max(...rows.map(([p, t]) => stringWidth(p) + stringWidth(t)));
        const canvas = new Canvas(width, rows.length);
        rows.forEach(([prefix, text], y) => {
            drawText(canvas, prefix, 0, y, 'edge');
            drawText(canvas, text, stringWidth(prefix), y, 'text');
        });
        return { canvas, warnings, classDefs: {} };
    },
};
function parseMindmap(src) {
    const lines = srcLines(src).slice(frontmatterEnd(srcLines(src)));
    const headerAt = lines.findIndex((l) => l.trim() !== '');
    if (headerAt === -1 || lines[headerAt].trim().toLowerCase() !== 'mindmap')
        return null;
    const roots = [];
    const warnings = [];
    /** Ancestors of the next node: the node at each indent level seen so far. */
    const stack = [];
    let count = 0;
    let truncated = false;
    for (const raw of lines.slice(headerAt + 1)) {
        const noComment = raw.split('%%')[0];
        if (noComment.trim() === '')
            continue;
        const indent = noComment.length - noComment.trimStart().length;
        const body = noComment.trim();
        // Decoration lines attach to the previous node and draw nothing.
        if (body.startsWith('::icon') || body.startsWith(':::'))
            continue;
        const text = nodeText(body);
        if (text === '') {
            warnings.push(`dropped, unreadable statement: "${body}"`);
            continue;
        }
        if (count >= MAX_NODES) {
            truncated = true;
            break;
        }
        count++;
        const node = { text: fitLabel(text, WRAP_WIDTH), children: [] };
        while (stack.length > 0 && stack[stack.length - 1].indent >= indent)
            stack.pop();
        if (stack.length === 0)
            roots.push(node);
        else
            stack[stack.length - 1].node.children.push(node);
        stack.push({ indent, node });
    }
    if (truncated)
        warnings.push(`diagram truncated: node cap (${MAX_NODES}) reached`);
    return roots.length === 0 ? null : { roots, warnings };
}
/** Shape brackets around a mindmap node all mean "text" in a terminal. */
const SHAPES = [
    ['((', '))'],
    ['))', '(('],
    ['(-', '-)'],
    ['{{', '}}'],
    ['[', ']'],
    ['(', ')'],
];
function nodeText(body) {
    // `id((text))` — an optional id may precede the bracket.
    for (const [open, close] of SHAPES) {
        const at = body.indexOf(open);
        if (at !== -1 && body.endsWith(close) && body.length > at + open.length + close.length - 1) {
            return cleanLabel(body.slice(at + open.length, body.length - close.length));
        }
    }
    return cleanLabel(body);
}
//# sourceMappingURL=mindmap.js.map