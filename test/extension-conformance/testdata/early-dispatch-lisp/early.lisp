;;;; The Lisp extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched, or writes its tag to a file when PIG_TEST_PURE_LISTENER names a directory.
;;;; Run it with sbcl --load; it loads the SDK from the repository's extensions/sdk-lisp and serves the socket PIG_EXT_SOCKET names.
(require :asdf)
(require :sb-bsd-sockets)
(require :sb-posix)
(setf *compile-verbose* nil *compile-print* nil)
(asdf:load-asd (merge-pathnames "../../../../extensions/sdk-lisp/pig-sdk.asd" (or *load-truename* *default-pathname-defaults*)))
(handler-bind ((warning #'muffle-warning))
  (asdf:load-system "pig-sdk"))

(let ((ext (pig:make-extension "early-dispatch")))
  (let ((pure (sb-posix:getenv "PIG_TEST_PURE_LISTENER")))
    (pig:events-on (pig:extension-events ext) "early:ch"
                   (if (and pure (plusp (length pure)))
                       (lambda (ctx data)
                         (declare (ignore ctx))
                         (with-open-file (out (format nil "~A/~A" pure (pig:jget data "seq")) :direction :output :if-exists :supersede)
                           (write-string (pig:jget data "tag") out)))
                       (lambda (ctx data) (pig:events-emit (pig:ctx-events ctx) "early:echo" data)))))
  (when (sb-posix:getenv "PIG_TEST_FACTORY_EMIT")
    (pig:events-emit (pig:extension-events ext) "early:factory" 1))
  (pig:run-extension ext))
