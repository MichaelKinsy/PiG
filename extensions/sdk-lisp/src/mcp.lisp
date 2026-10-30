;;;; SPDX-License-Identifier: MIT
;;;; MCP server registration, and the registry of servers the host replicates.

(in-package #:pig)

(defstruct (mcp-state (:conc-name mcp-))
  (pending '())                        ; (name . declaration) in registration order; the register payload
  (servers (vector)))                  ; the host's registry as of its last push or this extension's last registration call

(defun mcp-of (ext) (extension-extra ext :mcp #'make-mcp-state))

(defun mcp-note-servers (ext reply)
  "Keep the registry a registration call answers with."
  (let ((servers (and (hash-table-p reply) (jget reply "servers"))))
    (when (and (vectorp servers) (not (stringp servers)))
      (sb-thread:with-mutex ((extension-state-lock ext)) (setf (mcp-servers (mcp-of ext)) servers)))))

(defun mcp-register (ext name config call)
  (ensure-jsonable config (format nil "MCP server config for ~A" name))
  (let* ((mcp (mcp-of ext)) (declaration (obj "name" name "config" config))
         (live (sb-thread:with-mutex ((extension-reg-lock ext))
                 (or (extension-live ext)
                     (let ((entry (assoc name (mcp-pending mcp) :test #'string=)))
                       ;; A replaced server keeps its position, as a JavaScript Map does.
                       (if entry
                           (setf (cdr entry) declaration)
                           (setf (mcp-pending mcp) (append (mcp-pending mcp) (list (cons name declaration)))))
                       nil)))))
    (when live (mcp-note-servers ext (prov-call ext call "registerMcpServer" declaration)))
    nil))

(defun mcp-unregister (ext name call)
  (let* ((mcp (mcp-of ext))
         (live (sb-thread:with-mutex ((extension-reg-lock ext))
                 (or (extension-live ext)
                     (progn (setf (mcp-pending mcp) (remove name (mcp-pending mcp) :key #'car :test #'string=)) nil)))))
    (when live (mcp-note-servers ext (prov-call ext call "unregisterMcpServer" (obj "name" name))))
    nil))

(defun register-mcp-server (ext name config)
  "Register an MCP server (Pi's registerMcpServer): queued while the factory runs and applied when the host accepts the extension,
afterwards applied at once. The host validates CONFIG (a hash-table) and rejects a name another extension owns: that signals
host-call-error after load and fails the load when it happens in the factory."
  (mcp-register ext name config nil))

(defun unregister-mcp-server (ext name)
  "Remove an MCP server this extension registered."
  (mcp-unregister ext name nil))

(defun ctx-register-mcp-server (ctx name config) (mcp-register (ctx-extension ctx) name config (prov-ctx-call ctx)))
(defun ctx-unregister-mcp-server (ctx name) (mcp-unregister (ctx-extension ctx) name (prov-ctx-call ctx)))

(defun ctx-get-mcp-servers (ctx)
  "Every registered MCP server as a vector of {\"name\", \"config\", \"extensionPath\"}: the registry as of the host's last state push
or this extension's last registration call."
  (let ((ext (ctx-extension ctx)))
    (from-json (to-json (sb-thread:with-mutex ((extension-state-lock ext)) (mcp-servers (mcp-of ext)))))))

(def-register-contributor "mcp-servers" (ext payload)
  (let ((pending (mcp-pending (mcp-of ext))))
    (when pending (setf (gethash "mcp_servers" payload) (map 'simple-vector #'cdr pending)))))

(defun mcp-apply-state (ext state)
  (let ((servers (and (hash-table-p state) (jget state "mcpServers"))))
    (when (and (vectorp servers) (not (stringp servers)))
      (sb-thread:with-mutex ((extension-state-lock ext)) (setf (mcp-servers (mcp-of ext)) servers)))))

(def-ready-hook "mcp-servers" (ext ready) (mcp-apply-state ext (jget ready "state")))
(def-state-hook "mcp-servers" (ext state) (mcp-apply-state ext state))
