;;;; SPDX-License-Identifier: MIT
;;;; Unit tests of lane S3 (model runtime, create-provider, extension runner and factories, MCP options, event bus, built-in tools) against the
;;;; in-process fake host. The values of the session-graph conformance rows (test/extension-conformance, Pi's own tables) are the reference for behavior;
;;;; the fixtures under test/extension-conformance/testdata/ reach every export, test/parity/sdk-vectors/ holds the pure vectors.
(in-package #:pig-tests)

(defun rt-ctx () (pig::make-ctx (pig:make-extension "rt")))

(defun rt-call (host)
  "The next call frame of the extension: (values method args id)."
  (let* ((frame (hread-type host "call")) (call (pig:jget frame "call")))
    (values (pig:jget call "method") (pig:jget call "args") (pig:jget frame "id"))))

(defun rt-answer (host id result) (hsend host "type" "call_result" "id" id "call_result" (pig:obj "result" result)))

(defun rt-command-host (ext name args)
  "Start EXT, run its command NAME with ARGS and return the host (the command's calls are still to be answered)."
  (multiple-value-bind (host register) (start-host ext)
    (declare (ignore register))
    (request host "r1" "command" "tool" name "args" args)
    host))

;;; ---------------------------------------------------------------- ModelRuntime wire

(deftest runtime-create-and-calls-reach-the-session-graph
  (let ((ext (pig:make-extension "rt-runtime")))
    (pig:command ext "go" "Go"
                 (lambda (ctx args) (declare (ignore args))
                   (let* ((rt (pig:runtime-create ctx :auth-path "/a" :models-path :null))
                          (model (pig:runtime-get-model rt "p" "m")))
                     (pig:ctx-notify ctx (pig:to-json model) "info")
                     (check (pig:runtime-has-configured-auth-p rt "p"))
                     (pig:runtime-set-runtime-api-key rt "p" "k"))))
    (let ((host (rt-command-host ext "go" "")))
      (unwind-protect
           (progn
             (multiple-value-bind (method args id) (rt-call host)
               (check-equal "sessionGraph.new" method)
               (check-equal "ModelRuntime" (pig:jget args "kind"))
               (check-equal "create" (pig:jget args "op"))
               (let ((options (aref (pig:jget args "args") 0)))
                 (check-equal "/a" (pig:jget options "authPath"))
                 (check (eq :null (pig:jget options "modelsPath"))))
               (rt-answer host id (pig:obj "handle" "h1")))
             (multiple-value-bind (method args id) (rt-call host)
               (check-equal "sessionGraph.call" method)
               (check-equal "h1" (pig:jget args "handle"))
               (check-equal "getModel" (pig:jget args "method"))
               (check-equal '("p" "m") (coerce (pig:jget args "args") 'list))
               (rt-answer host id (pig:obj "id" "m")))
             (multiple-value-bind (method args id) (rt-call host)
               (check-equal "ui.notify" method)
               (check-equal "{\"id\":\"m\"}" (pig:jget args "message"))
               (rt-answer host id (pig:obj)))
             (multiple-value-bind (method args id) (rt-call host)
               (check-equal "sessionGraph.call" method)
               (check-equal "hasConfiguredAuth" (pig:jget args "method"))
               (rt-answer host id t))
             (multiple-value-bind (method args id) (rt-call host)
               (check-equal "sessionGraph.async" method)
               (check-equal "setRuntimeApiKey" (pig:jget args "method"))
               (rt-answer host id :null))
             (check-equal "r1" (pig:jget (hread-type host "response") "id")))
        (stop-host host)))))

(deftest runtime-get-auth-rejects-other-types
  (let ((rt (pig::%make-model-runtime (rt-ctx) "h1")))
    (check-error simple-error (pig:runtime-get-auth rt 42))))

;;; ---------------------------------------------------------------- bound function tables

(deftest runner-tables-name-functions-by-pi-member
  (let* ((ctx (rt-ctx))
         (wire (pig::runner-table ctx (list :send-message #'identity :get-session-name nil :set-label #'identity) pig::*runner-actions* "ExtensionActions")))
    (check-equal '("sendMessage" "setLabel") (loop for key being the hash-keys of wire collect key))
    (check (stringp (pig:jget wire "sendMessage")))
    (check-error simple-error (pig::runner-table ctx (list :bogus #'identity) pig::*runner-actions* "ExtensionActions"))))

(deftest runner-signal-member-answers-the-wire-form
  (let* ((ctx (rt-ctx)) (signal (pig::make-abort-signal))
         (wire (pig::runner-table ctx (list :get-signal (lambda () signal)) pig::*runner-context-actions* "ExtensionContextActions" :get-signal))
         (callback (let ((state (pig::extension-extra (pig:ctx-extension ctx) :graph #'pig::make-graph-callbacks)))
                     (gethash (pig:jget wire "getSignal") (pig::graph-callbacks-table state)))))
    (check-equal "false" (pig:to-json (pig:jget (funcall callback) "aborted")))
    (pig::signal-abort signal)
    (check (eq t (pig:jget (funcall callback) "aborted")))))

;;; ---------------------------------------------------------------- compiled-in extension references

(deftest extension-factory-options-are-pis
  (let ((ctx (rt-ctx)))
    (let ((wire (pig:extension-factory-wire (pig:create-codemode-extension :mode "only" :inline-budget 123 :models :false) ctx)))
      (check-equal "codemode" (pig:jget wire "builtin"))
      (check-equal "only" (pig:jref wire "options" "mode"))
      (check-equal 123 (pig:jref wire "options" "inlineBudget"))
      (check (eq :false (pig:jref wire "options" "models"))))
    (let ((wire (pig:extension-factory-wire (pig:create-codemode-extension) ctx)))
      (check-equal 0 (hash-table-count (pig:jget wire "options"))))
    (check-equal "tool-search" (pig:jget (pig:extension-factory-wire (pig:create-tool-search-extension) ctx) "builtin"))
    (let* ((wire (pig:extension-factory-wire (pig:create-mcp-extension :log-path "/l" :startup-wait-ms 0 :load-config #'identity :open-url #'identity) ctx))
           (options (pig:jget wire "options")))
      (check-equal "mcp" (pig:jget wire "builtin"))
      (check-equal "/l" (pig:jget options "logPath"))
      (check-equal 0 (pig:jget options "startupWaitMs"))
      (check (stringp (pig:jget options "loadConfig")))
      (check (stringp (pig:jget options "openUrl")))
      (check (null (pig:jget options "createTransport"))))
    (check (pig:extension-factory-p (pig:create-tool-search-extension)))
    (check (not (pig:extension-factory-p "mcp")))))

;;; ---------------------------------------------------------------- MCP transports and credentials

(defun rt-shim-callback (ctx id)
  (gethash id (pig::graph-callbacks-table (pig::extension-extra (pig:ctx-extension ctx) :graph #'pig::make-graph-callbacks))))

(deftest mcp-transport-shim-serves-the-host-operations
  (let* ((ctx (rt-ctx)) (log '()) transport
         (factory (pig:create-mcp-extension
                   :create-transport (lambda (entry cwd auth)
                                       (push (list (pig:jget entry "name") cwd auth) log)
                                       (setf transport
                                             (pig:make-mcp-transport
                                              :start (lambda () (push :start log))
                                              :send (lambda (message signal) (declare (ignore signal))
                                                      (push (pig:jget message "method") log)
                                                      (pig:mcp-transport-emit-message transport (pig:obj "id" 1)))
                                              :close (lambda () (push :close log) (pig:mcp-transport-emit-close transport)))))))
         (options (pig:jget (pig:extension-factory-wire factory ctx) "options"))
         (made (funcall (rt-shim-callback ctx (pig:jget options "createTransport")) (pig:obj "name" "demo") "/c" nil))
         (serve (rt-shim-callback ctx (pig:jget made "transport"))))
    (check (eq :false (pig:jget made "setsProtocolVersion")))
    (funcall serve "start")
    (funcall serve "send" 1 (pig:obj "method" "ping") t)
    (let ((events (pig:jget (funcall serve "receive") "events")))
      (check-equal '("placed" "message") (map 'list (lambda (e) (pig:jget e "type")) events))
      (check-equal 1 (pig:jget (aref events 0) "seq")))
    (funcall serve "close")
    (check-equal '("close") (map 'list (lambda (e) (pig:jget e "type")) (pig:jget (funcall serve "receive") "events")))
    (check-equal '(:close "ping" :start ("demo" "/c" nil)) log)
    (check-error simple-error (funcall serve "bogus"))))

(deftest mcp-credentials-lock-runs-the-stores-function-until-unlock
  (let* ((ctx (rt-ctx)) (events '())
         (store (pig:make-mcp-server-store :load (lambda () (pig:obj "state" 1)) :save (lambda (state) (push (pig:jget state "n") events))
                                           :with-refresh-lock (lambda (thunk) (push :locked events) (funcall thunk) (push :unlocked events))))
         (credentials (pig:make-mcp-credential-store :for-server (lambda (name url) (declare (ignore name url)) store)
                                                     :tokens (lambda (name url) (declare (ignore url)) (pig:obj "name" name))
                                                     :remove (lambda (name url) (declare (ignore name url)) t)))
         (options (pig:jget (pig:extension-factory-wire (pig:create-mcp-extension :credentials credentials) ctx) "options"))
         (serve (rt-shim-callback ctx (pig:jget options "credentials")))
         (store-id (pig:jget (funcall serve "forServer" "demo" "http://x") "store")))
    (check-equal "demo" (pig:jget (funcall serve "tokens" "demo" "http://x") "name"))
    (check (eq t (funcall serve "remove" "demo" "http://x")))
    (check-equal 1 (pig:jget (funcall serve "load" store-id) "state"))
    (funcall serve "save" store-id (pig:obj "n" 7))
    (let ((lock (pig:jget (funcall serve "lock" store-id) "lock")))
      (check-equal '(:locked 7) events)
      (funcall serve "unlock" store-id lock nil :false)
      (check-equal '(:unlocked :locked 7) events))
    (check-error simple-error (funcall serve "load" "missing"))))

;;; ---------------------------------------------------------------- createProvider

(deftest create-provider-requires-an-implementation
  (let ((auth (pig:make-provider-auth)))
    (check-error simple-error (pig:create-provider :id "e" :auth auth :models '()))
    (check-error simple-error (pig:create-provider :id "e" :auth auth :models '() :api (pig:obj) :images (pig:obj) :classifiers (pig:obj)))))

(deftest create-provider-dispatches-on-the-models-api
  (let* ((auth (pig:make-provider-auth))
         (hit nil)
         (streams (pig:make-provider-streams :stream (lambda (model context options) (declare (ignore context options)) (setf hit (pig:jget model "id")) :streamed)
                                             :stream-simple (lambda (model context options) (declare (ignore model context options)) :simple)))
         (p (pig:create-provider :id "cp" :auth auth :name "Named" :api (pig:obj "a" streams)
                                 :models (list (pig:obj "id" "c1" "api" "a") (pig:obj "id" "i1" "api" "a" "type" "image")))))
    (check-equal "Named" (pig:provider-name p))
    (check-equal '("c1") (mapcar (lambda (m) (pig:jget m "id")) (funcall (pig:provider-get-models p))))
    (check-equal '("c1" "i1") (mapcar (lambda (m) (pig:jget m "id")) (funcall (pig:provider-get-all-models p))))
    (check-equal :streamed (funcall (pig:provider-stream p) (pig:obj "id" "c1" "api" "a") (pig:obj) nil))
    (check-equal "c1" hit)
    (check-equal :simple (funcall (pig:provider-stream-simple p) (pig:obj "id" "c1" "api" "a") (pig:obj) nil))
    (let* ((stream (funcall (pig:provider-stream p) (pig:obj "id" "c9" "api" "z" "provider" "cp") (pig:obj) nil))
           (result (pig:model-stream-result stream)))
      (check-equal "error" (pig:jget result "stopReason"))
      (check-equal "Provider cp has no API implementation for \"z\"" (pig:jget result "errorMessage")))
    (check (null (pig:provider-fetch-deferred p)))
    (check (null (pig:provider-generate-images p)))
    (check (null (pig:provider-refresh-models p)))))

(deftest create-provider-refresh-restores-then-publishes
  (let* ((auth (pig:make-provider-auth)) (publications '()) (fetched 0)
         (p (pig:create-provider
             :id "fp" :auth auth :models (list (pig:obj "id" "c1" "name" "Base" "api" "a"))
             :api (pig:make-provider-streams :stream #'identity :stream-simple #'identity)
             :fetch-models (lambda (context) (declare (ignore context)) (incf fetched) (list (pig:obj "id" "c1" "name" "Fetched" "api" "a") (pig:obj "id" "u" "type" "future"))))))
    (flet ((publish (publication) (push publication publications) (funcall (pig:models-publication-update publication)) t)
           (names () (mapcar (lambda (m) (pig:jget m "name")) (funcall (pig:provider-get-all-models p)))))
      (funcall (pig:provider-refresh-models p)
               (pig:make-refresh-models-context :publish #'publish :allow-network nil
                                                :stored (pig:obj "models" (vector (pig:obj "id" "c1" "name" "Stored" "provider" "fp" "api" "a")))))
      (check-equal '("Stored") (names))
      (check-equal 0 fetched)
      (funcall (pig:provider-refresh-models p) (pig:make-refresh-models-context :publish #'publish :allow-network t))
      (check-equal '("Fetched") (names))
      (check-equal 1 fetched)
      (check (eq pig:+models-publication-omitted+ (pig:models-publication-persist (second publications))) "the restore persists nothing")
      (check-equal 1 (length (pig:jget (pig:models-publication-persist (first publications)) "models"))))))

;;; ---------------------------------------------------------------- built-in tool options

(deftest base64-round-trips-file-contents
  (check-equal "TWFu" (pig::bt-base64-encode (sb-ext:string-to-octets "Man")))
  (check-equal "TWE=" (pig::bt-base64-encode (sb-ext:string-to-octets "Ma")))
  (check-equal "TQ==" (pig::bt-base64-encode (sb-ext:string-to-octets "M")))
  (check-equal "" (pig::bt-base64-encode (make-array 0 :element-type '(unsigned-byte 8))))
  (let ((octets (sb-ext:string-to-octets (format nil "hello, w~Arld" (code-char 246)) :external-format :utf-8)))
    (check-equal (coerce octets 'list) (coerce (pig::bt-base64-decode (pig::bt-base64-encode octets)) 'list))))

(deftest builtin-options-name-the-operations-the-host-calls-back
  (let* ((spec (pig::bt-spec-of "read" "/c" (pig:make-read-tool-options
                                              :auto-resize-images :false :resize-options (pig:make-model-image-resize-options :max-width 10)
                                              :operations (pig:make-read-operations :read-file (lambda (path) (sb-ext:string-to-octets (format nil "OPS:~A" path)))
                                                                                    :access (lambda (path) (declare (ignore path)) nil)))))
         (options (pig::bt-spec-options spec)) (callbacks (pig::bt-spec-callbacks spec)))
    (check-equal '("readFile" "access") (coerce (pig:jget options "operations") 'list))
    (check (eq :false (pig:jget options "autoResizeImages")))
    (check-equal 10 (pig:jref options "resizeOptions" "maxWidth"))
    (check-equal (pig::bt-base64-encode (sb-ext:string-to-octets "OPS:x")) (funcall (gethash "operations.readFile" callbacks) (pig:obj "path" "x"))))
  (check-error simple-error (pig::bt-spec-of "write" "/c" (pig:make-write-tool-options :operations (pig:make-write-operations :write-file #'identity))))
  (let* ((spec (pig::bt-spec-of "bash" "/c" (pig:make-bash-tool-options :command-prefix "set -e" :expose-session-environment t
                                                                         :spawn-hook (lambda (context) context))))
         (options (pig::bt-spec-options spec)))
    (check-equal "set -e" (pig:jget options "commandPrefix"))
    (check (eq t (pig:jget options "exposeSessionEnvironment")))
    (check (eq t (pig:jget options "spawnHook")))
    (check (gethash "spawnHook" (pig::bt-spec-callbacks spec))))
  (let ((spec (pig::bt-spec-of "ls" "/c" (pig:make-ls-tool-options
                                           :operations (pig:make-ls-operations :exists (lambda (p) (declare (ignore p)) t)
                                                                               :stat (lambda (p) (declare (ignore p)) (pig:make-ls-stat :is-directory (lambda () t)))
                                                                               :readdir (lambda (p) (declare (ignore p)) (list "a"))))))
    (check (eq t (pig:jget (funcall (gethash "operations.stat" (pig::bt-spec-callbacks spec)) (pig:obj "path" "x")) "isDirectory")))
    (check-equal '("a") (coerce (funcall (gethash "operations.readdir" (pig::bt-spec-callbacks spec)) (pig:obj "path" "x")) 'list))))

(deftest builtin-callbacks-are-dispatched-by-handle-and-member
  (let ((callbacks (make-hash-table :test 'equal)))
    (setf (gethash "operations.access" callbacks) (lambda (args) (pig:jget args "path")))
    (sb-thread:with-mutex (pig::*bt-lock*) (setf (gethash "builtin-test" pig::*bt-callbacks*) callbacks))
    (unwind-protect
         (let ((handler (gethash "builtin_tool_callback" pig::*request-handlers*)))
           (check-equal "p" (funcall handler nil nil (pig:obj "args" (pig:obj "handle" "builtin-test" "member" "operations.access" "args" (pig:obj "path" "p")))))
           (check-error simple-error (funcall handler nil nil (pig:obj "args" (pig:obj "handle" "builtin-test" "member" "operations.nope"))))
           (check-error simple-error (funcall handler nil nil (pig:obj "args" (pig:obj "handle" "gone" "member" "x")))))
      (sb-thread:with-mutex (pig::*bt-lock*) (remhash "builtin-test" pig::*bt-callbacks*)))))

;;; ---------------------------------------------------------------- the event bus

(deftest event-bus-registers-factory-subscriptions-before-the-register-frame
  (let* ((ext (pig:make-extension "rt-bus")) (seen '())
         (unsubscribe (pig:events-on (pig:extension-events ext) "ch" (lambda (ctx data) (declare (ignore ctx)) (push (pig:jget data "n") seen)))))
    (declare (ignore unsubscribe))
    (pig:command ext "emit" "Emit" (lambda (ctx args) (declare (ignore args)) (pig:events-emit (pig:ctx-events ctx) "ch" (pig:obj "n" 2))))
    (let* ((path (format nil "/tmp/pig-lisp-test-bus-~D-~D.sock" (sb-posix:getpid) (random 1000000)))
           (listener (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
      (ignore-errors (delete-file path))
      (sb-bsd-sockets:socket-bind listener path)
      (sb-bsd-sockets:socket-listen listener 4)
      (let* ((thread (sb-thread:make-thread (lambda () (pig::serve ext (pig::connect-unix path))) :name "test-extension"))
             (conn (pig::socket-conn (sb-bsd-sockets:socket-accept listener)))
             (host (make-host :listener listener :conn conn :thread thread :ext ext :path path)))
        (unwind-protect
             (let ((first (pig::read-frame conn)))
               (check-equal "call" (pig:jget first "type"))
               (check-equal "events.on" (pig:jref first "call" "method"))
               (check-equal "ch" (pig:jref first "call" "args" "channel"))
               (let ((id (pig:jref first "call" "args" "handlerId")))
                 (check (eq t (pig:jref first "call" "args" "value")))
                 (pig::send-frame conn (pig:obj "type" "call_result" "id" (pig:jget first "id") "call_result" (pig:obj "result" (pig:obj))))
                 (check-equal "register" (pig:jget (pig::read-frame conn) "type"))
                 (pig::send-frame conn (pig:obj "type" "ready" "ready" (pig:obj "cwd" "/w" "mode" "tui" "width" 80 "state" (pig:obj))))
                 ;; the host delivers an emit to the listener
                 (request host "d1" "events.dispatch" "args" (pig:obj "handlerId" id "channel" "ch" "json" (pig:obj "n" 1)))
                 (check-equal "d1" (pig:jget (hread-type host "response") "id"))
                 (check-equal '(1) seen)
                 ;; a dispatch for another channel is refused
                 (request host "d2" "events.dispatch" "args" (pig:obj "handlerId" id "channel" "other" "json" (pig:obj)))
                 (check (pig:jref (hread-type host "response") "response" "error"))
                 ;; the command's emit reaches the host as events.emit and an unhandled error event raises
                 (request host "c1" "command" "tool" "emit" "args" "")
                 (multiple-value-bind (method args call-id) (rt-call host)
                   (check-equal "events.emit" method)
                   (check-equal "ch" (pig:jget args "channel"))
                   (check-equal 2 (pig:jref args "json" "n"))
                   (rt-answer host call-id (pig:obj "unhandledError" t)))
                 (check-equal "c1" (pig:jget (hread-type host "response") "id"))))
          (stop-host host))))))

(deftest events-on-validates-its-arguments
  (let ((bus (pig:extension-events (pig:make-extension "rt-bus-args"))))
    (check-error simple-error (pig:events-on bus 3 #'identity))
    (check-error simple-error (pig:events-on bus "ch" "not a function"))
    (check-error simple-error (pig:events-emit bus "ch" (make-array 2 :element-type 'complex)))))
