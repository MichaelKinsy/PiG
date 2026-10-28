;;;; SPDX-License-Identifier: MIT
;;;; Lane U1: the component kit, ui.custom, overlays, widgets, footer, header, theme. The fake host is sdk-tests.lisp's.
(in-package #:pig-tests)

(defun kt-canon (value)
  "VALUE with every object as a key-sorted alist, so two JSON values compare with EQUAL whatever their member order."
  (cond ((hash-table-p value)
         (sort (loop for k being the hash-keys of value using (hash-value v) collect (cons k (kt-canon v))) #'string< :key #'car))
        ((and (vectorp value) (not (stringp value))) (map 'list #'kt-canon value))
        ((and (floatp value) (= value (ffloor value)) (< (abs value) 1d15)) (round value))
        (t value)))

(defun kt-same-json (expected actual)
  "EXPECTED is JSON text."
  (equal (kt-canon (pig:from-json expected)) (kt-canon actual)))

(defmacro kt-check-json (expected actual &optional message)
  `(progn (incf *checks*)
          (let ((a ,actual))
            (unless (kt-same-json ,expected a)
              (report-failure ',actual (format nil "expected ~A, got ~A~@[ (~A)~]" ,expected (pig:to-json a) ,message))))))

(defun kt-encoded (view &optional frontend) (pig::kit-encode-view view frontend))
(defun kt-body (view &optional frontend) (pig::kit-encoded-body (kt-encoded view frontend)))
(defun kt-node-json (node &optional frontend)
  (pig:jget (pig::kit-encoded-body (pig::kit-encode-view (pig:kit-view node) frontend)) "root"))

(defun kt-octets (string) (sb-ext:string-to-octets string :external-format :utf-8))

;;; ---------------------------------------------------------------- SHA-256 and base64

(deftest kit-sha256-matches-the-known-vectors
  (check-equal "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" (pig::kit-sha256-hex (kt-octets "")))
  (check-equal "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" (pig::kit-sha256-hex (kt-octets "abc")))
  (check-equal "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"
               (pig::kit-sha256-hex (kt-octets "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")))
  (check-equal "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0"
               (pig::kit-sha256-hex (make-array 1000000 :element-type '(unsigned-byte 8) :initial-element 97))))

(deftest kit-base64-pads
  (check-equal "TWFu" (pig::kit-base64 (kt-octets "Man")))
  (check-equal "TWE=" (pig::kit-base64 (kt-octets "Ma")))
  (check-equal "TQ==" (pig::kit-base64 (kt-octets "M")))
  (check-equal "" (pig::kit-base64 (kt-octets "")))
  (check-equal "Y292ZXI=" (pig::kit-base64 (kt-octets "cover"))))

;;; ---------------------------------------------------------------- encoding

(deftest kit-every-kind-encodes-its-upstream-fields
  (let* ((tree (pig:kit-container
                (list (pig:kit-dynamic-border "accent")
                      (pig:kit-box 2 0 :bg "selectedBg" :children (list (pig:kit-text "in box" 1 1)))
                      (pig:kit-text "Pick" 2 0 :bg "customMessageBg")
                      (pig:kit-truncated-text "cut" 0 0)
                      (pig:kit-markdown "- **a**" 1 0 :default-text-style (pig:kit-text-style :color "text" :bg-color "customMessageBg" :italic t)
                                                      :render-latex :false)
                      (pig:kit-spacer 2)
                      (pig:kit-select-list "tracks" (list (pig:kit-select-item "t1" "One" "first") (pig:kit-select-item "t2" "Two")) 3
                                           :layout (pig:kit-select-layout :min-primary-column-width 4) :selected-index 1 :filter "o")
                      (pig:kit-settings-list
                       "prefs"
                       (list (pig:kit-setting-item "mode" "Mode" "fast" :values '("fast" "slow"))
                             (pig:kit-setting-item "theme" "Theme" "dark" :description "colors"
                                                   :submenu (pig:kit-select-list "themes" (list (pig:kit-select-item "dark" "Dark")) 5)))
                       4 :enable-search t :selected-index 1 :filter "th")
                      (pig:kit-loader "Working" :spinner-color "accent" :message-color "muted"
                                                :indicator (pig:kit-loader-indicator :frames '("a" "b") :interval-ms 120) :frame 1)
                      (pig:kit-hstack (list (pig:kit-truncated-text "left" 0 0)
                                            (pig:kit-stack-entry (pig:kit-truncated-text "right" 0 0) :grow 1 :basis 3)
                                            (pig:kit-stack-entry (pig:kit-truncated-text "plain" 0 0)))
                                      :gap 1 :align "center")
                      (pig:kit-vstack (list (pig:kit-spacer 1)))
                      (pig:kit-lines '("raw")))))
         (view (pig:kit-view tree :focus "tracks" :theme (pig:obj "accent" "#d75f00"))))
    (kt-check-json "{\"root\":{\"kind\":\"container\",\"children\":[
      {\"kind\":\"dynamic-border\",\"color\":\"accent\"},
      {\"kind\":\"box\",\"children\":[{\"kind\":\"text\",\"text\":\"in box\",\"paddingX\":1,\"paddingY\":1}],\"paddingX\":2,\"paddingY\":0,\"bg\":\"selectedBg\"},
      {\"kind\":\"text\",\"text\":\"Pick\",\"paddingX\":2,\"paddingY\":0,\"bg\":\"customMessageBg\"},
      {\"kind\":\"truncated-text\",\"text\":\"cut\",\"paddingX\":0,\"paddingY\":0},
      {\"kind\":\"markdown\",\"text\":\"- **a**\",\"paddingX\":1,\"paddingY\":0,\"defaultTextStyle\":{\"color\":\"text\",\"bgColor\":\"customMessageBg\",\"italic\":true},\"renderLatex\":false},
      {\"kind\":\"spacer\",\"lines\":2},
      {\"kind\":\"select-list\",\"id\":\"tracks\",\"items\":[{\"value\":\"t1\",\"label\":\"One\",\"description\":\"first\"},{\"value\":\"t2\",\"label\":\"Two\"}],
       \"maxVisible\":3,\"layout\":{\"minPrimaryColumnWidth\":4},\"selectedIndex\":1,\"filter\":\"o\"},
      {\"kind\":\"settings-list\",\"id\":\"prefs\",\"items\":[
        {\"id\":\"mode\",\"label\":\"Mode\",\"currentValue\":\"fast\",\"values\":[\"fast\",\"slow\"]},
        {\"id\":\"theme\",\"label\":\"Theme\",\"description\":\"colors\",\"currentValue\":\"dark\",
         \"submenu\":{\"kind\":\"select-list\",\"id\":\"themes\",\"items\":[{\"value\":\"dark\",\"label\":\"Dark\"}],\"maxVisible\":5}}],
       \"maxVisible\":4,\"enableSearch\":true,\"selectedIndex\":1,\"filter\":\"th\"},
      {\"kind\":\"loader\",\"message\":\"Working\",\"spinnerColor\":\"accent\",\"messageColor\":\"muted\",\"indicator\":{\"frames\":[\"a\",\"b\"],\"intervalMs\":120},\"frame\":1},
      {\"kind\":\"hstack\",\"children\":[
        {\"kind\":\"truncated-text\",\"text\":\"left\",\"paddingX\":0,\"paddingY\":0},
        {\"kind\":\"truncated-text\",\"text\":\"right\",\"paddingX\":0,\"paddingY\":0,\"stack\":{\"basis\":3,\"grow\":1}},
        {\"kind\":\"truncated-text\",\"text\":\"plain\",\"paddingX\":0,\"paddingY\":0}],\"gap\":1,\"align\":\"center\"},
      {\"kind\":\"vstack\",\"children\":[{\"kind\":\"spacer\",\"lines\":1}]},
      {\"kind\":\"lines\",\"content\":[\"raw\"]}]},
      \"focus\":\"tracks\",\"theme\":{\"accent\":\"#d75f00\"}}"
                (pig::kit-encoded-body (kt-encoded view)))
    (check (null (pig::kit-encoded-images (kt-encoded view))))))

(deftest kit-defaults-match-upstream-constructors
  (kt-check-json "{\"kind\":\"container\",\"children\":[{\"kind\":\"box\",\"paddingX\":1,\"paddingY\":1},{\"kind\":\"spacer\",\"lines\":1},
      {\"kind\":\"dynamic-border\",\"color\":\"border\"},{\"kind\":\"loader\",\"message\":\"Loading...\"},{\"kind\":\"text\",\"text\":\"\",\"paddingX\":1,\"paddingY\":1}]}"
              (kt-node-json (pig:kit-container (list (pig:kit-box) (pig:kit-spacer) (pig:kit-dynamic-border) (pig:kit-loader) (pig:kit-text))))))

(deftest kit-editor-input-scroll-view-and-mouse-region-encode-their-fields
  (kt-check-json "{\"kind\":\"editor\",\"id\":\"e\",\"value\":\"one\\ntwo\",\"paddingX\":2,\"autocompleteMaxVisible\":3,\"borderColor\":\"accent\",\"disableSubmit\":true,
      \"history\":[\"a\",\"b\"],\"autocomplete\":{\"commands\":[{\"name\":\"help\",\"description\":\"Show help\"},{\"name\":\"model\",\"argumentHint\":\"<name>\"}],\"basePath\":\"/work\"}}"
              (kt-node-json (pig:kit-editor "e" (format nil "one~%two") :padding-x 2 :autocomplete-max-visible 3 :border-color "accent" :disable-submit t
                                         :history '("a" "b")
                                         :autocomplete (pig:kit-autocomplete (list (pig:kit-slash-command "help" "Show help")
                                                                                   (pig:kit-slash-command "model" "" "<name>"))
                                                                             "/work"))))
  (kt-check-json "{\"kind\":\"editor\",\"id\":\"e\"}" (kt-node-json (pig:kit-editor "e")))
  (kt-check-json "{\"kind\":\"input\",\"id\":\"i\",\"value\":\"v\",\"prompt\":\"\",\"placeholder\":\"p\",\"placeholderColor\":\"dim\"}"
              (kt-node-json (pig:kit-input "i" "v" :prompt "" :placeholder "p" :placeholder-color "dim")))
  (kt-check-json "{\"kind\":\"input\",\"id\":\"i\"}" (kt-node-json (pig:kit-input "i")))
  (kt-check-json "{\"kind\":\"scroll-view\",\"id\":\"s\",\"children\":[{\"kind\":\"text\",\"text\":\"x\",\"paddingX\":1,\"paddingY\":0}],\"follow\":\"end\",\"primary\":true,
      \"overscroll\":\"contain\",\"scrollbar\":\"always\",\"scrollbarTrackColor\":\"dim\",\"scrollbarThumbColor\":\"accent\",\"scrollbarHideDelayMs\":0,\"scrollTop\":4}"
              (kt-node-json (pig:kit-scroll-view "s" (pig:kit-text "x" 1 0) :follow "end" :primary t :overscroll "contain" :scrollbar "always"
                                              :scrollbar-track-color "dim" :scrollbar-thumb-color "accent" :scrollbar-hide-delay-ms 0 :scroll-top 4)))
  (kt-check-json "{\"kind\":\"mouse-region\",\"id\":\"m\",\"children\":[{\"kind\":\"spacer\",\"lines\":1}],
      \"region\":{\"types\":[\"click\"],\"handled\":false,\"capture\":true,\"focus\":true,\"render\":false}}"
              (kt-node-json (pig:kit-mouse-region "m" (pig:kit-spacer 1) :types '("click") :handled :false :capture t :focus t :render :false)))
  (kt-check-json "{}" (pig:jget (kt-node-json (pig:kit-mouse-region "m" (pig:kit-spacer 1))) "region"))
  (kt-check-json "{\"kind\":\"cancellable-loader\",\"id\":\"c\",\"message\":\"Working\",\"spinnerColor\":\"accent\",\"frame\":3}"
              (kt-node-json (pig:kit-cancellable-loader "c" "Working" :spinner-color "accent" :frame 3)))
  (kt-check-json "{\"kind\":\"bordered-loader\",\"id\":\"b\",\"message\":\"Loading...\",\"cancellable\":false}"
              (kt-node-json (pig:kit-bordered-loader "b" "Loading..." :cancellable nil))))

(deftest kit-conversation-kinds-encode-as-the-other-sdks
  (let* ((image (kt-octets "png bytes"))
         (assistant (pig:kit-assistant-message nil :id "a1"))
         (hidden (pig:kit-assistant-message (pig:kit-message (list (pig:kit-text-block "x")) :stop-reason "error" :error-message "boom")))
         (tool (pig:kit-tool-execution "read" "call-1" (pig:obj "path" "a.txt") "/work" :output-pad 5 :id "call-1"))
         (empty (pig:kit-tool-execution "kit_tool" "" nil "" :tool-definition pig:*kit-tool-definition-empty*))
         (bash (pig:kit-bash-execution "ls" t :output-pad 2 :id "b"))
         (diff (pig:kit-diff (format nil " 1 a~%-2 b~%+2 c") :file-path "x.go"))
         (view (pig:kit-view nil)))
    (pig:kit-assistant-message-update-content
     assistant (pig:kit-message (list (pig:kit-thinking-block "plan") (pig:kit-text-block "done") (pig:kit-tool-call-block)) :stop-reason "toolUse") t)
    (pig:kit-assistant-message-set-hide-thinking-block hidden t)
    (pig:kit-assistant-message-set-hidden-thinking-label hidden "Pondering...")
    (pig:kit-assistant-message-set-output-pad hidden 0)
    (pig:kit-tool-execution-set-args-complete tool)
    (pig:kit-tool-execution-mark-execution-started tool)
    (pig:kit-tool-execution-update-result
     tool (pig:kit-tool-result (list (pig:kit-text-content "hi") (pig:kit-image-content image "image/png")) :is-error t :details (pig:obj "n" 1) :duration-ms 1234) nil)
    (pig:kit-tool-execution-set-expanded tool t)
    (pig:kit-tool-execution-set-output-pad tool 3)
    (pig:kit-tool-execution-set-show-images empty nil)
    (pig:kit-tool-execution-set-image-width-cells empty 30)
    (pig:kit-bash-execution-append-output bash (format nil "a~%"))
    (pig:kit-bash-execution-append-output bash "b")
    (pig:kit-bash-execution-set-complete bash 2 nil t "/tmp/out")
    (pig:kit-bash-execution-set-expanded bash t)
    (setf view (pig:kit-view (pig:kit-container (list (pig:kit-user-message "hello") assistant hidden tool empty bash (pig:kit-bash-execution "sleep") diff))))
    (let* ((enc (pig::kit-encode-view view nil)) (ref (pig::kit-sha256-hex image)))
      (kt-check-json (format nil "{\"root\":{\"kind\":\"container\",\"children\":[
      {\"kind\":\"user-message\",\"text\":\"hello\",\"outputPad\":1},
      {\"kind\":\"assistant-message\",\"id\":\"a1\",\"message\":{\"content\":[{\"type\":\"thinking\",\"thinking\":\"plan\"},{\"type\":\"text\",\"text\":\"done\"},{\"type\":\"toolCall\"}],\"stopReason\":\"toolUse\"},\"outputPad\":1,\"isStreaming\":true},
      {\"kind\":\"assistant-message\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"x\"}],\"stopReason\":\"error\",\"errorMessage\":\"boom\"},\"outputPad\":0,\"hideThinkingBlock\":true,\"hiddenThinkingLabel\":\"Pondering...\"},
      {\"kind\":\"tool-execution\",\"id\":\"call-1\",\"outputPad\":3,\"toolName\":\"read\",\"toolCallId\":\"call-1\",\"args\":{\"path\":\"a.txt\"},\"cwd\":\"/work\",\"executionStarted\":true,\"argsComplete\":true,\"expanded\":true,
       \"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"},{\"type\":\"image\",\"ref\":\"~A\",\"mimeType\":\"image/png\"}],\"isError\":true,\"details\":{\"n\":1},\"durationMs\":1234},\"isPartial\":false},
      {\"kind\":\"tool-execution\",\"toolName\":\"kit_tool\",\"args\":{},\"toolDefinition\":\"empty\",\"showImages\":false,\"imageWidthCells\":30},
      {\"kind\":\"bash-execution\",\"id\":\"b\",\"outputPad\":2,\"expanded\":true,\"command\":\"ls\",\"excludeFromContext\":true,\"output\":\"a\\nb\",\"complete\":{\"exitCode\":2,\"truncated\":true,\"fullOutputPath\":\"/tmp/out\"}},
      {\"kind\":\"bash-execution\",\"command\":\"sleep\"},
      {\"kind\":\"diff\",\"paddingX\":0,\"paddingY\":0,\"diff\":\" 1 a\\n-2 b\\n+2 c\",\"filePath\":\"x.go\"}]}}" ref)
                  (pig::kit-encoded-body enc))
      (check-equal (list ref) (pig::kit-encoded-refs enc) "the result image goes out once"))))

(deftest kit-image-ref-is-the-sha256-of-its-bytes
  (let ((image (pig:kit-image (kt-octets "png-bytes") "image/png" :max-width-cells 20 :max-height-cells 10 :filename "cover.png" :fallback-color "dim"))
        (ref (pig::kit-sha256-hex (kt-octets "png-bytes"))))
    (check-equal ref (pig:kit-image-ref image))
    (kt-check-json (format nil "{\"kind\":\"image\",\"ref\":\"~A\",\"mimeType\":\"image/png\",\"maxWidthCells\":20,\"maxHeightCells\":10,\"filename\":\"cover.png\",\"fallbackColor\":\"dim\"}" ref)
                (kt-node-json image))
    (check-equal (list ref) (pig::kit-encoded-refs (kt-encoded (pig:kit-view image))))
    (check-error type-error (pig:kit-image "not octets" "image/png"))))

(deftest kit-lines-annotations-travel-only-with-a-frontend
  (let ((view (pig:kit-view (pig:kit-lines-progress (pig:kit-lines-image (pig:kit-lines '("AB")) (kt-octets "cover") "image/jpeg") 30 120))))
    (kt-check-json "{\"kind\":\"lines\",\"content\":[\"AB\"]}" (pig:jget (kt-body view) "root"))
    (check (null (pig::kit-encoded-images (kt-encoded view))) "no image bytes without a frontend")
    (kt-check-json (format nil "{\"kind\":\"lines\",\"content\":[\"AB\"],\"image\":{\"ref\":\"~A\"},\"progress\":{\"value\":30.0,\"max\":120.0}}" (pig::kit-sha256-hex (kt-octets "cover")))
                (pig:jget (kt-body view t) "root")))
  (let ((tracks (pig:kit-list (list (pig:kit-list-item "Blue in Green" "Miles Davis" '("5:37")) (pig:kit-list-item "So What")) 1)))
    (kt-check-json "{\"kind\":\"lines\",\"content\":[\"a\",\"b\"]}" (kt-node-json (pig:kit-lines '("a" "b") :list tracks)))
    (kt-check-json "{\"items\":[{\"label\":\"Blue in Green\",\"detail\":\"Miles Davis\",\"columns\":[\"5:37\"]},{\"label\":\"So What\"}],\"selectedIndex\":1}"
                (pig:jget (kt-node-json (pig:kit-lines '("a" "b") :list tracks) t) "list")))
  (check-equal -1 (pig:jget (pig:jget (kt-node-json (pig:kit-lines '("a") :list (pig:kit-list (list (pig:kit-list-item "")))) t) "list") "selectedIndex")))

(deftest kit-image-bytes-go-once-per-connection-until-evicted
  (let* ((ext (pig:make-extension "kit-ledger"))
         (view (pig:kit-view (pig:kit-container (list (pig:kit-image (kt-octets "abc") "image/png") (pig:kit-image (kt-octets "abc") "image/png")))))
         (ref (pig::kit-sha256-hex (kt-octets "abc"))))
    (flet ((wire () (pig::kit-wire ext (pig::kit-encode ext view))))
      (kt-check-json (format nil "[{\"ref\":\"~A\",\"mimeType\":\"image/png\",\"data\":\"YWJj\"}]" ref) (pig:jget (wire) "images"))
      (check (not (pig::kit-has-unsent ext (pig::kit-encode ext view))))
      (check (null (pig:jget (wire) "images")) "the second frame names the ref only")
      (let ((kit (pig::kit-state ext))) (remhash ref (pig::kit-state-sent kit)))
      (check (pig::kit-has-unsent ext (pig::kit-encode ext view)))
      (check-equal "YWJj" (pig:jget (aref (pig:jget (wire) "images") 0) "data") "evicted bytes are sent again"))))

(deftest kit-a-view-deeper-than-the-host-bound-is-an-error
  (let ((node (pig:kit-spacer 1)))
    (dotimes (i 63) (setf node (pig:kit-container (list node))))
    (kt-encoded (pig:kit-view node))
    (check-error error (kt-encoded (pig:kit-view (pig:kit-container (list node)))))
    (let ((loop (pig:kit-container)))
      (pig:kit-container-add-child loop loop)
      (check-error error (kt-encoded (pig:kit-view loop))))
    (check (search "kit: view deeper than 64 nodes"
                   (handler-case (progn (kt-encoded (pig:kit-view (pig:kit-container (list node)))) "")
                     (error (e) (princ-to-string e)))))
    (check-error error (kt-encoded (pig:kit-view "not a node")))))

(deftest kit-ledger-follows-the-frontend-flag-of-each-snapshot
  (let ((ext (pig:make-extension "kit-frontend")) (view (pig:kit-view (pig:kit-lines-progress (pig:kit-lines '()) 1 2))))
    (flet ((progress () (pig:jget (pig:jget (pig::kit-wire ext (pig::kit-encode ext view)) "root") "progress")))
      (check (null (progress)))
      (pig::kit-apply-state ext (pig:obj "frontend" t))
      (kt-check-json "{\"value\":1.0,\"max\":2.0}" (progress))
      (pig::kit-apply-state ext (pig:obj "hasUI" t))
      (check (null (progress)) "state_update snapshots omit a false flag"))))

(deftest kit-events-decode-each-upstream-callback
  (multiple-value-bind (key event)
      (pig::kit-event-from-wire (pig:from-json "{\"key\":\"custom-1\",\"node\":\"tracks\",\"type\":\"select\",\"index\":2,\"item\":{\"value\":\"k2\",\"label\":\"Two\",\"description\":\"d\"}}"))
    (check-equal "custom-1" key)
    (check-equal "tracks" (pig:kit-event-node event))
    (check-equal "select" (pig:kit-event-type event))
    (check-equal 2 (pig:kit-event-index event))
    (kt-check-json "{\"value\":\"k2\",\"label\":\"Two\",\"description\":\"d\"}" (pig:kit-event-item event)))
  (let ((event (nth-value 1 (pig::kit-event-from-wire (pig:from-json "{\"key\":\"k\",\"node\":\"prefs\",\"type\":\"change\",\"index\":0,\"id\":\"mode\",\"value\":\"slow\",\"cursor\":{\"line\":1,\"col\":2}}")))))
    (check-equal "change" (pig:kit-event-type event))
    (check-equal "mode" (pig:kit-event-id event))
    (check-equal "slow" (pig:kit-event-value event))
    (check (null (pig:kit-event-item event)))
    (check-equal 2 (pig:jget (pig:kit-event-cursor event) "col")))
  (dolist (type '("cancel" "selectionChange" "submit" "escape" "abort" "mouse"))
    (check-equal type (pig:kit-event-type (nth-value 1 (pig::kit-event-from-wire (pig:obj "key" "k" "node" "n" "type" type))))))
  (check (null (nth-value 1 (pig::kit-event-from-wire (pig:obj "key" "k" "node" "n" "type" "hover"))))))

(deftest kit-render-results-carry-a-view
  (let ((ext (pig:make-extension "kit-result")))
    (kt-check-json "{\"view\":{\"root\":{\"kind\":\"spacer\",\"lines\":1}}}" (pig:kit-render-result ext (pig:kit-view (pig:kit-spacer 1))))))

;;; ---------------------------------------------------------------- overlay width

(deftest overlay-render-width-follows-upstream-layout
  (let ((cases `(("default" "{\"overlay\":true}" 80)
                 ("empty layout" "{\"overlay\":true,\"overlayOptions\":{}}" 80)
                 ("percent width" "{\"overlay\":true,\"overlayOptions\":{\"width\":\"75%\",\"maxHeight\":\"95%\",\"margin\":{\"top\":1}}}" 90)
                 ("minWidth clamped after margins" "{\"overlay\":true,\"overlayOptions\":{\"width\":20,\"minWidth\":200,\"margin\":2}}" 116)
                 ("percentage before margin" "{\"overlay\":true,\"overlayOptions\":{\"width\":\"50%\",\"margin\":{\"left\":10,\"right\":10}}}" 60)
                 ("zero width" "{\"overlay\":true,\"overlayOptions\":{\"width\":0}}" 1)
                 ("invalid width" "{\"overlay\":true,\"overlayOptions\":{\"width\":\"oops\"}}" 80)
                 ("legacy titled modal" "{\"overlay\":true,\"title\":\"Picker\"}" 120)
                 ("titled overlay with layout" "{\"overlay\":true,\"title\":\"Picker\",\"overlayOptions\":{\"width\":30}}" 30)
                 ("fractional percent" "{\"overlay\":true,\"overlayOptions\":{\"width\":\"33.5%\"}}" 40)
                 ("inline" "{}" 120))))
    (dolist (case cases)
      (check-equal (third case) (funcall (pig::overlay-render-width (pig:from-json (second case))) 120) (first case)))))

