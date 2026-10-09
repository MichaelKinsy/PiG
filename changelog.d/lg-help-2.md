### Fixed
- An extension's `ui.setHiddenThinkingLabel` now changes the label shown for hidden thinking in the chat, including assistant messages already on screen. It only changed a footer string before.
- Tab at the start of a line no longer forces file completion while the text before the cursor is a slash-command name.
- The `/login` and `/logout` provider picker and the `/tree` selector show the terminal cursor in their search and label inputs only while they hold focus, as Pi does. The picker's search input claimed focus even when it had none.
