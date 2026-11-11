;;;; SPDX-License-Identifier: MIT
;;;; The extension points feature files plug into, so a file that adds a capability (providers, the component kit, session
;;;; operations, ...) never edits the connection loop. A hook is registered when its file loads; the loop calls it by name.

(in-package #:pig)

;;; ---------------------------------------------------------------- per-extension state of a feature file

(defun extension-extra (ext key make)
  "The state a feature file keeps on EXT under KEY (a keyword naming the file), made by calling MAKE on first use."
  (sb-thread:with-mutex ((extension-extras-lock ext))
    (multiple-value-bind (value found) (gethash key (extension-extras ext))
      (if found
          value
          (setf (gethash key (extension-extras ext)) (funcall make))))))

;;; ---------------------------------------------------------------- hook tables

(defvar *register-contributors* '()
  "Functions (lambda (ext payload)) that add members to the register payload (a hash-table), in load order.")

(defvar *ready-hooks* '()
  "Functions (lambda (ext ready)) called with the host's ready payload after registration, before any request is served.")

(defvar *state-hooks* '()
  "Functions (lambda (ext state)) called with each state_update snapshot's state object, outside every lock.")

(defvar *stop-hooks* '()
  "Functions (lambda (ext)) called when the connection ends, before handler threads are joined.")

(defvar *notify-handlers* (make-hash-table :test 'equal)
  "Notify method name -> (lambda (ext args)). Runs on the reader thread: it must not block; start a thread for slow work.")

(defvar *request-handlers* (make-hash-table :test 'equal)
  "Request method name -> (lambda (ext ctx req)); the returned value is the response's result. It runs on the request's own
thread; a condition it signals becomes the response's error message.")

(defun add-hook (variable name function)
  "Install FUNCTION as the hook NAME of the list in VARIABLE, replacing an earlier definition of NAME (a file reloaded)."
  (let ((entry (assoc name (symbol-value variable) :test #'equal)))
    (if entry
        (setf (cdr entry) function)
        (setf (symbol-value variable) (append (symbol-value variable) (list (cons name function)))))))

(defmacro def-register-contributor (name (ext payload) &body body)
  `(add-hook '*register-contributors* ,name (lambda (,ext ,payload) (declare (ignorable ,ext ,payload)) ,@body)))

(defmacro def-ready-hook (name (ext ready) &body body)
  `(add-hook '*ready-hooks* ,name (lambda (,ext ,ready) (declare (ignorable ,ext ,ready)) ,@body)))

(defmacro def-state-hook (name (ext state) &body body)
  `(add-hook '*state-hooks* ,name (lambda (,ext ,state) (declare (ignorable ,ext ,state)) ,@body)))

(defmacro def-stop-hook (name (ext) &body body)
  `(add-hook '*stop-hooks* ,name (lambda (,ext) (declare (ignorable ,ext)) ,@body)))

(defmacro def-notify (method (ext args) &body body)
  "Handle the host notify METHOD (a string literal): the wire name stays greppable at the definition."
  `(setf (gethash ,method *notify-handlers*) (lambda (,ext ,args) (declare (ignorable ,ext ,args)) ,@body)))

(defmacro def-request (method (ext ctx req) &body body)
  "Serve the host request METHOD (a string literal); BODY's value is the result."
  `(setf (gethash ,method *request-handlers*) (lambda (,ext ,ctx ,req) (declare (ignorable ,ext ,ctx ,req)) ,@body)))

(defun run-hooks (variable &rest arguments)
  "Call every hook of VARIABLE with ARGUMENTS; one that signals is reported on stderr and does not stop the others."
  (dolist (entry (symbol-value variable))
    (handler-case (apply (cdr entry) arguments)
      (error (e) (format *error-output* "pig: hook ~A failed: ~A~%" (car entry) e)))))
