### Fixed

- Google (`google-generative-ai`): a request that fails before the response starts is reported with its cause, as Pi 1.5.2 does: `fetch failed: read ECONNRESET` for a connection the server reset, `fetch failed: other side closed (UND_ERR_SOCKET)` for one it closed, `fetch failed: bad port` for a base URL on a port that fetch refuses. It was a bare `fetch failed` in each case. <!-- fix: 1662063665 --> <!-- cause: the cause detail was switched on only for the stream builder made after the response headers, so a failure before them was formatted without it -->
