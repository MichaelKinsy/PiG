;;;; SPDX-License-Identifier: MIT
;;;; The type engine, tool schemas and pi-ai helpers against the vectors Pi's own implementation produced (test/parity/sdk-vectors/pi-ai/*.json,
;;;; written by automation/gen/generate-sdk-vectors.mjs; the same cases extensions/sdk-py/tests read), and against the in-process fake host.
(in-package #:pig-tests)

(defun tt-vectors (export)
  "The cases of test/parity/sdk-vectors/pi-ai/<export>.json: what Pi's own function returned at the pin."
  (let ((cases (pig:jget (pig:from-json (read-text-file (repo-path (format nil "test/parity/sdk-vectors/pi-ai/~A.json" export)))) "cases")))
    (check (plusp (length cases)) export)
    cases))

(defun tt-undefined-p (value)
  (and (hash-table-p value) (equal (pig:jget value "$") "undefined")))

(defun tt-arg (value) (if (tt-undefined-p value) nil value))

(defun tt-same-p (got want)
  "Whether GOT, a helper's answer (NIL is undefined), is Pi's WANT: the JSON text, key order and number text included."
  (cond ((tt-undefined-p want) (null got))
        ((member want '(t :false)) (eq (and got (not (eq got :false)) t) (eq want t)))
        (t (and got (string= (pig:to-json got) (pig:to-json want))))))

(defparameter *tt-helpers*
  (list (cons "StringEnum" #'pig:string-enum)
        (cons "getSupportedThinkingLevels" #'pig:get-supported-thinking-levels)
        (cons "clampThinkingLevel" #'pig:clamp-thinking-level)
        (cons "calculateCost" #'pig:calculate-cost)
        (cons "contentText" #'pig:content-text)
        (cons "getCurrentTools" #'pig:get-current-tools)
        (cons "getCurrentSystemMessage" #'pig:get-current-system-message)
        (cons "getCurrentSystemPrompt" #'pig:get-current-system-prompt)
        (cons "getInitialSystemMessage" #'pig:get-initial-system-message)
        (cons "getDeclaredTools" #'pig:get-declared-tools)
        (cons "hasNonAdditiveToolChanges" #'pig:has-non-additive-tool-changes-p)
        (cons "resolveTranscriptTools" #'pig:resolve-transcript-tools)
        (cons "collapseSystemMessages" #'pig:collapse-system-messages)
        (cons "resolveTranscript" #'pig:resolve-transcript)
        (cons "withoutInitialSystemMessage" #'pig:without-initial-system-message)
        (cons "normalizeContext" #'pig:normalize-context)
        (cons "createInitialSystemMessage" #'pig:create-initial-system-message)
        (cons "getSystemMessageText" #'pig:get-system-message-text)
        (cons "renderSystemMessageUpdate" #'pig:render-system-message-update)
        (cons "isRetryableAssistantError" #'pig:retryable-assistant-error-p)
        (cons "isContextOverflow" #'pig:context-overflow-p)
        (cons "isRecoverableLength" #'pig:recoverable-length-p)
        (cons "retryDelayMs" #'pig:retry-delay-ms)
        (cons "getModelType" #'pig:get-model-type)
        (cons "isModelType" #'pig:model-type-p)
        (cons "hasApi" #'pig:has-api-p)
        (cons "modelsAreEqual" #'pig:models-are-equal-p)))

(deftest the-default-retry-delay-cap-is-pis
  (check-equal 60000 pig:+default-max-agent-retry-delay-ms+))

(deftest every-pi-ai-helper-matches-the-pinned-package
  (dolist (entry *tt-helpers*)
    (let ((failures '()))
      (loop for case across (tt-vectors (car entry))
            do (let* ((args (map 'list #'tt-arg (pig:jget case "args")))
                      (got (handler-case (apply (cdr entry) args)
                             (error (e) (format nil "raised ~A" e)))))
                 (unless (tt-same-p got (pig:jget case "result"))
                   (push (list (pig:jget case "name") (pig:jget case "args") got (pig:jget case "result")) failures))))
      (check (null failures) (format nil "~A: ~D case~:P differ, the first ~S" (car entry) (length failures) (car (last failures)))))))

;;; ---------------------------------------------------------------- envApiKeyAuth

(deftest env-api-key-auth-matches-the-pinned-package
  ;; the auth objects are the provider lane's (provider.lisp); the file that defines them is part of every SDK build
  (check (fboundp 'pig::make-api-key-auth) "provider.lisp is part of the SDK")
  (when (fboundp 'pig::make-api-key-auth)
    (loop for case across (tt-vectors "envApiKeyAuth")
          do (destructuring-bind (name env-vars credential env) (coerce (pig:jget case "args") 'list)
               (unless (equal credential "$login")
                 (let* ((auth (pig:env-api-key-auth name env-vars))
                        (seen '())
                        (input (pig::make-api-key-auth-input
                                :ctx (pig::make-auth-context :env (lambda (variable) (push variable seen) (let ((v (pig:jget env variable))) (and (stringp v) v)))
                                                             :file-exists (lambda (path) (declare (ignore path)) nil))
                                :credential (if (tt-undefined-p credential) nil (and (hash-table-p credential) credential))
                                :signal nil))
                        (result (funcall (pig::api-key-auth-resolve auth) input))
                        (want (pig:jget (pig:jget case "result") "result")))
                   (check-equal (if (eq want :null) "null"
                                    (pig:to-json want))
                                (if result
                                    (pig:to-json (let ((got (pig:obj "auth" (pig::auth-result-auth result))))
                                                   (when (pig::auth-result-env result) (setf (gethash "env" got) (pig::auth-result-env result)))
                                                   (setf (gethash "source" got) (pig::auth-result-source result))
                                                   got))
                                    "null")
                                (pig:jget case "name"))
                   (check-equal (pig:to-json (pig:jget (pig:jget case "result") "seen")) (pig:to-json (reverse seen)) (pig:jget case "name"))))))
    (let* ((prompts '())
           (auth (pig:env-api-key-auth "Name" (vector "FIRST" "SECOND")))
           (login-case (find-if (lambda (case) (equal (aref (pig:jget case "args") 2) "$login")) (tt-vectors "envApiKeyAuth")))
           (credential (funcall (pig::api-key-auth-login auth)
                                (pig::make-auth-interaction :prompt (lambda (request) (push request prompts) "typed-key")
                                                            :notify (lambda (event) (declare (ignore event)) nil) :signal nil))))
      (check-equal (pig:to-json (pig:jget (pig:jget login-case "result") "prompts")) (pig:to-json (reverse prompts)))
      (check-equal (pig:to-json (pig:jget (pig:jget login-case "result") "result")) (pig:to-json credential))
      (check (null (pig::api-key-auth-check auth)) "no check, as in Pi")
      (let ((signal (pig::make-abort-signal)))
        (pig::signal-abort signal)
        (check-error error (funcall (pig::api-key-auth-resolve (pig:env-api-key-auth "Name" (vector "FIRST")))
                                    (pig::make-api-key-auth-input
                                     :ctx (pig::make-auth-context :env (lambda (name) (declare (ignore name)) "x")
                                                                  :file-exists (lambda (path) (declare (ignore path)) nil))
                                     :credential nil :signal signal)))))))

;;; ---------------------------------------------------------------- Type vectors

(defun tt-kebab (name)
  (with-output-to-string (out)
    (loop for c across name for first = t then nil
          do (when (and (upper-case-p c) (not first)) (write-char #\- out))
             (write-char (char-downcase c) out))))

(defun tt-type-function (name)
  "The SDK function Type.<name> of Pi's is: Foo is type-foo, IsFoo is type-foo-p."
  (let* ((guard (and (> (length name) 2) (string= name "Is" :end1 2) (upper-case-p (char name 2))))
         (symbol (find-symbol (string-upcase (if guard (format nil "TYPE-~A-P" (tt-kebab (subseq name 2))) (format nil "TYPE-~A" (tt-kebab name)))) :pig)))
    (unless (and symbol (fboundp symbol)) (error "no SDK function for Type.~A" name))
    (fdefinition symbol)))

(defun tt-predicate (spec)
  "The predicate or message function a case names (the generator's predicateFor builds the same from the same spec)."
  (let ((name (pig:jget spec "$fn")))
    (cond ((equal name "always") (lambda (value) (declare (ignore value)) t))
          ((equal name "never") (lambda (value) (declare (ignore value)) nil))
          ((equal name "lengthAtLeast") (lambda (value) (>= (length (princ-to-string value)) (pig:jget spec "n"))))
          ((equal name "equals") (lambda (value) (equalp value (pig:jget spec "value"))))
          ((equal name "even") (lambda (value) (evenp value)))
          ((equal name "greaterThan") (lambda (value) (> value (pig:jget spec "n"))))
          ((member name '("throws" "throwsText" "errorThrows") :test #'equal)
           (lambda (value) (declare (ignore value)) (error "~A" (pig:jget spec "message"))))
          ((equal name "text") (lambda (value) (declare (ignore value)) (pig:jget spec "text")))
          ((equal name "textWithValue") (lambda (value) (concatenate 'string (pig:jget spec "prefix") (princ-to-string value))))
          (t (error "no predicate ~A" name)))))

(defun tt-resolve (node)
  (cond ((pig::ts-array-p node) (map 'simple-vector #'tt-resolve node))
        ((hash-table-p node)
         (cond ((equal (pig:jget node "$") "bigint") (pig:make-big-int (parse-integer (pig:jget node "value"))))
               ((stringp (pig:jget node "$fn")) (tt-predicate node))
               ((stringp (pig:jget node "$call"))
                (apply (tt-type-function (pig:jget node "$call")) (map 'list #'tt-resolve (pig:jget node "args"))))
               (t (let ((out (pig:obj)))
                    (maphash (lambda (key value) (setf (gethash key out) (tt-resolve value))) node)
                    out))))
        (t node)))

(defparameter *tt-engine-members*
  '("Call" "Capitalize" "Conditional" "Constructor" "ConstructorParameters" "Cyclic" "Dependent" "Evaluate" "Exclude" "Extends" "Extract" "Function"
    "Generic" "Identifier" "Index" "Infer" "InstanceType" "Instantiate" "Interface" "KeyOf" "Lowercase" "Mapped" "Module" "NonNullable" "Omit"
    "Parameter" "Parameters" "Partial" "Pick" "ReadonlyObject" "ReadonlyType" "RecordKey" "RecordPattern" "RecordValue" "Required" "Rest"
    "ReturnType" "Script" "TemplateLiteral" "This" "Tuple" "Uncapitalize" "Uppercase")
  "The members of Type the host's type engine serves: calling one needs a host, which these tests reach in the fake host's test and which
tests/run-engine-vectors.lisp runs case by case through the host's own handler (TestTypeEngineVectorsThroughTheHost).")

(defun tt-all-cases () (tt-vectors "Type"))

(defun tt-needs-host-p (case)
  (let ((node (aref (pig:jget case "args") 0)))
    (or (equal (pig:jget case "name") "conformance engine tool parameters")
        (and (member (pig:jget node "$call") *tt-engine-members* :test #'equal) t)
        (and (equal (pig:jget node "$call") "Record")
             (let ((key (aref (pig:jget node "args") 0)))
               (and (hash-table-p key) (member (pig:jget key "$call") '("Enum" "Intersect" "TemplateLiteral") :test #'equal) t))))))

(defun tt-canon (table)
  "TABLE as JSON text with its keys sorted."
  (let ((keys (sort (loop for key being the hash-keys of table collect key) #'string<)))
    (format nil "{~{~A~^,~}}" (mapcar (lambda (key) (format nil "~S:~A" key (pig:to-json (gethash key table)))) keys))))

(defun tt-hidden-canon (schema)
  "The hidden members of SCHEMA as the vectors record them: a ~refine list is its count."
  (let ((hidden (or (pig::ts-hidden schema) (pig:obj))) (out (pig:obj)))
    (maphash (lambda (key value) (setf (gethash key out) (if (equal key "~refine") (pig:obj "count" (length value)) value))) hidden)
    (tt-canon out)))

(defun tt-check-schema (name got want)
  "NIL when the schema GOT is Pi's WANT ({json, hidden} or {stringifyThrows, hidden}), else the difference."
  (cond
    ((not (and (hash-table-p want) (or (nth-value 1 (gethash "json" want)) (nth-value 1 (gethash "stringifyThrows" want)))))
     (format nil "~A: the SDK answers a schema, Pi ~A" name (pig:to-json want)))
    ((nth-value 1 (gethash "stringifyThrows" want))
     (let ((message (handler-case (progn (pig:type-to-json got) nil)
                      (pig:type-schema-error (e) (pig:type-schema-error-message e)))))
       (cond ((null message) (format nil "~A: wrote JSON, Pi's JSON.stringify throws ~S" name (pig:jget want "stringifyThrows")))
             ((string/= message (pig:jget want "stringifyThrows")) (format nil "~A: writing it as JSON raised ~S, Pi ~S" name message (pig:jget want "stringifyThrows"))))))
    ((string/= (pig:type-to-json got) (pig:jget want "json"))
     (format nil "~A:~% got ~A~% Pi  ~A" name (pig:type-to-json got) (pig:jget want "json")))
    ((string/= (tt-hidden-canon got) (tt-canon (pig:jget want "hidden")))
     (format nil "~A: hidden members ~A, Pi ~A" name (tt-hidden-canon got) (tt-canon (pig:jget want "hidden"))))))

(defun tt-check-case (case)
  "What the SDK answers for one vector case against Pi's: the thrown message, the schema's JSON text and hidden members (or the failure writing
a schema that holds a bigint as JSON), or the guard's boolean. NIL is a match, otherwise the difference."
  (let* ((name (pig:jget case "name")) (want (pig:jget case "result")) (thrown nil) (got nil))
    (handler-case (setf got (tt-resolve (aref (pig:jget case "args") 0)))
      (error (e) (setf thrown (princ-to-string e))))
    (cond
      ((nth-value 1 (gethash "throws" case))
       (cond ((null thrown) (format nil "~A: answered ~A, Pi throws ~S" name (if (typep got '(or hash-table vector)) (pig:to-json got) got) (pig:jget case "throws")))
             ((string/= thrown (pig:jget case "throws")) (format nil "~A: raised ~S, Pi throws ~S" name thrown (pig:jget case "throws")))))
      (thrown (format nil "~A: raised ~A, Pi answers ~A" name thrown (pig:to-json want)))
      ((pig:type-schema-p got) (tt-check-schema name got want))
      ((member want '(t :false)) (unless (eq (and got (not (eq got :false)) t) (eq want t)) (format nil "~A: ~S, Pi ~S" name got want)))
      ((stringp want) (unless (equal got want) (format nil "~A: ~S, Pi ~S" name got want)))
      (t (format nil "~A: ~S, Pi ~S" name got want)))))

(deftest engine-served-cases-are-left-to-the-host
  (let* ((cases (tt-all-cases)) (served (count-if #'tt-needs-host-p cases)))
    (check (and (> served 300) (< served (length cases))) (format nil "~D of ~D cases need the host" served (length cases)))))

(deftest type-matches-pi
  (let ((failures '()))
    (loop for case across (tt-all-cases)
          do (unless (tt-needs-host-p case)
               (let ((failure (tt-check-case case))) (when failure (push failure failures)))))
    (check (null failures) (format nil "~D case~:P differ, the first: ~A" (length failures) (car (last failures))))))

(defun tt-run-engine-cases ()
  "Run every vector case the host's type engine answers; the host is the one PIG_EXT_SOCKET names. Prints ENGINE_CASES_RUN=<n> and exits 1 on a difference."
  (let ((run 0) (failures '()))
    (loop for case across (tt-all-cases)
          do (when (tt-needs-host-p case)
               (incf run)
               (let ((failure (tt-check-case case))) (when failure (push failure failures)))))
    (dolist (failure (reverse failures)) (format t "FAIL ~A~%" failure))
    (format t "ENGINE_CASES_RUN=~D~%" run)
    (finish-output)
    (sb-ext:exit :code (if failures 1 0) :abort t)))

(deftest engine-arity-is-matched-by-count-as-pi-does
  "TypeBox's Infer, Parameter, Script and Refine match their arguments by count: a missing or extra one is Pi's \"Invalid Arguments\"."
  (dolist (call (list (lambda () (pig:type-infer))
                      (lambda () (pig:type-infer "T" (pig:type-string) (pig:type-number)))
                      (lambda () (pig:type-parameter))
                      (lambda () (pig:type-parameter "T" 1 2 3))
                      (lambda () (pig:type-script))
                      (lambda () (pig:type-refine))
                      (lambda () (pig:type-refine (pig:type-string)))
                      (lambda () (pig:type-refine (pig:type-string) (lambda (v) v) (lambda (v) v) 1))))
    (check-equal "Invalid Arguments" (handler-case (progn (funcall call) nil)
                                       (pig:type-schema-error (e) (pig:type-schema-error-message e))))))

(deftest type-omits-the-codec-family
  (dolist (name '("TYPE-CODEC" "TYPE-DECODE" "TYPE-ENCODE" "TYPE-DECODE-BUILDER" "TYPE-ENCODE-BUILDER" "TYPE-CODEC-P"))
    (check (not (find-symbol name :pig)) (format nil "~A exists; D198 records that the Codec family is not served" name))))

(defun tt-wire-cases ()
  (loop for case across (tt-all-cases)
        for result = (pig:jget case "result")
        when (and (hash-table-p result) (pig:jget result "wire") (not (nth-value 1 (gethash "~refine" (pig:jget result "hidden")))))
          collect case))

(deftest engine-wire-round-trip
  "The wire text TypeBox's own value has, read through the SDK's decoder, makes Pi's schema (the JSON text and the hidden members), and the SDK's
encoder writes something that reads back as the same schema."
  (let ((cases (tt-wire-cases)))
    (check (> (length cases) 100) "wire cases")
    (dolist (case cases)
      (let* ((want (pig:jget case "result"))
             (value (pig::ts-schema (pig::te-decode (pig:from-json (pig:jget want "wire")) (vector)))))
        (when (pig:type-schema-p value)
          (unless (vectorp value)  ; a schema made of a JavaScript array: its wire form is the plain list
            (let ((failure (tt-check-schema (pig:jget case "name") value want)))
              (check (null failure) failure))
            (let ((again (pig::ts-schema (pig::te-decode (pig:from-json (pig:to-json (pig::te-encode value (make-array 0 :adjustable t :fill-pointer t)))) (vector)))))
              (check-equal (pig:to-json value) (pig:to-json again))
              (check-equal (tt-hidden-canon value) (tt-hidden-canon again)))))))))

(deftest a-schema-holds-a-bigint-and-only-writing-it-as-json-fails
  "Pi's value holds a bigint (Type.Literal(1n), a Type.Script(\"1n\") result); JSON.stringify of it throws \"Do not know how to serialize a BigInt\"."
  (let* ((literal (pig:type-literal (pig:make-big-int (expt 10 30))))
         (nested (pig:type-object (pig:obj "a" literal)))
         (ext (pig:make-extension "bigint-probe")))
    (check (and (pig:type-literal-p literal) (equal (pig:jget literal "type") "bigint")
                (equalp (pig:jget literal "const") (pig:make-big-int (expt 10 30)))))
    (dolist (schema (list literal nested))
      (check-equal "Do not know how to serialize a BigInt"
                   (handler-case (progn (pig:type-to-json schema) nil) (pig:type-schema-error (e) (pig:type-schema-error-message e)))))
    (check-equal "Do not know how to serialize a BigInt"
                 (handler-case (progn (pig:tool ext "t" "d" nested (lambda (ctx args) (declare (ignore ctx args)) "ok")) nil)
                   (pig:type-schema-error (e) (pig:type-schema-error-message e))))
    (check-error pig:type-schema-error (pig:make-big-int 1.5d0))
    (check-error pig:type-schema-error (pig:type-literal 'symbol))))

(deftest a-bigint-crosses-the-wire-as-pi-writes-it
  (let ((wire (pig:obj (string (code-char 0)) "n" "v" "-12345678901234567890")))
    (check-equal (pig:make-big-int -12345678901234567890) (pig::te-decode wire (vector)))
    (check-equal (pig:to-json wire) (pig:to-json (pig::te-encode (pig:make-big-int -12345678901234567890) (make-array 0 :adjustable t :fill-pointer t))))))

(deftest type-builders-build-pis-schemas
  (let ((schema (pig:type-object (pig:obj "id" (pig:type-string (pig:obj "description" "Id"))
                                          "limit" (pig:type-optional (pig:type-integer (pig:obj "minimum" 1)))
                                          "tags" (pig:type-array (pig:type-string))
                                          "mode" (pig:type-union (list (pig:type-literal "fast") (pig:type-literal "deep"))))
                                 (pig:obj "additionalProperties" :false))))
    (check-equal "{\"type\":\"object\",\"required\":[\"id\",\"tags\",\"mode\"],\"properties\":{\"id\":{\"type\":\"string\",\"description\":\"Id\"},\"limit\":{\"type\":\"integer\",\"minimum\":1},\"tags\":{\"type\":\"array\",\"items\":{\"type\":\"string\"}},\"mode\":{\"anyOf\":[{\"const\":\"fast\",\"type\":\"string\"},{\"const\":\"deep\",\"type\":\"string\"}]}},\"additionalProperties\":false}"
                 (pig:type-to-json schema))
    (check (pig:type-object-p schema))
    (check (pig:type-optional-p (pig:jref schema "properties" "limit")))
    (check (not (pig:type-optional-p (pig:jref schema "properties" "id"))))))

(deftest extends-results-compute-without-the-host
  (let ((inferred (pig:obj "T" (pig:type-string))))
    (check (pig:type-extends-true-p (pig:type-extends-true inferred)))
    (check (pig:type-extends-union-p (pig:type-extends-union)))
    (check (pig:type-extends-true-like-p (pig:type-extends-union inferred)))
    (check (pig:type-extends-false-p (pig:type-extends-false)))
    (check (not (pig:type-extends-true-like-p (pig:type-extends-false))))
    (check-equal "yes" (pig:type-extends-match (pig:type-extends-true inferred) (lambda (i) (declare (ignore i)) "yes") (lambda () "no")))
    (check-equal "no" (pig:type-extends-match (pig:type-extends-false) (lambda (i) (declare (ignore i)) "yes") (lambda () "no")))))

;;; ---------------------------------------------------------------- the engine through the fake host

(defun tt-wait-running ()
  "Wait until the extension of WITH-HOST has run its ready hooks, so the SDK's host calls outside a handler ride its connection."
  (loop repeat 500 until (pig::helper-running-for nil) do (sleep 0.01)))

(defun tt-engine-reply (host id value)
  (hsend host "type" "call_result" "id" id "call_result" (pig:obj "result" (pig:obj "value" value))))

(deftest an-engine-member-asks-the-host-and-decodes-its-schema
  (let ((ext (pig:make-extension "engine-probe")) (answer nil))
    (with-host (host ext)
      (tt-wait-running)
      (let ((thread (sb-thread:make-thread (lambda () (setf answer (pig:type-pick (pig:type-object (pig:obj "id" (pig:type-string) "n" (pig:type-optional (pig:type-integer))))
                                                                                    (vector "id" "n")))))))
        (let* ((call (hread-type host "call")) (request (pig:jget (pig:jget call "call") "args"))
               (null-char (string (code-char 0))))
          (check-equal "type.engine" (pig:jget (pig:jget call "call") "method"))
          (check-equal "Pick" (pig:jget request "member"))
          (let* ((schema (aref (pig:jget request "args") 0)) (hidden (pig:jget schema "h")))
            (check-equal "o" (pig:jget schema null-char))
            (check-equal "Object" (pig:jget hidden "~kind"))
            (check-equal "{\"type\":\"object\",\"required\":[\"id\"],\"properties\":{\"id\":{\"\\u0000\":\"o\",\"h\":{\"~kind\":\"String\"},\"e\":{\"type\":\"string\"}},\"n\":{\"\\u0000\":\"o\",\"h\":{\"~kind\":\"Integer\",\"~optional\":true},\"e\":{\"type\":\"integer\"}}}}"
                         (pig:to-json (pig:jget schema "e")))
            (check-equal "[\"id\",\"n\"]" (pig:to-json (aref (pig:jget request "args") 1))))
          (tt-engine-reply host (pig:jget call "id")
                           (pig:obj null-char "o" "h" (pig:obj "~kind" "Object" "~undefined" (pig:obj null-char "u"))
                                    "e" (pig:obj "type" "object" "x" (pig:obj null-char "u")
                                                 "properties" (pig:obj "id" (pig:obj null-char "o" "h" (pig:obj "~kind" "String") "e" (pig:obj "type" "string")))))))
        (sb-thread:join-thread thread)
        (check-equal "{\"type\":\"object\",\"properties\":{\"id\":{\"type\":\"string\"}}}" (pig:type-to-json answer))
        (check-equal "Object" (pig:jget (pig::ts-hidden answer) "~kind"))))))

(deftest an-engine-error-is-what-typebox-throws
  (let ((ext (pig:make-extension "engine-error")) (message nil))
    (with-host (host ext)
      (tt-wait-running)
      (let ((thread (sb-thread:make-thread (lambda () (setf message (handler-case (progn (pig:type-capitalize (pig:type-literal "")) nil)
                                                                      (pig:type-schema-error (e) (pig:type-schema-error-message e))))))))
        (let ((call (hread-type host "call")))
          (hsend host "type" "call_result" "id" (pig:jget call "id")
                      "call_result" (pig:obj "result" (pig:obj "error" (pig:obj "message" "Cannot read properties of undefined (reading 'toUpperCase')")))))
        (sb-thread:join-thread thread)
        (check-equal "Cannot read properties of undefined (reading 'toUpperCase')" message)))))

;;; ---------------------------------------------------------------- tools whose parameters are built with Type

(deftest tool-schema-validation-precedes-registration
  (dolist (schema (list nil (vector) "object" 1 :false))
    (let ((ext (pig:make-extension "schema")))
      (check-equal "Tool \"noop\" registered by extension \"schema\" must define an object parameter schema."
                   (handler-case (progn (pig:tool ext "noop" "No-op" schema (lambda (ctx args) (declare (ignore ctx args)) "bad")) nil)
                     (error (e) (princ-to-string e))))
      (check (null (pig::extension-tools ext)))
      (check (zerop (hash-table-count (pig::extension-tool-handlers ext))))))
  (let ((ext (pig:make-extension "schema")))
    (pig:tool ext "noop" "No-op" (pig:obj) (lambda (ctx args) (declare (ignore ctx args)) "ok"))
    (check-equal "[{}]" (pig:to-json (mapcar (lambda (tool) (pig:jget tool "parameters")) (pig::extension-tools ext))))))

(deftest tool-sampling-false-presence
  ;; Pi ToolDefinition.constrainedSampling is false | config | undefined: false stays present, omission stays absent.
  (let ((ext (pig:make-extension "sampling")))
    (pig:tool ext "unset" "Unset" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore ctx params)) "ok"))
    (pig:tool ext "disabled" "Disabled" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore ctx params)) "ok") :constrained-sampling :false)
    (check (not (nth-value 1 (gethash "constrained_sampling" (first (pig::extension-tools ext))))))
    (check (eq :false (gethash "constrained_sampling" (second (pig::extension-tools ext)))))))

(defun tt-refine-extension (log)
  (flet ((checked (tag check) (lambda (value) (push (format nil "check:~A" tag) (car log)) (funcall check value)))
         (worded (tag message) (lambda (value) (push (format nil "error:~A" tag) (car log)) (funcall message value))))
    (let ((ext (pig:make-extension "refine"))
          (probe (pig:type-refine
                  (pig:type-object (pig:obj "name" (pig:type-refine (pig:type-string) (checked "len" (lambda (v) (>= (length v) 3)))
                                                                    (worded "lenmsg" (lambda (v) (declare (ignore v)) "name is too short")))
                                            "qty" (pig:type-optional (pig:type-refine (pig:type-integer) (checked "even" #'evenp)
                                                                                      (worded "evenmsg" (lambda (v) (format nil "qty must be even, got ~D" v)))))
                                            "boom" (pig:type-optional (pig:type-refine (pig:type-string) (checked "boom" (lambda (v) (declare (ignore v)) (error "kaboom")))))))
                  (checked "root" (lambda (v) (declare (ignore v)) t)) (worded "rootmsg" (lambda (v) (declare (ignore v)) "root refused")))))
      (pig:tool ext "refine-probe" "Arguments checked by refinements" probe (lambda (ctx args) (declare (ignore ctx args)) "ok")
                :prepare-arguments (lambda (args) (let ((out (pig:obj))) (maphash (lambda (k v) (setf (gethash k out) v)) args) (setf (gethash "prepared" out) t) out)))
      ext)))

(deftest a-refined-tool-declares-its-sites-and-answers-the-hosts-refinement-requests
  (let* ((log (list '())) (ext (tt-refine-extension log)))
    (with-host (host ext register)
      (let ((declaration (aref (pig:jget (pig:jget register "register") "tools") 0)))
        (check-equal "[{\"path\":\"\",\"count\":1},{\"path\":\"/properties/name\",\"count\":1},{\"path\":\"/properties/qty\",\"count\":1},{\"path\":\"/properties/boom\",\"count\":1}]"
                     (pig:to-json (pig:jget declaration "refinements")))
        (check (eq t (pig:jget declaration "prepares_arguments")))
        (check-equal "Object" (pig:jget (pig:jget declaration "validation_parameters") "~kind"))
        (check-equal "String" (pig:jget (pig:jref (pig:jget declaration "validation_parameters") "properties" "name") "~kind"))
        (check (not (nth-value 1 (gethash "~kind" (pig:jget declaration "parameters"))))))
      (flet ((refine (path index value &optional with-message)
               (request host "r" "tool_refine_arguments" "tool" "refine-probe"
                        "refine" (pig:obj "path" path "index" index "value" value "with_message" (if with-message t :false)))
               (hread-type host "response")))
        (let ((response (pig:jget (refine "/properties/name" 0 "ab" t) "response")))
          (check-equal "{\"ok\":false,\"message\":\"name is too short\"}" (pig:to-json (pig:jget response "result"))))
        (let ((response (pig:jget (refine "/properties/name" 0 "abcd") "response")))
          (check-equal "{\"ok\":true}" (pig:to-json (pig:jget response "result"))))
        (let ((response (pig:jget (refine "/properties/qty" 0 3 t) "response")))
          (check-equal "{\"ok\":false,\"message\":\"qty must be even, got 3\"}" (pig:to-json (pig:jget response "result"))))
        (let ((response (pig:jget (refine "" 0 :null t) "response")))
          (check-equal "{\"ok\":true}" (pig:to-json (pig:jget response "result"))))
        (check-equal "kaboom" (pig:jget (pig:jget (pig:jget (refine "/properties/boom" 0 "x") "response") "error") "message") "a throwing check fails the request")
        (check-equal "tool \"refine-probe\" has no refinement 5 at \"/properties/name\""
                     (pig:jget (pig:jget (pig:jget (refine "/properties/name" 5 "x") "response") "error") "message"))
        (check-equal '("check:len" "error:lenmsg" "check:len" "check:even" "error:evenmsg" "check:root" "check:boom") (reverse (car log)))))))

(deftest a-tool-prepares-its-arguments-for-the-host
  (let ((ext (tt-refine-extension (list '()))))
    (with-host (host ext)
      (request host "p1" "tool_prepare_arguments" "tool" "refine-probe" "args" (pig:obj "name" "abc"))
      (check-equal "{\"name\":\"abc\",\"prepared\":true}" (pig:to-json (pig:jget (pig:jget (hread-type host "response") "response") "result")))
      (pig:tool ext "plain" "No prepare" (pig:obj) (lambda (ctx args) (declare (ignore ctx args)) "ok"))
      (request host "p2" "tool_prepare_arguments" "tool" "plain" "args" (pig:obj))
      (check-equal "tool \"plain\" has no prepare_arguments" (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "error") "message") "no hook registered"))))

(deftest a-refinement-the-host-does-not-run-fails-registration
  (let ((ext (pig:make-extension "stranded")))
    (check-equal "Tool \"t\" registered by extension \"stranded\" has a refinement under a schema keyword whose refinements the host does not run (it runs them under properties, patternProperties, additionalProperties, items, prefixItems, additionalItems, allOf, anyOf, oneOf, not, if, then, else and $defs)."
                 (handler-case (progn (pig:tool ext "t" "d" (pig:type-object (pig:obj) (pig:obj "x-extra" (pig:type-refine (pig:type-string) (lambda (v) v))))
                                                (lambda (ctx args) (declare (ignore ctx args)) "ok"))
                                      nil)
                   (error (e) (princ-to-string e))))
    (check (null (pig::extension-tools ext)))))
