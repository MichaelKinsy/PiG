;;;; SPDX-License-Identifier: MIT
;;;; The extension's log, where the SDK writes its own diagnostics.
;;;;
;;;; The host forwards the extension process's stderr to the user's terminal, and Pi writes no stack for a throwing handler (print mode reports one line,
;;;; interactive mode shows one), so a diagnostic of the SDK's own must not go there: a handler that signaled, a host call that failed, a frame the SDK
;;;; dropped. The host names the process's log in PIG_EXT_LOG (the log its load and crash diagnostics read, which also receives everything the process writes
;;;; to stderr), and the SDK appends one whole diagnostic per write. With no log, or one that cannot be written, the diagnostic goes to stderr, as it does when
;;;; no host started the process. What the extension's own code writes stays on stderr.

(in-package #:pig)

(defconstant +diagnostic-log-max+ (ash 1 20)
  "The most the SDK writes to the log. The host bounds what a process writes to stderr, and a handler that signals on every event must not fill the disk through this
path. The diagnostic that crosses the bound is the last.")

(defvar *diagnostic-written* 0 "What this process asked to write to the log, in bytes.")
(defvar *diagnostic-lock* (sb-thread:make-mutex :name "pig diagnostic"))

(defun append-to-log (path text)
  "Write TEXT in one write to the end of the file at PATH, which the host opened for appending too. True when it was written."
  (handler-case
      (let ((octets (sb-ext:string-to-octets text :external-format :utf-8))
            (fd (sb-posix:open path (logior sb-posix:o-wronly sb-posix:o-append))))
        (unwind-protect
             (sb-sys:with-pinned-objects (octets)
               (sb-posix:write fd (sb-sys:vector-sap octets) (length octets))
               t)
          (sb-posix:close fd)))
    (error () nil)))

(defun write-diagnostic (path text)
  "Write the diagnostic TEXT, which carries its own newline, to the log at PATH (when PATH is a non-empty string) or to stderr."
  (when (and path (plusp (length path)))
    (let* ((size (length (sb-ext:string-to-octets text :external-format :utf-8)))
           (before (sb-thread:with-mutex (*diagnostic-lock*)
                     (prog1 *diagnostic-written* (incf *diagnostic-written* size)))))
      (cond ((> (+ before size) +diagnostic-log-max+)
             (when (<= before +diagnostic-log-max+)
               (append-to-log path (format nil "~%[... further SDK diagnostics dropped ...]~%")))
             (return-from write-diagnostic nil))
            ((append-to-log path text) (return-from write-diagnostic t)))))
  (ignore-errors (write-string text *error-output*) (finish-output *error-output*))
  nil)

(defun diagnose (control &rest arguments)
  "Write one SDK-owned diagnostic; CONTROL and ARGUMENTS are FORMAT's and CONTROL ends with ~%. It never signals: one that cannot be written does not end the extension."
  (write-diagnostic (sb-posix:getenv "PIG_EXT_LOG") (apply #'format nil control arguments)))
