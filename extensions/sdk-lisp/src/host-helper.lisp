;;;; SPDX-License-Identifier: MIT
;;;; Host calls made outside a handler (type.engine, frontmatter.parse, ...): over the connection of a running extension, or, while a
;;;; factory runs, over the one this file dials to PIG_EXT_SOCKET and serve takes over once the extension runs.

(in-package #:pig)

(defvar *helper-lock* (sb-thread:make-mutex :name "pig helper"))
(defvar *helper-dialled* '() "(path . conn): connections a factory-time call dialled, not yet taken by serve.")
(defvar *helper-taken* '() "(conn . path): connections serve took over, so a running extension knows its socket.")
(defvar *helper-running* '() "(ext . path-or-nil) of every extension that is serving.")
(defvar *helper-sequence* 0)

(defun helper-socket-path ()
  (let ((path (sb-posix:getenv "PIG_EXT_SOCKET"))) (and path (plusp (length path)) path)))

(defun helper-running-for (path)
  "The running extension a call uses: the one on socket PATH, or with no socket named (or known) the first that runs."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (car (find-if (lambda (entry) (or (null path) (null (cdr entry)) (string= path (cdr entry)))) *helper-running*))))

(defun helper-take-conn (path)
  "The connection the factory's calls dialled to PATH (a new one when it made none), for SERVE."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (let* ((entry (assoc path *helper-dialled* :test #'string=))
           (conn (cond (entry (setf *helper-dialled* (remove entry *helper-dialled*)) (cdr entry))
                       (t (connect-unix path)))))
      (push (cons conn path) *helper-taken*)
      conn)))

(defun helper-drop-conn (path)
  "Close the connection dialled to PATH; true when there was one."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (let ((entry (assoc path *helper-dialled* :test #'string=)))
      (when entry
        (setf *helper-dialled* (remove entry *helper-dialled*))
        (ignore-errors (conn-close (cdr entry)))
        t))))

(defun helper-loading-call (path method args)
  (unless path
    (error 'host-call-error :message "not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig"))
  (sb-thread:with-recursive-lock (*helper-lock*)
    (let ((conn (let ((entry (assoc path *helper-dialled* :test #'string=)))
                  (if entry
                      (cdr entry)
                      (let ((new (connect-unix path))) (push (cons path new) *helper-dialled*) new))))
          (id (format nil "h~D" (incf *helper-sequence*))))
      (send-frame conn (obj "type" "call" "id" id "call" (obj "method" method "args" args)))
      (loop
        (let ((env (read-frame conn)))
          (when (eq env :eof) (error 'host-call-error :message "the host closed the connection"))
          (let ((type (jget env "type")))
            (cond
              ((and (equal type "call_result") (equal (jget env "id") id))
               (let* ((result (or (jget env "call_result") (obj))) (err (jget result "error")))
                 (when (hash-table-p err)
                   (error 'host-call-error :message (frame-error-message err)
                                           :code (let ((code (jget err "code"))) (and (stringp code) code))))
                 (let ((value (jget result "result"))) (return (if (eq value :null) nil value)))))
              ((equal type "ping")
               (send-frame conn (obj "type" "pong" "pong" (obj "nonce" (jget (or (jget env "ping") (obj)) "nonce" "")))))
              ((equal type "request")
               (send-frame conn (obj "type" "response" "id" (jget env "id")
                                     "response" (obj "result" :null
                                                     "error" (obj "message" (format nil "~A: the extension has not registered with the host yet"
                                                                                    (jget (or (jget env "request") (obj)) "method" ""))))))))))))))

(defun running-extension ()
  "The running extension of the socket PIG_EXT_SOCKET names (with none named, the first that runs); an error when none runs."
  (or (helper-running-for (helper-socket-path))
      (error "the extension is not running: it has not connected to the host yet")))

(defun helper-call (method &optional args)
  "The result (a hash-table; empty for null) of the host call METHOD, made outside a handler. Signals HOST-CALL-ERROR."
  (let* ((path (helper-socket-path))
         (running (helper-running-for path)))
    (or (if running (host-call running method args) (helper-loading-call path method args))
        (obj))))

(def-ready-hook "host-helper" (ext ready)
  (sb-thread:with-recursive-lock (*helper-lock*)
    (let ((entry (assoc (extension-conn ext) *helper-taken*)))
      (setf *helper-running* (append *helper-running* (list (cons ext (cdr entry))))))))

(def-stop-hook "host-helper" (ext)
  (sb-thread:with-recursive-lock (*helper-lock*)
    (setf *helper-running* (remove ext *helper-running* :key #'car)
          *helper-taken* (remove (extension-conn ext) *helper-taken* :key #'car))))
