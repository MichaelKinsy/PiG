;;;; SPDX-License-Identifier: MIT
;;;; Host calls made outside a handler (type.engine, frontmatter.parse, ...): over the connection of a running extension, or, while a
;;;; factory runs, over the one this file dials to PIG_EXT_SOCKET and serve takes over once the extension runs.

(in-package #:pig)

(defvar *helper-lock* (sb-thread:make-mutex :name "pig helper"))
(defvar *helper-dialled* '() "(path . conn): connections a factory-time call dialled, not yet taken by serve.")
(defvar *helper-taken* '() "(conn . path): connections serve took over, so a running extension knows its socket.")
(defvar *helper-running* '() "(ext . path-or-nil) of every extension that is serving.")
(defvar *helper-shutdown* '() "Connections a factory-time call read the host's shutdown frame on, which serve ends on once it has registered.")
(defvar *helper-sequence* 0)
(defvar *helper-path-locks* '() "(path . mutex): the lock of each socket path a factory-time call dialled, held for the length of one call on its connection.")

(defun helper-socket-path ()
  (let ((path (sb-posix:getenv "PIG_EXT_SOCKET"))) (and path (plusp (length path)) path)))

(defun helper-running-for (path)
  "The running extension a call uses: the one on socket PATH, or with no socket named (or known) the first that runs."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (car (find-if (lambda (entry) (or (null path) (null (cdr entry)) (string= path (cdr entry)))) *helper-running*))))

(defun helper-path-lock (path)
  "The lock that serialises the factory-time calls on the connection dialled to PATH. The host accepts a packed cell's members in order and serves a connection only after
the members before it have registered, so a call waits for the host's answer while an earlier member's run still has to take its own connection. The wait therefore holds
the lock of its own PATH and never *HELPER-LOCK*, which guards the tables for a moment and which every member's run takes."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (or (cdr (assoc path *helper-path-locks* :test #'string=))
        (let ((lock (sb-thread:make-mutex :name "pig helper connection")))
          (push (cons path lock) *helper-path-locks*)
          lock))))

(defun helper-dial (path)
  "The connection dialled to PATH, dialled now when no call has. The caller holds PATH's lock."
  (or (sb-thread:with-recursive-lock (*helper-lock*) (cdr (assoc path *helper-dialled* :test #'string=)))
      (let ((conn (connect-unix path)))
        (sb-thread:with-recursive-lock (*helper-lock*) (push (cons path conn) *helper-dialled*))
        conn)))

(defun helper-undial (path)
  "Forget the connection dialled to PATH and return it (NIL when no call dialled one). The caller holds PATH's lock."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (let ((entry (assoc path *helper-dialled* :test #'string=)))
      (when entry
        (setf *helper-dialled* (remove entry *helper-dialled*))
        (cdr entry)))))

(defun helper-forget-path-lock (path)
  "Forget PATH's lock. The caller has released it: a lock removed while held would let a thread waiting on it and a later caller use two locks for one path. A path
has one member and its factory calls run one after another, so no thread is left waiting on it once the connection is taken or dropped."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (setf *helper-path-locks* (remove path *helper-path-locks* :key #'car :test #'string=))))

(defun helper-take-conn (path)
  "The connection the factory's calls dialled to PATH (a new one when it made none), for SERVE. A call still running on that connection finishes first."
  (prog1 (sb-thread:with-recursive-lock ((helper-path-lock path))
           (let ((conn (or (helper-undial path) (connect-unix path))))
             (sb-thread:with-recursive-lock (*helper-lock*) (push (cons conn path) *helper-taken*))
             conn))
    (helper-forget-path-lock path)))

(defun helper-take-shutdown (conn)
  "True when a factory-time call read the host's shutdown frame on CONN."
  (sb-thread:with-recursive-lock (*helper-lock*)
    (when (member conn *helper-shutdown*)
      (setf *helper-shutdown* (remove conn *helper-shutdown*))
      t)))

(defun helper-drop-conn (path)
  "Close the connection dialled to PATH; true when there was one."
  (prog1 (sb-thread:with-recursive-lock ((helper-path-lock path))
           (let ((conn (helper-undial path)))
             (when conn
               (ignore-errors (conn-close conn))
               t)))
    (helper-forget-path-lock path)))

(defvar *helper-owners* '() "(path . ext): the extension whose factory-time bus calls dialled PATH, which owns the listeners the host dispatches on it.")
(defvar *helper-results* (make-hash-table :test 'equal :synchronized t) "id -> call_result of a factory-time call whose answer arrived while an inner call waited.")

(defun helper-owner (path)
  (sb-thread:with-recursive-lock (*helper-lock*) (cdr (assoc path *helper-owners* :test #'string=))))

(defun helper-note-shutdown (conn)
  (sb-thread:with-recursive-lock (*helper-lock*) (pushnew conn *helper-shutdown*)))

(declaim (ftype function event-dispatch event-release))

(defun helper-answer-request (owner env)
  "Answer a request of the host on a connection whose extension has not registered: its listener dispatches run, anything else is refused."
  (let* ((request (or (jget env "request") (obj))) (method (jget request "method" "")))
    (obj "result" :null
         "error" (cond ((and owner (equal method "events.dispatch"))
                        ;; a listener that runs while the factory loads has no request, hence no context
                        (handler-case (progn (event-dispatch owner nil (jget request "args")) :null)
                          (error (e) (obj "message" (format nil "~A" e)))))
                       (t (obj "message" (format nil "~A: the extension has not registered with the host yet" method)))))))

(defun helper-loading-call (path method args &optional owner)
  "A host call made while a factory runs, over the connection its first such call dials and SERVE later takes. It never rides a running
extension's connection: the host attributes a pi.events listener to the extension of the connection it arrives on. While it waits it answers pings
and the events.dispatch requests of OWNER's listeners; the wait is re-entrant, so a listener may make a bus call of its own. It holds the lock of PATH's
connection while it waits, and *HELPER-LOCK* only for the moment each table is read or changed."
  (unless path
    (error 'host-call-error :message "not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig"))
  (sb-thread:with-recursive-lock ((helper-path-lock path))
    (when owner
      (sb-thread:with-recursive-lock (*helper-lock*)
        (setf *helper-owners* (acons path owner (remove path *helper-owners* :key #'car :test #'string=)))))
    (let ((conn (helper-dial path))
          (id (sb-thread:with-recursive-lock (*helper-lock*) (format nil "h~D" (incf *helper-sequence*)))))
      (send-frame conn (obj "type" "call" "id" id "call" (obj "method" method "args" args)))
      (loop until (nth-value 1 (gethash id *helper-results*))
            do (let ((env (read-frame conn)))
                 (when (eq env :eof) (error 'host-call-error :message "the host closed the connection"))
                 (let ((type (jget env "type")))
                   (cond
                     ((equal type "call_result") (setf (gethash (jget env "id") *helper-results*) (or (jget env "call_result") (obj))))
                     ((equal type "ping")
                      (send-frame conn (obj "type" "pong" "pong" (obj "nonce" (jget (or (jget env "ping") (obj)) "nonce" "")))))
                     ((equal type "shutdown") (helper-note-shutdown conn))
                     ((refused-request-p env) (refuse-request conn env))
                     ((equal type "refused") (note-dropped-frame))
                     ((equal type "request")
                      (send-frame conn (obj "type" "response" "id" (jget env "id")
                                            "response" (helper-answer-request (helper-owner path) env))))
                     ((and (equal type "notify") (equal (jref env "notify" "method") "events.release"))
                      (let ((owner (helper-owner path)))
                        (when owner (event-release owner (jref env "notify" "args" "handlerId")))))))))
      (let* ((result (prog1 (gethash id *helper-results*) (remhash id *helper-results*))) (err (jget result "error")))
        (when (hash-table-p err)
          (error 'host-call-error :message (frame-error-message err)
                                  :code (let ((code (jget err "code"))) (and (stringp code) code))))
        (let ((value (jget result "result"))) (if (eq value :null) nil value))))))

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
