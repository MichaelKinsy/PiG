### Fixed

- Highlighted code in a markdown block or a tool card, and the label of a legacy bash entry in the session tree, no longer measure a lone UTF-16 surrogate one cell wide. highlight.js works on UTF-16 units and can split a surrogate pair between two scopes, and PiG encoded the leftover unit as U+FFFD before wrapping and truncating the row, so a row could wrap or clip one cell earlier than in Pi. The unit now stays nonprinting while the row is measured and is encoded when it is written to the terminal, as in Pi. <!-- fix: a97bb034e5171c4465cc3ba0f83061e772cecee5 -->
