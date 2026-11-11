(defpackage "provider-sibling" (:use "COMMON-LISP"))
(in-package "provider-sibling")

(defun new-extension () (pig:make-extension "provider-sibling"))
