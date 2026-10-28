;;;; SPDX-License-Identifier: MIT
;;;; Pi's createCodemodeExtension, createToolSearchExtension and createMcpExtension (extensions/codemode, tool-search, mcp): a reference to the
;;;; compiled-in extension of the host, which a resource loader takes in its extension factories and the host runs with the same options.

(in-package #:pig)

(declaim (ftype function mcp-register-functions))

(defstruct (extension-factory (:constructor make-extension-factory (builtin options &optional functions)))
  "A reference to a compiled-in extension of the host. FUNCTIONS are the function options of create-mcp-extension."
  builtin options functions)

(defun extension-factory-wire (factory ctx)
  "The {builtin, options} entry of a loader's extensionFactories: the data options, and each function named to the host once per loader."
  (let ((options (obj)))
    (maphash (lambda (k v) (setf (gethash k options) v)) (extension-factory-options factory))
    (when (extension-factory-functions factory)
      (mcp-register-functions ctx (extension-factory-functions factory) options))
    (obj "builtin" (extension-factory-builtin factory) "options" options)))

(defun create-codemode-extension (&key mode inline-budget (models nil models-p))
  "createCodemodeExtension(options): the codemode tool, registered inactive. MODE (\"on\" or \"only\") and INLINE-BUDGET override the codemode settings; :models :false hides the model catalog from scripts."
  (let ((options (obj)))
    (when mode (setf (gethash "mode" options) mode))
    (when inline-budget (setf (gethash "inlineBudget" options) inline-budget))
    (when models-p (setf (gethash "models" options) (graph-bool-arg models)))
    (make-extension-factory "codemode" options)))

(defun create-tool-search-extension ()
  "createToolSearchExtension(): the tool_search tool, registered inactive."
  (make-extension-factory "tool-search" (obj)))

(defun create-mcp-extension (&key load-config create-transport credentials log-path open-url update-config startup-wait-ms)
  "createMcpExtension(options): the MCP extension; the functions run in this process (see mcp-options.lisp). LOAD-CONFIG is (lambda (context)) with {cwd, isProjectTrusted}
returning Pi's LoadedMcpConfig; CREATE-TRANSPORT is (lambda (entry cwd auth-provider)) returning an mcp-transport (AUTH-PROVIDER is an mcp-auth-provider, NIL for a
server without OAuth); CREDENTIALS is an mcp-credential-store; OPEN-URL is (lambda (url)); UPDATE-CONFIG is (lambda (entry patch))."
  (let ((options (obj)))
    (when log-path (setf (gethash "logPath" options) log-path))
    (when startup-wait-ms (setf (gethash "startupWaitMs" options) startup-wait-ms))
    (make-extension-factory "mcp" options
                            (loop for (key value) on (list :load-config load-config :create-transport create-transport :credentials credentials
                                                           :open-url open-url :update-config update-config) by #'cddr
                                  when value append (list key value)))))
