;;;; The Lisp SDK version of the writer-start fixture (../node/index.mjs).
(defpackage "writer-start" (:use "COMMON-LISP"))
(in-package "writer-start")

(defun new-extension ()
  (let ((ext (pig:make-extension "lisp")))
    (pig:on ext "session_start"
            (lambda (ctx data)
              (declare (ignore data))
              (pig:ctx-append-entry ctx "writer-start" (pig:obj "text" "from-start"))
              (pig:ctx-set-session-name ctx "start-name")
              (pig:ctx-send-message ctx "writer-start" "start-message" :display nil :trigger-turn :false)
              (pig:ctx-set-status ctx "writer" "writer: ready")))
    (pig:command ext "writer-after" "Append, name and send from a command"
                 (lambda (ctx args)
                   (declare (ignore args))
                   (pig:ctx-append-entry ctx "writer-after" (pig:obj "text" "from-command"))
                   (pig:ctx-set-session-name ctx "after-name")
                   (pig:ctx-send-message ctx "writer-after" "after-message" :display nil :trigger-turn :false)))
    ext))
