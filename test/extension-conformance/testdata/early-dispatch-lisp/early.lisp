;;;; The Lisp extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched.
;;;; Run it with sbcl --load; it loads the SDK from the repository's extensions/sdk-lisp and serves the socket PIG_EXT_SOCKET names.
(require :asdf)
(require :sb-bsd-sockets)
(require :sb-posix)
(setf *compile-verbose* nil *compile-print* nil)
(asdf:load-asd (merge-pathnames "../../../../extensions/sdk-lisp/pig-sdk.asd" (or *load-truename* *default-pathname-defaults*)))
(handler-bind ((warning #'muffle-warning))
  (asdf:load-system "pig-sdk"))

(let ((ext (pig:make-extension "early-dispatch")))
  (pig:events-on (pig:extension-events ext) "early:ch"
                 (lambda (ctx data) (pig:events-emit (pig:ctx-events ctx) "early:echo" data)))
  (pig:run-extension ext))
