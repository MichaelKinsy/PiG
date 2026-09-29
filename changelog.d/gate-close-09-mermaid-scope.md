### Fixed

- Transform only top-level Mermaid code tokens. Preserve examples nested in other code blocks, lists, blockquotes, HTML, and multiline reference definitions, while allowing subsequent top-level diagrams to render.
- Match Pi's mixed closing-fence suffixes, space-only fence closers, whitespace-token boundaries, and HTML tag-prefix exclusions.
