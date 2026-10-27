### Fixed

- The login dialog opens and prints only `http:` and `https:` URLs, in their normalized form (percent-encoded control characters); any other URL is shown with its control characters replaced and is not opened in the browser, and no terminal hyperlink is made for it. Before, a URL from a login server could end a terminal hyperlink early or send its own escape sequences. <!-- fix: ae1ee6d132 -->
- A terminal title that holds a control character (a session name from a session file with an ESC or BEL) no longer ends the title sequence early and sends the rest to the terminal as escape sequences: control characters become spaces, as in Pi's `setTitle`. <!-- fix: ae1ee6d132 -->
