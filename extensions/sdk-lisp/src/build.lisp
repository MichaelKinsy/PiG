;;;; SPDX-License-Identifier: MIT
;;;; Builds a runner image. Run by the host as
;;;;   sbcl --noinform --non-interactive --no-sysinit --no-userinit --disable-debugger --load build.lisp
;;;; with PIG_LISP_SDK_ROOT, PIG_LISP_SCRATCH, PIG_LISP_OUTPUT and
;;;; PIG_LISP_EXTRA_ROOTS (JSON array of Quicklisp source trees the host resolved) and PIG_LISP_MODE=scan (write the
;;;; systems ASDF cannot find, as a JSON array, to PIG_LISP_OUTPUT instead of building) and
;;;; PIG_LISP_MEMBERS (JSON: [{"name","root","system"|"file"}...], empty for the standalone base image) in the environment.
;;;; ASDF sees only the SDK, the members' own roots and the resolved Quicklisp trees, and writes only into the scratch directory.

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
(defvar *members-json* (or (env "PIG_LISP_MEMBERS") "[]"))

(setf *compile-verbose* nil *compile-print* nil)
(asdf:clear-configuration)
;; Compiled files go to short paths of the scratch directory: under a directory per source root (r0, r1, ...) holding the
;; root-relative layout, never under a copy of the source's absolute path. Windows refuses (and SBCL there crashes on) a
;; compile output path past MAX_PATH, which the absolute path of a deep temporary or cache directory reaches. The scan compiles
;; into a directory of its own: the build that follows compiles the SDK afresh, never loading a scan's fasls.
(defvar *fasl-dir* (concatenate 'string *scratch* (if (equal (env "PIG_LISP_MODE") "scan") "scan-fasl/" "fasl/")))

(defun root-spellings (root)
  "ROOT as given and as the file system names it: ASDF finds a source by its truename (a Windows 8.3 short name, a symbolic link or a
junction spelled out), so the translation must match both."
  (let ((true (ignore-errors (probe-file root))))
    (if true (remove-duplicates (list root (namestring true)) :test #'string=) (list root))))

(defun configure-output-translations (roots)
  (asdf:initialize-output-translations
   `(:output-translations :ignore-inherited-configuration
     ,@(loop for root in roots for i from 0
             append (loop for spelling in (root-spellings root)
                          collect `(,spelling (,(format nil "~Ar~D/" *fasl-dir* i) :**/ :*.*.*))))
     (t (,*fasl-dir* :**/ :*.*.*)))))
(configure-output-translations (list *sdk-root*))
;; load the SDK from source so the image holds exactly the staged SDK
(asdf:initialize-source-registry
 `(:source-registry :ignore-inherited-configuration (:directory ,*sdk-root*)))
(handler-bind ((warning #'muffle-warning))
  (handler-case (asdf:load-system "pig-sdk")
    (error (e) (fail "loading the pig-sdk system: ~A" e))))

;;; the SDK is loaded: PIG: symbols can be read from here on

(defun builtin-system-p (name)
  (or (member name '("asdf" "uiop") :test #'string=)
      (and (> (length name) 3) (string= (subseq name 0 3) "sb-"))
      (and (> (length name) 5) (string= (subseq name 0 5) "uiop/"))
      (and (> (length name) 5) (string= (subseq name 0 5) "asdf/"))))

(defun dependency-name (spec)
  "The system a :depends-on entry names, or NIL when it names none that applies (:require, an unmet :feature)."
  (cond ((stringp spec) (string-downcase spec))
        ((and spec (symbolp spec)) (string-downcase (symbol-name spec)))
        ((and (consp spec) (eq (first spec) :version)) (dependency-name (second spec)))
        ((and (consp spec) (eq (first spec) :feature))
         (and (uiop:featurep (second spec)) (dependency-name (third spec))))
        (t nil)))

(defun unresolved-dependencies (system-names)
  "Systems the named ones need, directly or through systems ASDF finds, that ASDF cannot find in the source registry."
  (let ((seen (make-hash-table :test 'equal)) (missing '()))
    (labels ((visit-component (component)
               (dolist (spec (asdf:component-sideway-dependencies component))
                 (let ((name (dependency-name spec)))
                   ;; a serial system names the component before as a dependency: a sibling, not a system to find
                   (when (and name (not (and (asdf:component-parent component) (asdf:find-component (asdf:component-parent component) name))))
                     (visit-name name))))
               (when (typep component 'asdf:parent-component)
                 (mapc #'visit-component (asdf:component-children component))))
             (visit-name (name)
               (unless (or (gethash name seen) (builtin-system-p name))
                 (setf (gethash name seen) t)
                 (let ((system (asdf:find-system name nil)))
                   (if system (visit-component system) (push name missing))))))
      (mapc #'visit-name system-names))
    (sort missing #'string<)))

(defun write-scan (file names)
  (with-open-file (out file :direction :output :if-exists :supersede :external-format :utf-8)
    (write-string (pig:to-json (coerce names 'vector)) out)))

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
       (roots (mapcar (lambda (m) (slash (pig:jget m "root"))) members))
       (extra (mapcar #'slash (coerce (pig:from-json (or (env "PIG_LISP_EXTRA_ROOTS") "[]")) 'list))))
  (configure-output-translations (list* *sdk-root* (append roots extra)))
  (asdf:initialize-source-registry
   `(:source-registry :ignore-inherited-configuration (:directory ,*sdk-root*)
                      ,@(mapcar (lambda (root) `(:tree ,root)) roots)
                      ,@(mapcar (lambda (root) `(:tree ,root)) extra)))
  (when (equal (env "PIG_LISP_MODE") "scan")
    (let ((systems (loop for m in members for s = (pig:jget m "system") when s collect s)))
      (handler-case (write-scan *output* (unresolved-dependencies systems))
        (error (e) (fail "scanning the dependencies: ~A" e)))
      (sb-ext:exit :code 0 :abort t)))
  (handler-bind ((warning (lambda (w) (when (typep w 'style-warning) (muffle-warning w)))))
    (mapc #'bake members)))

(sb-ext:disable-debugger)
(sb-ext:save-lisp-and-die *output* :executable t :toplevel #'pig::image-main :save-runtime-options t)