;;; ---------------------------------------------------------------- theme

(defun kt-dark-palette ()
  (pig:from-json
   (format nil "{\"name\":\"dark\",\"sourcePath\":\"/t.json\",\"appearance\":\"light\",\"mode\":\"256color\",
     \"foregrounds\":{\"success\":\"\\u001b[38;2;1;2;3m\",\"dimmed\":\"\\u001b[38;2;9;9;9m\\u001b[2m\"},\"backgrounds\":{\"toolSuccessBg\":\"\\u001b[48;2;4;5;6m\"},
     \"colors\":{\"success\":{\"kind\":\"rgb\",\"r\":1,\"g\":2,\"b\":3},\"accent\":{\"kind\":\"oklch\",\"l\":0.62,\"c\":0.1,\"h\":200},
       \"bad\":{\"kind\":\"nope\"},\"worse\":\"x\"},\"modifiers\":true}")))

(defun kt-esc (text) (format nil "~C[~A" (code-char 27) text))

(deftest theme-reads-the-palette
  (let ((theme (pig:make-theme-from-palette (kt-dark-palette))))
    (check (typep theme 'pig:theme))
    (check-equal "dark" (pig:theme-name theme))
    (check-equal "/t.json" (pig:theme-source-path theme))
    (check (null (pig:theme-source-info theme)))
    (check-equal "light" (pig:theme-appearance theme))
    (check-equal "256color" (pig:theme-get-color-mode theme))
    (kt-check-json "{\"success\":{\"kind\":\"rgb\",\"r\":1,\"g\":2,\"b\":3},\"accent\":{\"kind\":\"oklch\",\"l\":0.62,\"c\":0.1,\"h\":200}}" (pig:theme-colors theme))
    (setf (gethash "r" (gethash "success" (pig:theme-colors theme))) 99)
    (check-equal 1 (pig:jget (pig:jget (pig:theme-colors theme) "success") "r") "the colors are a copy")
    (check-equal (concatenate 'string (kt-esc "38;2;1;2;3m") "x" (kt-esc "39m")) (pig:theme-fg theme "success" "x"))
    (check-equal (concatenate 'string (kt-esc "38;2;9;9;9m") (kt-esc "2m") "x" (kt-esc "22;39m")) (pig:theme-fg theme "dimmed" "x") "a faint token closes with 22;39")
    (check-equal (concatenate 'string (kt-esc "48;2;4;5;6m") "x" (kt-esc "49m")) (pig:theme-bg theme "toolSuccessBg" "x"))
    (check-equal (kt-esc "38;2;1;2;3m") (pig:theme-get-fg-ansi theme "success"))
    (check-equal (kt-esc "48;2;4;5;6m") (pig:theme-get-bg-ansi theme "toolSuccessBg"))
    (check-error error (pig:theme-fg theme "nope" "x"))
    (check-equal "Unknown theme color: nope" (handler-case (pig:theme-bg theme "nope" "x") (error (e) (princ-to-string e))))
    (check-error error (pig:theme-get-fg-ansi theme "nope"))
    (check-equal (concatenate 'string (kt-esc "1m") "x" (kt-esc "22m")) (pig:theme-bold theme "x"))
    (check-equal "" (pig:theme-italic theme ""))
    (check-equal (concatenate 'string (kt-esc "3m") "a" (kt-esc "23m") (format nil "~%") (kt-esc "3m") "b" (kt-esc "23m")) (pig:theme-italic theme (format nil "a~%b")) "line breaks are closed and re-opened")
    (check-equal (concatenate 'string (kt-esc "4m") "a" (kt-esc "24m") (kt-esc "4m") "b" (kt-esc "24m")) (pig:theme-underline theme (concatenate 'string "a" (kt-esc "24m") "b")) "an inner close re-opens")
    (check-equal (concatenate 'string (kt-esc "7m") "x" (kt-esc "27m")) (pig:theme-inverse theme "x"))
    (check-equal (concatenate 'string (kt-esc "9m") "x" (kt-esc "29m")) (pig:theme-strikethrough theme "x"))
    (check-error error (funcall (pig:theme-get-thinking-border-color theme "nope") "t"))
    (check-equal (concatenate 'string (kt-esc "1m") "x" (kt-esc "22m")) (pig:theme-bold (pig:make-theme-from-palette (pig:obj "modifiers" t)) "x"))))

(deftest theme-closes-a-faint-token-and-draws-the-border-colors-through-fg
  (let ((theme (pig:make-theme-from-palette
                (pig:from-json "{\"name\":\"faint\",\"foregrounds\":{\"accent\":\"\\u001b[38;5;5m\",\"muted\":\"\\u001b[39m\\u001b[2m\",
                  \"thinkingXhigh\":\"\\u001b[38;5;13m\\u001b[2m\",\"thinkingOff\":\"\\u001b[38;5;1m\",\"bashMode\":\"\\u001b[38;5;2m\"},\"backgrounds\":{},\"modifiers\":true,\"mode\":\"256color\"}"))))
    (check-equal (concatenate 'string (kt-esc "39m") (kt-esc "2m") "x" (kt-esc "22;39m")) (pig:theme-fg theme "muted" "x"))
    (check-equal (concatenate 'string (kt-esc "38;5;5m") "x" (kt-esc "39m")) (pig:theme-fg theme "accent" "x"))
    (check-equal (concatenate 'string (kt-esc "38;5;13m") (kt-esc "2m") "x" (kt-esc "22;39m")) (funcall (pig:theme-get-thinking-border-color theme "xhigh") "x"))
    (check-equal (concatenate 'string (kt-esc "38;5;1m") "x" (kt-esc "39m")) (funcall (pig:theme-get-thinking-border-color theme "unknown level") "x"))
    (check-equal (concatenate 'string (kt-esc "38;5;2m") "x" (kt-esc "39m")) (funcall (pig:theme-get-bash-mode-border-color theme) "x"))))

(defun kt-style-theme (mode)
  (pig:make-theme-from-palette
   (pig:from-json (format nil "{\"name\":\"dark\",\"appearance\":\"light\",\"mode\":\"~A\",\"modifiers\":true,
     \"foregrounds\":{\"success\":\"\\u001b[38;2;1;2;3m\",\"dimmed\":\"\\u001b[38;2;9;9;9m\\u001b[2m\",\"accent\":\"\\u001b[38;5;4m\"},
     \"backgrounds\":{\"toolSuccessBg\":\"\\u001b[48;2;4;5;6m\",\"userMessageBg\":\"\\u001b[49m\"}}" mode))))

(deftest theme-style-renders-tokens-attributes-and-colors
  (let ((theme (kt-style-theme "truecolor")))
    (flet ((style (options) (pig:theme-style theme "x" (pig:from-json options))))
      (check-equal (concatenate 'string (kt-esc "38;2;1;2;3m") (kt-esc "48;2;4;5;6m") (kt-esc "1m") "x" (kt-esc "22m") (kt-esc "49m") (kt-esc "39m"))
                   (style "{\"fg\":\"success\",\"bg\":\"toolSuccessBg\",\"bold\":true}") "tokens and bold")
      (check-equal "x" (style "{}") "no style")
      (check-equal (concatenate 'string (kt-esc "1m") (kt-esc "2m") (kt-esc "3m") (kt-esc "4m") (kt-esc "7m") (kt-esc "9m") "x" (kt-esc "29m") (kt-esc "27m") (kt-esc "24m") (kt-esc "23m") (kt-esc "22m"))
                   (style "{\"bold\":true,\"dim\":true,\"italic\":true,\"underline\":true,\"inverse\":true,\"strikethrough\":true}") "every attribute")
      (check-equal (concatenate 'string (kt-esc "38;2;9;9;9m") (kt-esc "2m") "x" (kt-esc "22m") (kt-esc "39m")) (style "{\"fg\":\"dimmed\"}") "a faint token adds dim")
      (check-equal (concatenate 'string (kt-esc "38;2;10;20;30m") "x" (kt-esc "39m")) (style "{\"fg\":{\"kind\":\"rgb\",\"r\":10,\"g\":20,\"b\":30}}") "rgb in truecolor")
      (check-equal (concatenate 'string (kt-esc "38;5;5m") "x" (kt-esc "39m")) (style "{\"fg\":{\"kind\":\"indexed\",\"index\":5}}") "indexed")
      (check-equal "Unknown theme color: notAToken" (handler-case (style "{\"fg\":\"notAToken\"}") (error (e) (princ-to-string e))))
      (check-error error (style "{\"fg\":\"toolSuccessBg\"}"))
      (check-error error (style "{\"bg\":\"success\"}"))
      (check-error error (style "{\"fg\":{\"kind\":\"nope\"}}"))))
  (let ((theme (kt-style-theme "256color")))
    (check-equal (concatenate 'string (kt-esc "38;5;16m") "x" (kt-esc "39m")) (pig:theme-style theme "x" (pig:from-json "{\"fg\":{\"kind\":\"rgb\",\"r\":10,\"g\":20,\"b\":30}}")))
    (check-equal (concatenate 'string (kt-esc "38;5;203m") "x" (kt-esc "39m")) (pig:theme-style theme "x" (pig:from-json "{\"fg\":{\"kind\":\"rgb\",\"r\":250,\"g\":100,\"b\":50}}")))))

(deftest theme-without-a-palette-styles-nothing-and-without-modifiers-draws-none
  (let ((empty (pig:make-theme-from-palette nil)))
    (check-equal "x" (pig:theme-fg empty "anything" "x") "no palette, no tokens to lack")
    (check-equal (concatenate 'string (kt-esc "1m") "x" (kt-esc "22m")) (pig:theme-bold empty "x"))
    (check (null (pig:theme-appearance empty)))
    (check (null (pig:theme-name empty)))
    (check-equal "truecolor" (pig:theme-get-color-mode empty)))
  (let ((plain (pig:make-theme-from-palette (pig:obj "modifiers" :false))))
    (check-equal "x" (pig:theme-bold plain "x"))
    (check-equal "" (pig:theme-bold plain nil))))

;;; ---------------------------------------------------------------- the wire, against the fake host

(defstruct kth host (events '()) (lock (sb-thread:make-mutex)) (queue (sb-thread:make-waitqueue)) pump (pending '()))

(defun kth-start (ext &key state)
  "Serve EXT against a fake host that answers every call except ui.custom and ui.custom.control (kept pending: a test answers them itself) and records every frame."
  (multiple-value-bind (host register) (start-host ext)
    (declare (ignore register))
    (let ((kth (make-kth :host host)))
      (when state (hsend host "type" "notify" "notify" (pig:obj "method" "state_update" "args" (pig:obj "state" state))))
      (setf (kth-pump kth)
            (sb-thread:make-thread
             (lambda ()
               (loop (let ((frame (handler-case (hread host) (error () :eof))))
                       (when (eq frame :eof) (return))
                       (let ((type (pig:jget frame "type")))
                         (cond ((equal type "call")
                                (let* ((call (pig:jget frame "call")) (method (pig:jget call "method")))
                                  (kth-record kth (list :call method (pig:jget call "args") frame))
                                  (unless (member method '("ui.custom" "ui.custom.control") :test #'equal)
                                    (hsend host "type" "call_result" "id" (pig:jget frame "id") "call_result" (pig:obj)))))
                               ((equal type "notify")
                                (kth-record kth (list :notify (pig:jget (pig:jget frame "notify") "method") (pig:jget (pig:jget frame "notify") "args") frame)))
                               ((equal type "widget_push")
                                (kth-record kth (list :widget-push "widget_push" (pig:jget frame "widget_push") frame)))
                               ((equal type "response") (kth-record kth (list :response "response" (pig:jget frame "response") frame))))))))
             :name "ui-host-pump"))
      kth)))

(defun kth-record (kth event)
  (sb-thread:with-mutex ((kth-lock kth))
    (setf (kth-events kth) (append (kth-events kth) (list event)))
    (sb-thread:condition-broadcast (kth-queue kth))))

(defun kth-take (kth predicate &optional (seconds 8))
  "The first recorded event satisfying PREDICATE, removing only it (polls: a timed condition-wait may return without the lock)."
  (let ((deadline (+ (get-internal-real-time) (* seconds internal-time-units-per-second))))
    (loop
      (let ((event (sb-thread:with-mutex ((kth-lock kth))
                     (let ((hit (position-if predicate (kth-events kth))))
                       (when hit
                         (prog1 (nth hit (kth-events kth))
                           (setf (kth-events kth) (append (subseq (kth-events kth) 0 hit) (nthcdr (1+ hit) (kth-events kth))))))))))
        (when event (return event)))
      (when (> (get-internal-real-time) deadline)
        (error "timed out waiting for an event; recorded ~S" (mapcar (lambda (e) (list (first e) (second e))) (kth-events kth))))
      (sleep 0.01))))

(defun kth-next (kth kind method &optional (seconds 8))
  "The next recorded event of KIND and METHOD, removing everything before it."
  (kth-take kth (lambda (e) (and (eq (first e) kind) (equal (second e) method))) seconds))

(defun kth-args (kth kind method) (third (kth-next kth kind method)))

(defun kth-quiet (kth kind method &optional (seconds 0.4))
  "True when no event of KIND and METHOD arrives within SECONDS."
  (sleep seconds)
  (sb-thread:with-mutex ((kth-lock kth))
    (notany (lambda (e) (and (eq (first e) kind) (equal (second e) method))) (kth-events kth))))

(defun kth-stop (kth) (stop-host (kth-host kth)))

(defmacro with-kth ((var ext &rest options) &body body)
  `(let ((,var (kth-start ,ext ,@options)))
     (unwind-protect (progn ,@body) (kth-stop ,var))))

(defun kt-run-command (kth name &optional (args "") (id (format nil "cmd-~A" name)))
  (request (kth-host kth) id "command" "tool" name "args" args)
  (kth-next kth :response "response"))

(deftest widget-string-lists-go-out-as-a-push-frame-and-clear-with-null
  (let ((ext (pig:make-extension "w")))
    (pig:command ext "show" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-widget ctx "k" '("a" "b"))))
    (pig:command ext "clear" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-widget ctx "k" nil)))
    (pig:command ext "placed" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-widget ctx "k" #("c") (pig:obj "placement" "belowEditor"))))
    (with-kth (h ext)
      (kt-run-command h "show")
      (kt-check-json "{\"key\":\"k\",\"lines\":[\"a\",\"b\"]}" (kth-args h :widget-push "widget_push"))
      (kt-run-command h "clear")
      (kt-check-json "{\"key\":\"k\",\"content\":null,\"options\":{}}" (kth-args h :call "ui.setWidget"))
      (kt-run-command h "placed")
      (kt-check-json "{\"key\":\"k\",\"content\":[\"c\"],\"options\":{\"placement\":\"belowEditor\"}}" (kth-args h :call "ui.setWidget")))))

(deftest surfaces-carry-the-width-they-were-laid-out-for
  (let ((ext (pig:make-extension "s")))
    (pig:command ext "footer" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-footer ctx (list (format nil "rows@~D" (pig:ctx-width ctx))))))
    (pig:command ext "header" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-header ctx (list (format nil "rows@~D" (pig:ctx-width ctx))))))
    (pig:command ext "unfooter" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-clear-footer ctx)))
    (with-kth (h ext)
      (kt-run-command h "footer")
      (kt-check-json "{\"lines\":[\"rows@80\"],\"width\":80}" (kth-args h :call "ui.setFooter"))
      (kt-run-command h "header")
      (kt-check-json "{\"lines\":[\"rows@80\"],\"width\":80}" (kth-args h :call "ui.setHeader"))
      (hsend (kth-host h) "type" "notify" "notify" (pig:obj "method" "width_change" "args" (pig:obj "width" 60)))
      (kt-run-command h "footer")
      (kt-check-json "{\"lines\":[\"rows@60\"],\"width\":60}" (kth-args h :call "ui.setFooter"))
      (kt-run-command h "unfooter")
      (kt-check-json "{\"clear\":true}" (kth-args h :call "ui.setFooter")))))

(deftest renderers-render-at-the-host-width-and-follow-a-resize
  (let ((ext (pig:make-extension "r")) (renders '()))
    (pig:command ext "footer" "" (lambda (ctx args) (declare (ignore args))
                                   (pig:ctx-set-footer-renderer ctx (lambda (width) (push width renders) (list (format nil "row@~D" width))))))
    (pig:command ext "widget" "" (lambda (ctx args) (declare (ignore args))
                                   (pig:ctx-set-widget-renderer ctx "w" (lambda (width) (list (format nil "wide@~D" width)))
                                                                (pig:obj "placement" "aboveEditor"))))
    (pig:command ext "off" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-footer-renderer ctx nil)))
    (with-kth (h ext)
      (kt-run-command h "footer")
      (kt-check-json "{\"lines\":[\"row@80\"],\"width\":80}" (kth-args h :call "ui.setFooter"))
      (kt-run-command h "widget")
      (kt-check-json "{\"lines\":[\"wide@80\"],\"width\":80,\"key\":\"w\",\"options\":{\"placement\":\"aboveEditor\"}}" (kth-args h :call "ui.setWidget"))
      (hsend (kth-host h) "type" "notify" "notify" (pig:obj "method" "width_change" "args" (pig:obj "width" 50)))
      (kt-check-json "{\"lines\":[\"row@50\"],\"width\":50}" (kth-args h :call "ui.setFooter"))
      (kt-check-json "{\"lines\":[\"wide@50\"],\"width\":50,\"key\":\"w\",\"options\":{\"placement\":\"aboveEditor\"}}" (kth-args h :call "ui.setWidget"))
      (kt-run-command h "off")
      (kt-check-json "{\"clear\":true}" (kth-args h :call "ui.setFooter"))
      (hsend (kth-host h) "type" "notify" "notify" (pig:obj "method" "width_change" "args" (pig:obj "width" 40)))
      (kt-check-json "{\"lines\":[\"wide@40\"],\"width\":40,\"key\":\"w\",\"options\":{\"placement\":\"aboveEditor\"}}" (kth-args h :call "ui.setWidget"))
      (check (kth-quiet h :call "ui.setFooter") "a retired renderer stays retired"))))

(deftest a-footer-renderer-that-signals-is-reported-and-leaves-the-rows
  (let ((ext (pig:make-extension "e")))
    (pig:command ext "footer" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-set-footer-renderer ctx (lambda (width) (declare (ignore width)) (error "bad render")))))
    (with-kth (h ext)
      (kt-run-command h "footer")
      (kt-check-json "{\"message\":\"footer render failed: bad render\",\"level\":\"error\"}" (kth-args h :call "ui.notify"))
      (check (kth-quiet h :call "ui.setFooter")))))

(deftest footer-data-changes-render-the-footer-again
  (let ((ext (pig:make-extension "f")))
    (pig:command ext "footer" "" (lambda (ctx args) (declare (ignore args))
                                   (pig:ctx-set-footer-renderer ctx (lambda (width) (declare (ignore width))
                                                                      (list (format nil "branch=~A" (or (pig:footer-get-git-branch (pig:ctx-footer-data ctx)) "none")))))))
    (with-kth (h ext)
      (kt-run-command h "footer")
      (kt-check-json "{\"lines\":[\"branch=none\"],\"width\":80}" (kth-args h :call "ui.setFooter"))
      (hsend (kth-host h) "type" "notify" "notify" (pig:obj "method" "state_update" "args" (pig:obj "state" (pig:obj "footerData" (pig:obj "gitBranch" "main")))))
      (kt-check-json "{\"lines\":[\"branch=main\"],\"width\":80}" (kth-args h :call "ui.setFooter"))
      (hsend (kth-host h) "type" "notify" "notify" (pig:obj "method" "state_update" "args" (pig:obj "state" (pig:obj "footerData" (pig:obj "gitBranch" "main")))))
      (check (kth-quiet h :call "ui.setFooter") "an unchanged footer data renders nothing"))))

(deftest the-working-members-send-their-arguments
  (let ((ext (pig:make-extension "work")))
    (pig:command ext "go" "" (lambda (ctx args) (declare (ignore args))
                               (pig:ctx-set-working-message ctx "Thinking") (pig:ctx-set-working-message ctx "") (pig:ctx-set-working-message ctx)
                               (pig:ctx-set-hidden-thinking-label ctx "Pondering") (pig:ctx-set-hidden-thinking-label ctx)
                               (pig:ctx-set-working-visible ctx nil)
                               (pig:ctx-set-working-indicator ctx (pig:obj "frames" #("-" "+") "intervalMs" 120))
                               (pig:ctx-set-tools-expanded ctx t)
                               (pig:ctx-bell ctx)))
    (with-kth (h ext)
      (kt-run-command h "go")
      (kt-check-json "{\"message\":\"Thinking\"}" (kth-args h :call "ui.setWorkingMessage"))
      (kt-check-json "{\"message\":\"\"}" (kth-args h :call "ui.setWorkingMessage"))
      (kt-check-json "{}" (kth-args h :call "ui.setWorkingMessage"))
      (kt-check-json "{\"label\":\"Pondering\"}" (kth-args h :call "ui.setHiddenThinkingLabel"))
      (kt-check-json "{}" (kth-args h :call "ui.setHiddenThinkingLabel"))
      (kt-check-json "{\"visible\":false}" (kth-args h :call "ui.setWorkingVisible"))
      (kt-check-json "{\"frames\":[\"-\",\"+\"],\"intervalMs\":120}" (kth-args h :call "ui.setWorkingIndicator"))
      (kt-check-json "{\"expanded\":true}" (kth-args h :call "ui.setToolsExpanded"))
      (kt-check-json "{}" (kth-args h :call "ui.bell")))))

(defun kt-picker (log)
  "The conformance picker: logs every event and input and closes on select with the log."
  (pig:make-custom-component
   :view (lambda (width) (declare (ignore width))
           (pig:kit-view (pig:kit-container (list (pig:kit-text (format nil "~D logged" (length (car log))) 1 0)
                                                  (pig:kit-select-list "kit-tracks" (loop for i below 5 collect (pig:kit-select-item (format nil "k~D" i) (format nil "Track ~D" i) (format nil "d~D" i))) 3
                                                                       :selected-index 2)))
                         :focus "kit-tracks" :theme (pig:obj "accent" "#d75f00")))
   :handle-input (lambda (data) (unless (equal data "noop") (push (format nil "input:~A" data) (car log))) nil)
   :handle-view-event (lambda (event)
                        (let ((value (if (pig:kit-event-item event) (pig:jget (pig:kit-event-item event) "value") "")))
                          (cond ((equal (pig:kit-event-type event) "selectionChange")
                                 (push (format nil "selectionChange:~D:~A" (pig:kit-event-index event) value) (car log)) nil)
                                ((equal (pig:kit-event-type event) "select")
                                 (push (format nil "select:~D:~A" (pig:kit-event-index event) value) (car log))
                                 (pig:make-custom-result :done t :value (format nil "~{~A~^,~}" (reverse (car log)))))
                                ((equal (pig:kit-event-type event) "cancel") (error "cancelled"))
                                (t nil))))))

(defun kt-notify-event (host method args) (hsend host "type" "notify" "notify" (pig:obj "method" method "args" args)))

(deftest custom-view-sends-authoritative-frames-and-receives-events-in-order
  (let ((ext (pig:make-extension "kit-custom")) (log (list nil)))
    (pig:tool ext "pick" "Pick" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore params)) (format nil "~A" (pig:ctx-custom ctx (kt-picker log)))))
    (with-kth (h ext)
      (request (kth-host h) "pick" "tool_call" "tool" "pick" "args" (pig:obj))
      (let* ((call (kth-next h :call "ui.custom")) (key (pig:jget (third call) "key")) (frame (kth-args h :notify "ui.custom.render")))
        (check (null (pig:jget frame "lines")) "an authoritative view frame has no lines")
        (check-equal key (pig:jget frame "key"))
        (check-equal 80 (pig:jget frame "width"))
        (check-equal 1 (pig:jget frame "seq"))
        (check-equal "kit-tracks" (pig:jget (pig:jget frame "view") "focus"))
        (kt-check-json "{\"accent\":\"#d75f00\"}" (pig:jget (pig:jget frame "view") "theme"))
        (check-equal 2 (pig:jref (pig:jget frame "view") "root" "children" 1 "selectedIndex"))
        (kt-check-json "{\"value\":\"k3\",\"label\":\"Track 3\",\"description\":\"d3\"}" (pig:jref (pig:jget frame "view") "root" "children" 1 "items" 3))
        (flet ((event (type index value)
                 (pig:obj "key" key "node" "kit-tracks" "type" type "index" index "item" (pig:obj "value" value "label" "x" "description" "y"))))
          (kt-notify-event (kth-host h) "ui.custom.input" (pig:obj "key" key "data" "noop"))
          (kt-notify-event (kth-host h) "ui.view.event" (event "selectionChange" 3 "k3"))
          (kt-notify-event (kth-host h) "ui.view.event" (pig:obj "key" "custom-999" "node" "kit-tracks" "type" "select" "index" 0))
          (kt-notify-event (kth-host h) "ui.view.event" (event "selectionChange" 4 "k4"))
          (kt-notify-event (kth-host h) "ui.custom.input" (pig:obj "key" key "data" "x"))
          (kt-notify-event (kth-host h) "ui.view.event" (event "select" 0 "k0")))
        (let ((seqs '()) close)
          (loop (let ((next (kth-take h (lambda (e) (eq (first e) :notify)))))
                  (unless next (error "no close"))
                  (when (equal (second next) "ui.custom.close") (setf close (third next)) (return))
                  (check-equal "ui.custom.render" (second next))
                  (push (pig:jget (third next) "seq") seqs)))
          (check-equal '(2 3 4) (reverse seqs) "one frame per changed view, none for the no-op input")
          (check-equal "selectionChange:3:k3,selectionChange:4:k4,input:x,select:0:k0" (pig:jget close "result"))
          (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth call) "id")
                 "call_result" (pig:obj "result" (pig:obj "ok" t "result" (pig:jget close "result"))))
          (check-equal "selectionChange:3:k3,selectionChange:4:k4,input:x,select:0:k0"
                       (pig:jget (pig:jget (third (kth-next h :response "response")) "result") "content")))))))

(deftest a-view-event-error-closes-the-overlay-with-it
  (let ((ext (pig:make-extension "kit-cancel")) (log (list nil)))
    (pig:tool ext "pick" "Pick" (pig:obj "type" "object") (lambda (ctx params) (declare (ignore params)) (format nil "~A" (pig:ctx-custom ctx (kt-picker log)))))
    (with-kth (h ext)
      (request (kth-host h) "pick" "tool_call" "tool" "pick" "args" (pig:obj))
      (let* ((call (kth-next h :call "ui.custom")) (key (pig:jget (third call) "key")))
        (kth-next h :notify "ui.custom.render")
        (kt-notify-event (kth-host h) "ui.view.event" (pig:obj "key" key "node" "kit-tracks" "type" "cancel" "index" 0))
        (check-equal "cancelled" (pig:jget (kth-args h :notify "ui.custom.close") "error"))
        (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth call) "id") "call_result" (pig:obj "result" (pig:obj "ok" :false)))
        (kth-next h :response "response")))))

(deftest a-view-component-sends-an-evicted-image-again-in-an-unchanged-frame
  (let ((ext (pig:make-extension "kit-cover"))
        (cover (pig::kit-sha256-hex (kt-octets "cover"))))
    (pig:tool ext "pick" "Pick" (pig:obj "type" "object")
              (lambda (ctx params) (declare (ignore params))
                (format nil "~A" (pig:ctx-custom ctx (pig:make-custom-component
                                                       :view (lambda (width) (declare (ignore width)) (pig:kit-view (pig:kit-image (kt-octets "cover") "image/png")))
                                                       :handle-input (lambda (data) (declare (ignore data)) (pig:make-custom-result :done t)))))))
    (with-kth (h ext)
      (request (kth-host h) "c" "tool_call" "tool" "pick" "args" (pig:obj))
      (let* ((call (kth-next h :call "ui.custom")) (key (pig:jget (third call) "key")) (first (kth-args h :notify "ui.custom.render")))
        (check-equal cover (pig:jref (pig:jget first "view") "images" 0 "ref"))
        (kt-notify-event (kth-host h) "width_change" (pig:obj "width" 90))
        (let ((resized (kth-args h :notify "ui.custom.render")))
          (check-equal 2 (pig:jget resized "seq"))
          (check-equal 90 (pig:jget resized "width"))
          (check (null (pig:jget (pig:jget resized "view") "images")) "the unchanged view names the ref only"))
        (kt-notify-event (kth-host h) "ui.view.evicted" (pig:obj "refs" #("0000")))
        (kt-notify-event (kth-host h) "ui.view.evicted" (pig:obj "refs" (vector cover)))
        (let ((again (kth-args h :notify "ui.custom.render")))
          (check-equal 3 (pig:jget again "seq"))
          (check-equal "Y292ZXI=" (pig:jref (pig:jget again "view") "images" 0 "data")))
        (kt-notify-event (kth-host h) "ui.custom.input" (pig:obj "key" key "data" "q"))
        (kth-next h :notify "ui.custom.close")
        (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth call) "id") "call_result" (pig:obj "result" (pig:obj "ok" t)))
        (kth-next h :response "response")))))

(deftest widget-header-and-footer-views-send-images-once-and-again-after-eviction
  (let ((ext (pig:make-extension "kit-surfaces")) (cover (pig::kit-sha256-hex (kt-octets "cover"))))
    (flet ((cover-view () (pig:kit-view (pig:kit-container (list (pig:kit-image (kt-octets "cover") "image/png") (pig:kit-lines-progress (pig:kit-lines '("AB")) 1 4))))))
      (pig:tool ext "show" "Show" (pig:obj "type" "object")
                (lambda (ctx params) (declare (ignore params))
                  (pig:ctx-set-widget ctx "w" (cover-view))
                  (pig:ctx-set-widget ctx "w" (cover-view))
                  (pig:ctx-set-header-view ctx (cover-view))
                  (pig:ctx-set-footer-view ctx (pig:kit-view (pig:kit-spacer 1)))
                  (pig:ctx-set-widget ctx "w2" (pig:kit-view (pig:kit-spacer 2)) (pig:obj "placement" "belowEditor"))
                  "ok"))
      (pig:tool ext "plain" "Plain" (pig:obj "type" "object")
                (lambda (ctx params) (declare (ignore params)) (pig:ctx-set-widget ctx "w" (cover-view)) "ok"))
      (with-kth (h ext :state (pig:obj "hasUI" t "frontend" t))
        (request (kth-host h) "show" "tool_call" "tool" "show" "args" (pig:obj))
        (let ((push (kth-args h :widget-push "widget_push")))
          (check (null (pig:jget push "lines")))
          (kt-check-json (format nil "[{\"ref\":\"~A\",\"mimeType\":\"image/png\",\"data\":\"Y292ZXI=\"}]" cover) (pig:jget (pig:jget push "view") "images"))
          (kt-check-json "{\"value\":1.0,\"max\":4.0}" (pig:jref (pig:jget push "view") "root" "children" 1 "progress") "a frontend draws"))
        (check (null (pig:jget (pig:jget (kth-args h :widget-push "widget_push") "view") "images")) "the ref is sent once")
        (let ((header (kth-args h :call "ui.setHeader")))
          (check-equal cover (pig:jref (pig:jget header "view") "root" "children" 0 "ref"))
          (check (null (pig:jget (pig:jget header "view") "images")))
          (check (and (null (pig:jget header "lines")) (null (pig:jget header "width")))))
        (kt-check-json "{\"view\":{\"root\":{\"kind\":\"spacer\",\"lines\":1}}}" (kth-args h :call "ui.setFooter"))
        (kt-check-json "{\"key\":\"w2\",\"view\":{\"root\":{\"kind\":\"spacer\",\"lines\":2}},\"options\":{\"placement\":\"belowEditor\"}}" (kth-args h :call "ui.setWidget"))
        (kth-next h :response "response")
        ;; the host dropped the cover: the widget and the header that name it go out again; the footer and w2 do not
        (kt-notify-event (kth-host h) "ui.view.evicted" (pig:obj "refs" (vector cover)))
        (let ((resent (kth-args h :widget-push "widget_push")))
          (check-equal "w" (pig:jget resent "key"))
          (kt-check-json (format nil "[{\"ref\":\"~A\",\"mimeType\":\"image/png\",\"data\":\"Y292ZXI=\"}]" cover) (pig:jget (pig:jget resent "view") "images")))
        (let ((header (kth-args h :call "ui.setHeader")))
          (check-equal cover (pig:jref (pig:jget header "view") "root" "children" 0 "ref"))
          (check (null (pig:jget (pig:jget header "view") "images")) "the data travels once"))
        (check (kth-quiet h :call "ui.setFooter"))
        ;; without a frontend the progress annotation is not sent
        (kt-notify-event (kth-host h) "state_update" (pig:obj "state" (pig:obj "hasUI" t)))
        (request (kth-host h) "plain" "tool_call" "tool" "plain" "args" (pig:obj))
        (kt-check-json "{\"kind\":\"lines\",\"content\":[\"AB\"]}" (pig:jref (pig:jget (kth-args h :widget-push "widget_push") "view") "root" "children" 1))))))

(deftest theme-and-tools-expanded-changes-reach-their-subscribers-in-order
  (let ((ext (pig:make-extension "feeds")) (themes '()) (expanded '()) (lock (sb-thread:make-mutex)))
    (pig:command ext "sub" "" (lambda (ctx args) (declare (ignore args))
                                (pig:ctx-on-theme-change ctx (lambda (theme) (sb-thread:with-mutex (lock) (push (list (pig:theme-name theme) (pig:theme-fg theme "success" "x")) themes))))
                                (pig:ctx-on-tools-expanded-change ctx (lambda (value) (sb-thread:with-mutex (lock) (push value expanded))))))
    (pig:command ext "read" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-notify ctx (pig:theme-name (pig:ctx-theme ctx)))))
    (flet ((palette (name rgb) (pig:from-json (format nil "{\"name\":\"~A\",\"foregrounds\":{\"success\":\"\\u001b[38;2;~A;~A;~Am\"},\"backgrounds\":{}}" name rgb rgb rgb)))
           (wait-for (predicate) (loop repeat 200 until (sb-thread:with-mutex (lock) (funcall predicate)) do (sleep 0.02))))
      (with-kth (h ext :state (pig:obj "hasUI" t "theme" (palette "first" 1) "toolsExpanded" :false))
        (kt-run-command h "sub")
        (kt-run-command h "read")
        (check-equal "first" (pig:jget (kth-args h :call "ui.notify") "message"))
        ;; the same palette is not a change
        (kt-notify-event (kth-host h) "state_update" (pig:obj "state" (pig:obj "theme" (palette "first" 1) "toolsExpanded" :false)))
        (kt-notify-event (kth-host h) "theme_change" (palette "second" 2))
        (kt-notify-event (kth-host h) "theme_change" (palette "second" 2))
        (kt-notify-event (kth-host h) "state_update" (pig:obj "state" (pig:obj "theme" (palette "third" 3) "toolsExpanded" t)))
        (wait-for (lambda () (and (= 2 (length themes)) (= 1 (length expanded)))))
        (check-equal (list (list "second" (concatenate 'string (kt-esc "38;2;2;2;2m") "x" (kt-esc "39m")) )
                           (list "third" (concatenate 'string (kt-esc "38;2;3;3;3m") "x" (kt-esc "39m"))))
                     (reverse themes))
        (check-equal '(t) expanded)
        (kt-run-command h "read" "" "again")
        (check-equal "third" (pig:jget (kth-args h :call "ui.notify") "message") "a held theme follows the host")))))

(deftest ctx-custom-does-nothing-without-a-ui-and-keeps-the-dialog-scope-in-rpc-mode
  (let ((ext (pig:make-extension "custom-modes")) (results '()))
    (pig:command ext "go" "" (lambda (ctx args) (declare (ignore args))
                               (push (pig:ctx-custom ctx (pig:make-custom-component :render (lambda (width) (declare (ignore width)) '("x")))) results)))
    (with-kth (h ext :state (pig:obj "hasUI" :false))
      (kt-run-command h "go")
      (check-equal '(nil) results)
      (check (kth-quiet h :call "ui.custom")))))

(deftest overlay-handle-controls-call-the-host-and-mirror-its-state
  (let ((ext (pig:make-extension "handle")) (seen '()) (done (sb-thread:make-semaphore)))
    (pig:command ext "go" ""
                 (lambda (ctx args) (declare (ignore args))
                   (pig:ctx-custom ctx (pig:make-custom-component :render (lambda (width) (declare (ignore width)) '("x"))
                                                                  :handle-input (lambda (data) (declare (ignore data)) (pig:make-custom-result :done t)))
                                   :options (pig:obj "overlay" t)
                                   :on-handle (lambda (handle)
                                                (push (list (pig:overlay-focused-p handle) (pig:overlay-hidden-p handle) (pig:jget (pig:overlay-get-bounds handle) "width")) seen)
                                                (pig:overlay-set-hidden handle t)
                                                (push (list (pig:overlay-hidden-p handle) (pig:overlay-focused-p handle) (pig:overlay-get-bounds handle)) seen)
                                                (pig:overlay-unfocus handle :target nil)
                                                (sb-thread:signal-semaphore done)))))
    (with-kth (h ext)
      (request (kth-host h) "go" "command" "tool" "go" "args" "")
      (let* ((call (kth-next h :call "ui.custom")) (key (pig:jget (third call) "key")))
        (check-equal t (pig:jget (third call) "hasHandle"))
        (kt-notify-event (kth-host h) "ui.custom.opened"
                      (pig:obj "key" key "hidden" :false "focused" t "visible" t "bounds" (pig:obj "row" 3 "col" 4 "width" 20 "height" 5)))
        (let ((control (kth-next h :call "ui.custom.control")))
          (kt-check-json (format nil "{\"key\":\"~A\",\"action\":\"setHidden\",\"hidden\":true}" key) (third control))
          (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth control) "id")
                 "call_result" (pig:obj "result" (pig:obj "hidden" t "focused" :false "visible" :false))))
        (let ((control (kth-next h :call "ui.custom.control")))
          (kt-check-json (format nil "{\"key\":\"~A\",\"action\":\"unfocus\",\"target\":{\"kind\":\"null\"}}" key) (third control))
          (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth control) "id")
                 "call_result" (pig:obj "result" (pig:obj "hidden" t "focused" :false "visible" :false))))
        (sb-thread:wait-on-semaphore done :timeout 5)
        (check-equal '((t nil 20) (t nil nil)) (reverse seen))
        (kt-notify-event (kth-host h) "ui.custom.input" (pig:obj "key" key "data" "q"))
        (kth-next h :notify "ui.custom.close")
        (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth call) "id") "call_result" (pig:obj "result" (pig:obj "ok" t)))
        (kth-next h :response "response")))))

(deftest mouse-events-reach-a-component-that-takes-the-mouse
  (let ((ext (pig:make-extension "mouse")) (log '()))
    (pig:tool ext "go" "" (pig:obj "type" "object")
              (lambda (ctx params) (declare (ignore params))
                (format nil "~A"
                        (pig:ctx-custom ctx (pig:make-custom-component
                                             :render (lambda (width) (declare (ignore width)) '("mouse probe"))
                                             :handle-input (lambda (data) (declare (ignore data)) nil)
                                             :handle-mouse (lambda (event)
                                                             (push (format nil "~A/~A/~D,~D/~D,~D/~Dx~D/w~D/c~D/~A" (pig:mouse-event-type event) (pig:mouse-event-button event)
                                                                           (pig:mouse-event-x event) (pig:mouse-event-y event) (pig:mouse-event-screen-x event)
                                                                           (pig:mouse-event-screen-y event) (pig:mouse-event-width event) (pig:mouse-event-height event)
                                                                           (pig:mouse-event-wheel-delta event) (pig:mouse-event-click-count event)
                                                                           (format nil "~:[~;S~]~:[~;A~]~:[~;C~]" (pig:mouse-event-shift event) (pig:mouse-event-alt event) (pig:mouse-event-ctrl event)))
                                                                   log)
                                                             (if (equal (pig:mouse-event-type event) "click")
                                                                 (pig:make-custom-result :done t :value (format nil "~{~A~^,~}" (reverse log)))
                                                                 nil)))))))
    (with-kth (h ext)
      (request (kth-host h) "go" "tool_call" "tool" "go" "args" (pig:obj))
      (let* ((call (kth-next h :call "ui.custom")) (key (pig:jget (third call) "key")) (frame (kth-args h :notify "ui.custom.render")))
        (check-equal t (pig:jget frame "mouse") "a frame of a component that takes the mouse says so")
        (kt-check-json "[\"mouse probe\"]" (pig:jget frame "lines"))
        (kt-notify-event (kth-host h) "ui.custom.mouse"
                      (pig:obj "key" key "event" (pig:obj "type" "wheel" "button" "none" "x" 2 "y" 1 "screenX" 12 "screenY" 6 "width" 30 "height" 4 "shift" t "ctrl" t "wheelDelta" -3 "clickCount" 0)))
        (kt-notify-event (kth-host h) "ui.custom.mouse"
                      (pig:obj "key" key "event" (pig:obj "type" "click" "button" "left" "x" 1 "y" 2 "screenX" 11 "screenY" 7 "width" 30 "height" 4 "alt" t "clickCount" 2)))
        (check-equal "ui.custom.close" (second (kth-next h :notify "ui.custom.close")))
        (hsend (kth-host h) "type" "call_result" "id" (pig:jget (fourth call) "id") "call_result" (pig:obj "result" (pig:obj "ok" t "result" "x")))
        (kth-next h :response "response")
        (check-equal '("wheel/none/2,1/12,6/30x4/w-3/c0/SC" "click/left/1,2/11,7/30x4/w0/c2/A") (reverse log))))))
