### Fixed

- Google (`google-generative-ai`): a connection that fails before the response starts is reported with its cause, as Pi does, for example `fetch failed: read ECONNRESET` or `fetch failed: other side closed (UND_ERR_SOCKET)`. It was a bare `fetch failed`. <!-- fix: 1662063665 --> <!-- cause: the cause detail was switched on only for the stream builder made after the response headers, so a failure before them was formatted without it -->
