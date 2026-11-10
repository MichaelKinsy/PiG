;;;; SPDX-License-Identifier: MIT
;;;; D614 plugin UI mounts as the Lisp SDK builds and reads them. The behavior against the host is the shared conformance row
;;;; TestConformance_UIViewMounts; these cases pin the wire records and the replicated-state decoding.
(in-package #:pig-tests)

(deftest ui-view-intent-wire-names
  (let ((intent (pig::view-intent "x.go"
                                  (pig::view-shape "object" :properties (list (cons "n" (pig::view-shape "integer"))) :required '("n"))
                                  :node-key "btn"
                                  :answer (pig::view-shape "array" :items (pig::view-shape "string") :max-items 3)
                                  :cancellable t :timeout-ms 500)))
    (kt-check-json "{\"name\":\"x.go\",\"arg\":{\"type\":\"object\",\"properties\":{\"n\":{\"type\":\"integer\"}},\"required\":[\"n\"]},\"nodeKey\":\"btn\",\"answer\":{\"type\":\"array\",\"items\":{\"type\":\"string\"},\"maxItems\":3},\"cancellable\":true,\"timeoutMs\":500}"
                   intent)))

(deftest ui-view-target-sets-one-member
  (kt-check-json "{\"toolCallId\":\"c1\"}" (pig::view-target :tool-call-id "c1")))

(deftest ui-view-ref-wire
  (kt-check-json "{\"key\":\"k\",\"epoch\":3,\"rev\":2}" (pig::view-ref-wire (pig::view-ref "k" 3 2))))

(deftest ui-view-abilities-decode-and-absence-clears
  (let ((ext (pig:make-extension "uv")))
    (pig::run-hooks 'pig::*state-hooks* ext
                    (pig:from-json "{\"uiAbilities\":{\"slots\":[\"panel\"],\"markdown\":true,\"limits\":{\"mounts\":7}}}"))
    (let ((ab (pig::extension-view-abilities ext)))
      (check (hash-table-p ab))
      (check-equal 7 (pig:jget (pig:jget ab "limits") "mounts")))
    (pig::run-hooks 'pig::*state-hooks* ext (pig:from-json "{}"))
    (check (null (pig::extension-view-abilities ext)))
    (pig::run-hooks 'pig::*ready-hooks* ext (pig:from-json "{\"state\":{\"uiAbilities\":{\"slots\":[\"x\"]}}}"))
    (check (hash-table-p (pig::extension-view-abilities ext)))))

(deftest ui-view-intent-without-handler-is-refused
  (let* ((ext (pig:make-extension "uv"))
         (handler (gethash "ui.view.intent" pig::*request-handlers*))
         (out (funcall handler ext nil (pig:from-json "{\"args\":{\"intent\":\"x.go\"}}"))))
    (check-equal "refused" (pig:jget out "outcome"))))

(deftest ui-view-handler-result-defaults-to-done
  (let* ((ext (pig:make-extension "uv")) (seen nil)
         (handler (gethash "ui.view.intent" pig::*request-handlers*)))
    (pig::on-view-intent ext (lambda (ctx invocation) (declare (ignore ctx)) (setf seen invocation) (pig::view-intent-result "done" :answer (pig:from-json "{\"ok\":1}"))))
    (let ((out (funcall handler ext nil (pig:from-json "{\"args\":{\"mount\":{\"key\":\"k\",\"epoch\":2},\"rev\":5,\"intent\":\"x.go\",\"ticket\":\"t7\"}}"))))
      (check-equal "done" (pig:jget out "outcome"))
      (check-equal "t7" (pig:jget seen "ticket"))
      (check-equal 5 (pig:jget seen "rev")))))
