;;;; SPDX-License-Identifier: MIT
;;;; Tools whose parameters are built with Type. The host validates a call's arguments as plain JSON Schema, so the schema's hidden members
;;;; (~kind, ~refine) never leave this process.

(in-package #:pig)

(defun tool-schema-guard (schema)
  "Signal Pi's error for a schema that holds a bigint, which JSON.stringify cannot write."
  (when (ts-holds-big-int schema) (ts-fail "Do not know how to serialize a BigInt")))
