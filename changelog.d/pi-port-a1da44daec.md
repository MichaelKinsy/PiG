### Fixed

- The durable turn classifier retries on a status code (429, 500, 502, 503, 504, 520, 524) only when it stands alone, not inside a request id or another number, and on `stream disconnected before completion`; a ChatGPT Business workspace usage-limit error is not retried. Carried forward from Pi 6e491cfa, 29057ece and cedefc53. <!-- fix: a1da44daec -->
