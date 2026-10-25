;;;; SPDX-License-Identifier: MIT
;;;; Builds a runner image. Run by the host as
;;;;   sbcl --noinform --non-interactive --no-sysinit --no-userinit --disable-debugger --load build.lisp
;;;; with PIG_LISP_SDK_ROOT, PIG_LISP_SCRATCH, PIG_LISP_OUTPUT, PIG_LISP_COMPRESSION (a zstd level or "none") and
;;;; PIG_LISP_MEMBERS (JSON: [{"name","root","system"|"file"}...], empty for the standalone base image) in the environment.
;;;; ASDF sees only the SDK and the members' own roots, and writes only into the scratch directory.

(require :asdf)
(require :sb-bsd-sockets)
(require :sb-posix)

(defun env (name)
  (let ((value (sb-posix:getenv name))) (and value (plusp (length value)) value)))

(defun fail (control &rest arguments)
  (format *error-output* "pig-lisp-build: ~?~%" control arguments)
  (finish-output *error-output*)
  (sb-ext:exit :code 1 :abort t))

(defun slash (directory)
  (if (char= (char directory (1- (length directory))) #\/) directory (concatenate 'string directory "/")))

(defvar *sdk-root* (slash (or (env "PIG_LISP_SDK_ROOT") (fail "PIG_LISP_SDK_ROOT is not set"))))
(defvar *scratch* (slash (or (env "PIG_LISP_SCRATCH") (fail "PIG_LISP_SCRATCH is not set"))))
(defvar *output* (or (env "PIG_LISP_OUTPUT") (fail "PIG_LISP_OUTPUT is not set")))
(defvar *compression* (or (env "PIG_LISP_COMPRESSION") "none"))
(defvar *members-json* (or (env "PIG_LISP_MEMBERS") "[]"))

(setf *compile-verbose* nil *compile-print* nil)
(asdf:clear-configuration)
(asdf:initialize-output-translations
 `(:output-translations :ignore-inherited-configuration (t (,(concatenate 'string *scratch* "fasl/") :**/ :*.*.*))))
;; load the SDK from source so the image holds exactly the staged SDK
(asdf:initialize-source-registry
 `(:source-registry :ignore-inherited-configuration (:directory ,*sdk-root*)))
(handler-bind ((warning #'muffle-warning))
  (handler-case (asdf:load-system "pig-sdk")
    (error (e) (fail "loading the pig-sdk system: ~A" e))))

;;; the SDK is loaded: PIG: symbols can be read from here on

(defun bake (member)
  (let* ((name (pig:jget member "name")) (root (slash (pig:jget member "root")))
         (system (pig:jget member "system")) (file (pig:jget member "file"))
         (before (list-all-packages))
         (cl-user-before (let ((s (find-symbol "NEW-EXTENSION" :cl-user))) (and s (fboundp s)))))
    (handler-case
        (cond
          (system (asdf:load-system system))
          (file
           (let* ((source (concatenate 'string root file))
                  (fasl (concatenate 'string *scratch* "member-" name ".fasl")))
             (multiple-value-bind (out warnings-p failure-p)
                 (let ((*package* (find-package :cl-user))) (compile-file source :output-file fasl))
               (declare (ignore warnings-p))
               (when (or (null out) failure-p) (fail "~A: compiling ~A failed" name file))
               (let ((*package* (find-package :cl-user))) (load out)))))
          (t (fail "~A: neither a system nor a file" name)))
      (error (e) (fail "~A: ~A" name e)))
    ;; the factory is the one NEW-EXTENSION the member's load created
    (let ((candidates '()))
      (dolist (package (set-difference (list-all-packages) before))
        (let ((symbol (find-symbol "NEW-EXTENSION" package)))
          (when (and symbol (eq (symbol-package symbol) package) (fboundp symbol)) (push symbol candidates))))
      (when (and (null candidates) (not cl-user-before))
        (let ((symbol (find-symbol "NEW-EXTENSION" :cl-user)))
          (when (and symbol (eq (symbol-package symbol) (find-package :cl-user)) (fboundp symbol)) (push symbol candidates))))
      (cond ((null candidates) (fail "~A: no function NEW-EXTENSION was defined" name))
            ((cdr candidates) (fail "~A: several NEW-EXTENSION functions were defined: ~{~S~^, ~}" name candidates)))
      (pig::register-member name (symbol-function (car candidates))))))

(let* ((members (coerce (pig:from-json *members-json*) 'list))
       (roots (mapcar (lambda (m) (slash (pig:jget m "root"))) members)))
  (asdf:initialize-source-registry
   `(:source-registry :ignore-inherited-configuration (:directory ,*sdk-root*)
                      ,@(mapcar (lambda (root) `(:tree ,root)) roots)))
  (handler-bind ((warning (lambda (w) (when (typep w 'style-warning) (muffle-warning w)))))
    (mapc #'bake members)))

(sb-ext:disable-debugger)
(let ((compress (cond ((string= *compression* "none") nil)
                      ((find :sb-core-compression *features*) (parse-integer *compression*))
                      (t (fail "compression ~A was requested but this SBCL has no :sb-core-compression" *compression*)))))
  (sb-ext:save-lisp-and-die *output* :executable t :toplevel #'pig::image-main :save-runtime-options t
                                     :compression compress))
