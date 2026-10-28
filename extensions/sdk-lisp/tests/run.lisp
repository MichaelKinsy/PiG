;;;; SPDX-License-Identifier: MIT
;;;; sbcl --script tests/run.lisp : the SDK's own tests, no network. The files and their order are pig-sdk.asd's: the sources of the
;;;; "pig-sdk" system, then the files of "pig-sdk/tests".
(require :asdf)
(require :sb-bsd-sockets)
(require :sb-posix)
(setf *compile-verbose* nil *compile-print* nil)
(defparameter *root*
  (merge-pathnames "../" (make-pathname :name nil :type nil :defaults (or *load-truename* *default-pathname-defaults*))))

(defun system-files (name)
  "The files of the system NAME in pig-sdk.asd, in order, as paths under *root* without the .lisp type."
  (with-open-file (in (merge-pathnames "pig-sdk.asd" *root*))
    (let ((*package* (find-package :cl-user)))
      (loop for form = (read in nil :eof)
            until (eq form :eof)
            do (when (and (consp form) (equal (second form) name))
                 (let* ((plist (cddr form))
                        (directory (getf plist :pathname))
                        (components (getf plist :components)))
                   (return (loop for component in components
                                 when (and (consp component) (eq (first component) :file))
                                   collect (concatenate 'string directory (second component))))))))))

(handler-bind ((warning #'muffle-warning))
  (dolist (file (append (system-files "pig-sdk") (system-files "pig-sdk/tests")))
    (load (merge-pathnames (concatenate 'string file ".lisp") *root*))))
(funcall (intern "RUN-ALL" "PIG-TESTS"))
