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

(deftest ui-view-intent-a-hidden-mount-takes
  (kt-check-json "{\"name\":\"x.open\",\"arg\":{\"type\":\"object\"},\"whenHidden\":true}"
                 (pig::view-intent "x.open" (pig::view-shape "object") :when-hidden t)))

(deftest ui-view-intent-the-users-dismissal-raises
  (kt-check-json "{\"name\":\"x.close\",\"arg\":{\"type\":\"object\"},\"dismiss\":true}"
                 (pig::view-intent "x.close" (pig::view-shape "object") :dismiss t))
  (kt-check-json "{\"name\":\"x.tap\",\"arg\":{\"type\":\"object\"}}"
                 (pig::view-intent "x.tap" (pig::view-shape "object"))))

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

(deftest ui-view-abilities-changes-reach-on-view-abilities-in-order
  ;; The handler runs after the replica took the change. The first state is where the extension starts, an equal state is not a change, and
  ;; no client (NIL) differs from a client that draws no slot.
  (let ((ext (pig:make-extension "uv")) (seen '()) (lock (sb-thread:make-mutex)))
    (flet ((show (ab) (if ab
                          (format nil "slots=~{~A~^+~},mounts=~A" (coerce (pig:jget ab "slots") 'list) (pig:jget (pig:jget ab "limits") "mounts"))
                          "none"))
           (state (json) (pig::run-hooks 'pig::*state-hooks* ext (pig:from-json json)))
           (wait-for (n) (loop repeat 500 until (sb-thread:with-mutex (lock) (>= (length seen) n)) do (sleep 0.02))))
      (pig::on-view-abilities ext (lambda (ctx ab) (sb-thread:with-mutex (lock) (push (format nil "~A|~A" (show ab) (show (pig::ctx-view-abilities ctx))) seen))))
      (pig::on-view-abilities ext (lambda (ctx ab) (declare (ignore ctx)) (sb-thread:with-mutex (lock) (push (format nil "second:~A" (show ab)) seen))))
      (state "{\"uiAbilities\":{\"slots\":[\"panel\",\"status\"],\"limits\":{\"mounts\":7}}}")
      (state "{\"uiAbilities\":{\"slots\":[\"panel\",\"status\"],\"limits\":{\"mounts\":7}}}")
      (state "{\"uiAbilities\":{\"slots\":[],\"limits\":{\"mounts\":7}}}")
      (wait-for 2)
      (state "{\"hasUI\":true}")
      (wait-for 4)
      (state "{\"uiAbilities\":{\"slots\":[\"panel\",\"status\"],\"limits\":{\"mounts\":7}}}")
      (wait-for 6)
      (check-equal '("slots=,mounts=7|slots=,mounts=7" "second:slots=,mounts=7" "none|none" "second:none"
                     "slots=panel+status,mounts=7|slots=panel+status,mounts=7" "second:slots=panel+status,mounts=7")
                   (sb-thread:with-mutex (lock) (reverse seen))))))

(deftest ui-view-abilities-handler-never-runs-without-a-client
  (let* ((ext (pig:make-extension "uv")) (fired 0)
         (feed (pig::kit-state-abilities-feed (pig::kit-state ext))))
    (pig::on-view-abilities ext (lambda (ctx ab) (declare (ignore ctx ab)) (incf fired)))
    (dolist (json '("{\"hasUI\":true}" "{\"toolsExpanded\":true}" "{\"hasUI\":false}" "{\"uiAbilities\":null}"))
      (pig::run-hooks 'pig::*state-hooks* ext (pig:from-json json)))
    (check (null (pig::kit-feed-queue feed)))
    (check (null (pig::kit-feed-running feed)))
    (check-equal 0 fired)))

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

(deftest ui-view-when-wire
  (kt-check-json "{\"any\":[{\"all\":[{\"claim\":\"display.colors\",\"op\":\"atLeast\",\"value\":256},{\"claim\":\"display.hover\",\"op\":\"present\"}]}]}"
                 (pig::view-when (list (pig::view-clause "display.colors" "atLeast" 256) (pig::view-clause "display.hover" "present")))))

(deftest ui-view-alternative-keeps-its-parts
  (let ((alt (pig::view-alternative (pig::view-when (list (pig::view-clause "a.b" "present"))) :a-view :drawing :a-drawing)))
    (check-equal t (hash-table-p (first alt)))
    (check-equal :a-view (second alt))
    (check-equal :a-drawing (third alt))))

(deftest ui-view-status-carries-the-result
  (check-equal 77 (pig::view-status-result (pig::%view-status (pig::view-ref "k" 1 1) "finished" "" 77)))
  (check-equal nil (pig::view-status-result (pig::%view-status (pig::view-ref "k" 1 1) "live" ""))))
