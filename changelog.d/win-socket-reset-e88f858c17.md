### Fixed

- Windows: a connection reset before the response headers, or a response body reset, is reported as Pi reports it ("Connection error.: read ECONNRESET", "terminated: other side closed (UND_ERR_SOCKET)") instead of a bare "Connection error." or "terminated". <!-- fix: e88f858c17ec751b54467fb5db6b02d3f8908aed -->
