# PiG Common Lisp SDK (SBCL)

Write PiG extensions in Common Lisp. The SDK speaks the same wire as the Go, Rust and Python SDKs and exposes the same extension API with Lisp
names: package `pig`, kebab-case functions, a `ctx-` prefix for the per-request context.

```lisp
(defpackage "hello" (:use "COMMON-LISP"))
(in-package "hello")

(pig:define-extension "hello"
  (:tool "hello_ping" "Return a short pong." (pig:obj "type" "object")
         (lambda (ctx params) (declare (ignore ctx params)) "pong"))
  (:command "hello" "Say hello."
            (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx "hello" "info"))))
```

Create one with `pig extension init ./hello --lang lisp`. Pig builds the system into a cached SBCL image (SBCL 2.6.9; `pig setup` checks it) and
starts that image; see `docs/extension-authoring.md`, "Lisp".

## Data

| JSON | Lisp |
|---|---|
| object | `equal` hash-table, string keys, member order kept (`(pig:obj "k" v ...)` builds one) |
| array | simple-vector (a list also writes as an array; write an empty array as `#()`) |
| number | integer when the text is an integer within 2^53, else double-float; written as ECMAScript number text |
| `true` `false` `null` | `t` `:false` `:null` (`nil` writes null) |

Strings carry lone UTF-16 surrogates as characters in U+D800..U+DFFF and write them as `\udXXX` escapes, as `JSON.stringify` does.

## Served in phase 1

`make-extension`, `define-extension`, `tool`, `command`, `shortcut`, `flag`, `on`, `on-loaded`, `tool-result`, `run-extension`; the context's
`ctx-cwd`, `ctx-mode`, `ctx-has-ui-p`, `ctx-cancelled-p`, `ctx-wait-cancelled`, `ctx-signal`, `ctx-on-update`, `ctx-notify`, `ctx-set-status`,
`ctx-set-title`, `ctx-select`, `ctx-confirm`, `ctx-input`, `ctx-editor`, `ctx-exec`, `ctx-is-idle-p`, `ctx-abort`, `ctx-shutdown`, `ctx-compact`,
`ctx-reload`, `ctx-get-flag`, `ctx-get-system-prompt`, `ctx-get-thinking-level`, `ctx-get-all-tools`, `ctx-get-active-tools`, `ctx-send-message`,
`ctx-send-user-message`, `ctx-append-entry`, `ctx-set-session-name`, `ctx-session-manager` (`session-get-*`), `ctx-model-registry`
(`registry-has-configured-auth`, `registry-is-using-oauth`), `ctx-footer-data` (`footer-*`), and `ctx-call-host` for any other wire method.
Everything else signals `pig:unsupported-capability` and is a `gap:SdkLispPhase2` cell in `test/parity/sdk-exports.toml`.

## Tests

`make test-sdk-lisp` runs `sbcl --script extensions/sdk-lisp/tests/run.lisp`: the JSON vectors (copied from the Rust and Python SDK tests) and the SDK
against an in-process fake host. The conformance rows run through the real host in `test/extension-conformance`.
