### Fixed

- With the AWS client log mode for request signing on, the Bedrock signing debug log printed the signed `x-amz-security-token` header, the session token, in clear text. The log now shows `<redacted>` for the `x-amz-security-token` and `authorization` header lines. <!-- fix: 8101d67c40 -->
