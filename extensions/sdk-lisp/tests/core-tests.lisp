;;;; SPDX-License-Identifier: MIT
;;;; The core context members (context-extra.lisp) against the fake host.
(in-package #:pig-tests)

(defvar *core-thunk* nil)
(defvar *core-result* nil)

(defun core-extension ()
  "An extension with the command c (runs *core-thunk* in a command), the tool t (runs it in a tool call) and the declared tools of the hooks."
  (let ((ext (pig:make-extension "core")))
    (pig:command ext "c" "C" (lambda (ctx args) (declare (ignore args)) (setf *core-result* (funcall *core-thunk* ctx)))
                 :get-argument-completions (lambda (prefix) (list (concatenate 'string prefix "-one") "two")))
    (pig:tool ext "t" "T" (pig:obj "type" "object")
              (lambda (ctx params) (declare (ignore params)) (setf *core-result* (funcall *core-thunk* ctx)) "ok")
              :prepare-arguments (lambda (args) (pig:obj "prepared" (pig:jget args "n"))))
    (pig:register-tool ext "d" :label "D" :description "Defined" :parameters (pig:obj "type" "object")
                              :execute (lambda (ctx params) (declare (ignore ctx params)) "d")
                              :exposure "deferred" :default-active :false :execution-mode "parallel"
                              :prepare-loadout (lambda (loadout)
                                                 (pig:obj "descriptions" (pig:obj "d" (format nil "~A/~A" (pig:tool-loadout-get-exposure loadout "d")
                                                                                              (pig:tool-loadout-get-exposure loadout "zzz")))))
                              :reaches-tools (lambda (input) (if (pig:jget input "all") "all" (list "x" "y"))))
    ext))

(defun run-command (host thunk &optional (id "r"))
  (setf *core-thunk* thunk)
  (request host id "command" "tool" "c" "args" "")
  (pig:jget (hread-type host "response") "response"))

(defun answer-call (host call result)
  (hsend host "type" "call_result" "id" (pig:jget call "id") "call_result" (pig:obj "result" result)))

(deftest core-state-is-replicated-from-ready-and-state-updates
  (let ((*ready-state* (pig:from-json "{\"model\":{\"id\":\"m1\",\"provider\":\"p1\"},\"session\":{\"sessionName\":\"nm\"},\"settings\":{\"a\":{\"b\":1}}}"))
        (*ready-extra* (list "sourceInfo" (pig:obj "path" "/x.lisp" "scope" "user"))))
    (with-host (host (core-extension))
      (run-command host (lambda (ctx) (setf *core-result* (list (pig:ctx-model ctx) (pig:ctx-model-provider ctx) (pig:ctx-model-qualified ctx)
                                                                (pig:ctx-session-name ctx) (pig:jget (pig:ctx-extension-source-info ctx) "path")
                                                                (pig:jref (pig:ctx-get-settings ctx) "a" "b")))))
      (check-equal '("m1" "p1" "p1/m1" "nm" "/x.lisp" 1) *core-result*)
      (hsend host "type" "notify" "notify" (pig:obj "method" "state_update"
                                                    "args" (pig:obj "state" (pig:from-json "{\"model\":null,\"session\":{\"sessionName\":\"\"}}"))))
      (loop repeat 200 until (equal "" (progn (run-command host (lambda (ctx) (pig:ctx-model ctx)) "q") *core-result*)) do (sleep 0.01))
      (check-equal "" *core-result*)
      (run-command host (lambda (ctx) (list (pig:ctx-model-qualified ctx) (pig:ctx-session-name ctx))) "q2")
      (check-equal '("" "") *core-result*))))

(deftest settings-are-unavailable-until-the-host-sends-them
  (with-host (host (core-extension))
    (run-command host (lambda (ctx) (handler-case (pig:ctx-get-settings ctx) (error (e) (princ-to-string e)))))
    (check-equal "settings are not available: the host has not sent them" *core-result*)))

(deftest config-home-follows-the-environment
  (flet ((home (pig-home xdg home)
           (sb-posix:setenv "PIG_HOME" pig-home 1) (sb-posix:setenv "XDG_CONFIG_HOME" xdg 1) (sb-posix:setenv "HOME" home 1)
           (pig:ctx-config-home nil)))
    (check-equal "/h/.pig" (home "" "" "/h"))
    (check-equal "/x/pig" (home "" "/x//y/.." "/h"))
    (check-equal "/h/a" (home "~/a/../a" "/x" "/h"))
    (check-equal "~u/a" (home "~u/a" "" "/h"))
    (check-equal "/h" (home "~" "" "/h"))))

(deftest tool-input-and-params-keep-javascripts-key-order
  (with-host (host (core-extension))
    (setf *core-thunk* (lambda (ctx) (list (pig:ctx-tool-input ctx) (pig:to-json (pig:ctx-tool-params ctx)) (pig:ctx-tool-call-index ctx))))
    (request host "r" "tool_call" "tool" "t" "tool_call_id" "c1" "tool_call_index" 3
                  "args" (pig:from-json "{\"b\":1,\"10\":2,\"2\":{\"z\":1,\"1\":0},\"a\":[{\"y\":1,\"0\":2}]}"))
    (hread-type host "response")
    (check-equal '("{\"2\":{\"1\":0,\"z\":1},\"10\":2,\"b\":1,\"a\":[{\"0\":2,\"y\":1}]}"
                   "{\"2\":{\"1\":0,\"z\":1},\"10\":2,\"b\":1,\"a\":[{\"0\":2,\"y\":1}]}" 3)
                 (list (first *core-result*) (second *core-result*) (third *core-result*)))
    (run-command host (lambda (ctx) (list (pig:ctx-tool-input ctx) (pig:ctx-tool-params ctx) (pig:ctx-tool-call-index ctx))) "q")
    (check-equal '(nil nil nil) *core-result*)))

(deftest exec-with-a-signal-sends-its-own-id-and-cancels
  (let ((signal (pig:make-abort-signal)))
    (with-host (host (core-extension))
      (setf *core-thunk* (lambda (ctx) (let ((r (pig:ctx-exec ctx "sleep" :args '("1") :signal signal)))
                                         (list (pig:exec-result-killed r) (pig:exec-result-exit-code r) (pig:exec-result-stdout r)))))
      (request host "r" "command" "tool" "c" "args" "")
      (let* ((call (hread-type host "call")) (args (pig:jget (pig:jget call "call") "args")))
        (check-equal "exec" (pig:jget (pig:jget call "call") "method"))
        (check (not (pig:jget (pig:jget call "call") "parent_request_id")) "an owned signal replaces the request's cancellation")
        (check (eq t (pig:jget args "ownSignal")))
        (pig:signal-abort signal)
        (let ((cancel (hread-type host "call")))
          (check-equal "exec.cancel" (pig:jget (pig:jget cancel "call") "method"))
          (check-equal (pig:jget args "execId") (pig:jget (pig:jget (pig:jget cancel "call") "args") "execId"))
          (answer-call host cancel nil))
        (answer-call host call (pig:obj "stdout" "so far" "code" 0 "killed" t))
        (hread-type host "response")
        (check-equal '(t 0 "so far") *core-result*)))))

(deftest execute-tool-delivers-updates-in-order-and-returns-the-outcome
  (let ((updates '()))
    (with-host (host (core-extension))
      (setf *core-thunk* (lambda (ctx)
                           (pig:ctx-execute-tool ctx "other" (pig:obj "q" 1) :on-update (lambda (p) (push (pig:jget p "n") updates)))))
      (request host "r" "tool_call" "tool" "t" "tool_call_id" "c1")
      (let* ((call (hread-type host "call")) (payload (pig:jget (pig:jget call "call") "args")))
        (check-equal "executeTool" (pig:jget (pig:jget call "call") "method"))
        (check-equal "c1" (pig:jget payload "callerId"))
        (check (eq t (pig:jget payload "wantsUpdates")))
        (check (not (pig:jget payload "ownSignal")))
        (check-equal "r" (pig:jget (pig:jget call "call") "parent_request_id"))
        (dolist (n '(1 2))
          (request host (format nil "u~D" n) "execute_tool_update" "args" (pig:obj "executeId" (pig:jget payload "executeId") "result" (pig:obj "n" n)))
          (check-equal nil (pig:jget (pig:jget (hread-type host "response") "response") "error")))
        (check-equal '(2 1) updates)
        (answer-call host call (pig:obj "isError" t "result" (pig:obj "content" "x")))
        (hread-type host "response")
        (check (eq t (pig:jget *core-result* "isError")))))))

(deftest a-failing-on-update-answers-the-host-and-rejects-the-call
  (with-host (host (core-extension))
    (setf *core-thunk* (lambda (ctx) (handler-case (pig:ctx-execute-tool ctx "o" (pig:obj) :on-update (lambda (p) (declare (ignore p)) (error "update boom")))
                                       (error (e) (princ-to-string e)))))
    (request host "r" "tool_call" "tool" "t" "tool_call_id" "c1")
    (let* ((call (hread-type host "call")) (id (pig:jget (pig:jget (pig:jget call "call") "args") "executeId")))
      (request host "u" "execute_tool_update" "args" (pig:obj "executeId" id "result" (pig:obj)))
      (check-equal "update boom" (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "error") "message"))
      (answer-call host call (pig:obj))
      (hread-type host "response")
      (check-equal "update boom" *core-result*))))

(deftest tools-and-execute-tool-need-a-running-tool
  (with-host (host (core-extension))
    (run-command host (lambda (ctx) (list (handler-case (pig:ctx-tools ctx) (error (e) (princ-to-string e)))
                                          (handler-case (pig:ctx-execute-tool ctx "x" (pig:obj)) (error (e) (princ-to-string e))))))
    (check-equal '("ctx-tools is only available while a tool runs" "ctx-execute-tool is only available while a tool runs") *core-result*)))

(deftest the-session-log-mirror-serves-branch-and-entries
  (with-host (host (core-extension))
    (setf *core-thunk* (lambda (ctx) (list (map 'list (lambda (e) (pig:jget e "id")) (pig:ctx-get-branch ctx))
                                           (length (pig:ctx-get-entries ctx)))))
    (request host "r" "command" "tool" "c" "args" "")
    (let ((first (hread-type host "call")))
      (check-equal "watchSessionLog" (pig:jget (pig:jget first "call") "method"))
      (check-equal 0 (pig:jget (pig:jget (pig:jget first "call") "args") "cursor"))
      (answer-call host first (pig:from-json "{\"entries\":[{\"id\":\"a\"},{\"id\":\"b\",\"parentId\":\"a\"},{\"id\":\"c\",\"parentId\":\"a\"}],\"entryCount\":3,\"leafId\":\"b\"}")))
    (let ((second (hread-type host "call")))
      (check (eq t (pig:jget (pig:jget (pig:jget second "call") "args") "complete")))
      (check-equal 3 (pig:jget (pig:jget (pig:jget second "call") "args") "cursor"))
      (answer-call host second (pig:from-json "{\"entries\":[],\"entryCount\":3,\"leafId\":\"b\"}")))
    (hread-type host "response")
    (check-equal '(("a" "b") 3) *core-result*)
    ;; a pushed append moves the leaf; the next read needs no host call
    (hsend host "type" "notify" "notify"
           (pig:obj "method" "state_update"
                    "args" (pig:obj "state" (pig:obj "session" (pig:from-json "{\"entriesAppended\":[{\"id\":\"d\",\"parentId\":\"c\"}],\"entryCount\":4,\"leafId\":\"d\"}")))))
    (loop repeat 200 until (progn (run-command host (lambda (ctx) (length (pig:ctx-get-entries ctx))) "q") (eql 4 *core-result*)) do (sleep 0.01))
    (run-command host (lambda (ctx) (map 'list (lambda (e) (pig:jget e "id")) (pig:ctx-get-branch ctx))) "q2")
    (check-equal '("a" "c" "d") *core-result*)))

(deftest tool-hooks-answer-the-hosts-requests
  (with-host (host (core-extension) register)
    (let ((d (find "d" (pig:jget (pig:jget register "register") "tools") :key (lambda (x) (pig:jget x "name")) :test #'equal))
          (tool (find "t" (pig:jget (pig:jget register "register") "tools") :key (lambda (x) (pig:jget x "name")) :test #'equal)))
      (check-equal "deferred" (pig:jget d "exposure"))
      (check (eq :false (pig:jget d "default_active")))
      (check (eq t (pig:jget d "prepares_loadout")))
      (check (eq t (pig:jget d "reaches_tools")))
      (check-equal "parallel" (pig:jget d "execution_mode"))
      (check (eq t (pig:jget tool "prepares_arguments"))))
    (flet ((result (id method &rest members)
             (apply #'request host id method members)
             (pig:jget (pig:jget (hread-type host "response") "response") "result")))
      (check-equal 7 (pig:jget (result "p" "tool_prepare_arguments" "tool" "t" "args" (pig:obj "n" 7)) "prepared"))
      (check-equal "all" (result "r1" "tool_reaches_tools" "tool" "d" "args" (pig:obj "all" t)))
      (check-equal '("x" "y") (coerce (result "r2" "tool_reaches_tools" "tool" "d" "args" (pig:obj)) 'list))
      (check-equal "deferred/direct"
                   (pig:jget (pig:jget (result "l" "tool_prepare_loadout" "tool" "d"
                                               "args" (pig:from-json "{\"declared\":[],\"callable\":[],\"registered\":[],\"exposures\":{\"d\":\"deferred\"}}"))
                                       "descriptions")
                             "d"))
      (check-equal '("a-one" "two") (coerce (result "cc" "command_argument_completions" "tool" "c" "args" "a") 'list)))))

(deftest get-all-tools-turns-the-reaches-tools-mark-into-a-function
  (with-host (host (core-extension))
    (setf *core-thunk* (lambda (ctx) (let ((tool (aref (pig:ctx-get-all-tools ctx) 0))) (funcall (pig:jget tool "reachesTools") (pig:obj "k" 1)))))
    (request host "r" "command" "tool" "c" "args" "")
    (let ((all (hread-type host "call")))
      (answer-call host all (pig:from-json "{\"tools\":[{\"name\":\"d\",\"reachesTools\":true}]}")))
    (let* ((call (hread-type host "call")) (args (pig:jget (pig:jget call "call") "args")))
      (check-equal "tool.reachesTools" (pig:jget (pig:jget call "call") "method"))
      (check-equal "d" (pig:jget args "tool"))
      (check-equal 1 (pig:jget (pig:jget args "input") "k"))
      (answer-call host call "all"))
    (hread-type host "response")
    (check-equal "all" *core-result*)))

(deftest compact-with-callbacks-returns-at-once-and-reports-later
  (let ((completed nil))
    (with-host (host (core-extension))
      (run-command host (lambda (ctx) (pig:ctx-compact ctx :custom-instructions "be brief" :on-complete (lambda (r) (setf completed r)))))
      (let ((call (hread-type host "call")))
        (check-equal "compact" (pig:jget (pig:jget call "call") "method"))
        (check (eq t (pig:jget (pig:jget (pig:jget call "call") "args") "awaitCompletion")))
        (check-equal "be brief" (pig:jget (pig:jget (pig:jget call "call") "args") "customInstructions"))
        (answer-call host call (pig:obj "tokensBefore" 9))
        (loop repeat 200 until completed do (sleep 0.01))
        (check-equal 9 (pig:jget completed "tokensBefore"))))))

(deftest user-bash-operations-are-kept-and-run-by-handle
  (let ((ext (pig:make-extension "bash")))
    (pig:on ext "user_bash" (lambda (ctx data) (declare (ignore ctx data))
                              (pig:obj "operations" (pig:make-bash-operations
                                                     (lambda (command cwd options)
                                                       (funcall (pig:bash-exec-options-on-data options) (format nil "~A@~A" command cwd))
                                                       (pig:obj "exitCode" 3))))))
    (with-host (host ext)
      (request host "e" "event" "event" "user_bash" "handler_id" 1 "args" (pig:obj "command" "ls"))
      (let ((handle (pig:jref (pig:jget (pig:jget (hread-type host "response") "response") "result") "operations" "handle")))
        (check (stringp handle))
        (request host "x" "user_bash_exec" "tool" handle "args" (pig:obj "command" "ls" "cwd" "/w"))
        (let ((update (hread-type host "notify")))
          (check-equal "tool_update" (pig:jget (pig:jget update "notify") "method"))
          (check-equal "bHNAL3c=" (pig:jref (pig:jget update "notify") "args" "result" "data")))
        (check-equal 3 (pig:jget (pig:jget (pig:jget (hread-type host "response") "response") "result") "exitCode"))))))

(deftest before-agent-start-system-prompt-reads-live
  (let ((ext (pig:make-extension "bas")) (seen '()))
    (pig:on ext "before_agent_start"
            (lambda (ctx data) (declare (ignore ctx))
              (push (pig:jget data "systemPrompt") seen)
              (setf (gethash "selectedTools" (pig:jget data "systemPromptOptions")) (vector "z"))
              (push (pig:jget data "systemPrompt") seen)
              nil))
    (with-host (host ext)
      (request host "b" "event" "event" "before_agent_start" "handler_id" 1
                    "args" (pig:obj "systemPrompt" "base" "systemPromptOptions" (pig:obj "sections" (vector))))
      (let ((call (hread-type host "call")))
        (check-equal "renderSystemPrompt" (pig:jget (pig:jget call "call") "method"))
        (answer-call host call (pig:obj "prompt" "edited")))
      (hread-type host "response")
      (check-equal '("edited" "base") seen))))
