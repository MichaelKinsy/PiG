### Fixed

- Bold, italic, underline, strikethrough and inverse text (markdown headings, quotes, table headers, links and emphasis, tool titles, selector titles, diff highlights) now draw only when Pi's chalk would: its stdout color level is above zero. With `CI` set (outside a known CI service), `TERM=dumb` or stdout redirected, PiG kept drawing them while Pi 1.5.0 drew only the theme colors. <!-- fix: ad0c1678f6, 0a036c9a15 -->
