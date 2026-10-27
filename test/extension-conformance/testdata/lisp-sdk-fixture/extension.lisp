(in-package "lisp-sdk-fixture")

(defun new-extension ()
  (let ((ext (pig:make-extension "lisp-sdk-fixture")))
    (loop for (nil . function) in *registrations* do (funcall function ext))
    ext))
