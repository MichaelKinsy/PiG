### Fixed

- Markdown no longer shows HTML comments (`<!-- ... -->`) as text in Extension Updates, What's New, `/changelog` and messages. A comment-only block disappears with one of its blank lines, so no extra gap remains; an inline comment is skipped, so `See <!-- c --> the docs.` renders `See the docs.`; an unclosed comment stays hidden while it streams. Follows Pi main 54280c4ff. <!-- fix: 348a3a8859 -->
