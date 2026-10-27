;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's keys.ts: raw terminal input against key identifiers (legacy sequences, Kitty CSI-u, xterm modifyOtherKeys). A port of Pi's
;;;; functions (Python SDK: tui/_keys.py). JavaScript number semantics are kept where they decide a result: a digit run parses as an integer
;;;; rounded to a double (:INF when it overflows), and bitwise operators truncate to int32.

(in-package #:pig)

(defvar *tui-kitty-protocol-active* nil)

(defun tui-set-kitty-protocol-active (active)
  "Set the global Kitty keyboard protocol state (pi-tui's setKittyProtocolActive)."
  (setf *tui-kitty-protocol-active* (and active (not (eq active :false)) t)))

(defun tui-kitty-protocol-active-p ()
  "Whether the Kitty keyboard protocol is active (pi-tui's isKittyProtocolActive)."
  *tui-kitty-protocol-active*)

(defparameter *tui-key-names*
  '((:escape . "escape") (:esc . "esc") (:enter . "enter") (:return . "return") (:tab . "tab") (:space . "space") (:backspace . "backspace")
    (:delete . "delete") (:insert . "insert") (:clear . "clear") (:home . "home") (:end . "end") (:page-up . "pageUp") (:page-down . "pageDown")
    (:up . "up") (:down . "down") (:left . "left") (:right . "right")
    (:f1 . "f1") (:f2 . "f2") (:f3 . "f3") (:f4 . "f4") (:f5 . "f5") (:f6 . "f6") (:f7 . "f7") (:f8 . "f8") (:f9 . "f9") (:f10 . "f10")
    (:f11 . "f11") (:f12 . "f12")
    (:backtick . "`") (:hyphen . "-") (:equals . "=") (:leftbracket . "[") (:rightbracket . "]") (:backslash . "\\") (:semicolon . ";")
    (:quote . "'") (:comma . ",") (:period . ".") (:slash . "/") (:exclamation . "!") (:at . "@") (:hash . "#") (:dollar . "$")
    (:percent . "%") (:caret . "^") (:ampersand . "&") (:asterisk . "*") (:leftparen . "(") (:rightparen . ")") (:underscore . "_")
    (:plus . "+") (:pipe . "|") (:tilde . "~") (:leftbrace . "{") (:rightbrace . "}") (:colon . ":") (:lessthan . "<")
    (:greaterthan . ">") (:question . "?"))
  "pi-tui's Key constants: the keyword names (page-up for pageUp) and the key identifiers they stand for.")

(defun tui-key (key &rest modifiers)
  "pi-tui's Key: the identifier of KEY (a keyword of *tui-key-names* such as :page-up, or a string such as \"c\") with MODIFIERS
(:shift :alt :ctrl :super, in the order given): (tui-key \"c\" :ctrl) is \"ctrl+c\", (tui-key :enter :alt) is \"alt+enter\"."
  (let ((name (if (keywordp key)
                  (or (cdr (assoc key *tui-key-names*)) (error "unknown key name ~S" key))
                  key)))
    (format nil "~{~A+~}~A" (mapcar (lambda (m) (string-downcase (symbol-name m))) modifiers) name)))

(defparameter *tk-symbol-keys* "`-=[]\\;',./!@#$%^&*()_+|~{}:<>?")
(defparameter *tk-shifted-symbol-keys* "~!@#$%^&*()_+|{}:<>?")
(defconstant +tk-shift+ 1)
(defconstant +tk-alt+ 2)
(defconstant +tk-ctrl+ 4)
(defconstant +tk-super+ 8)
(defconstant +tk-lock-mask+ 192)
(defconstant +tk-escape+ 27)
(defconstant +tk-tab+ 9)
(defconstant +tk-enter+ 13)
(defconstant +tk-space+ 32)
(defconstant +tk-backspace+ 127)
(defconstant +tk-kp-enter+ 57414)
(defconstant +tk-up+ -1)
(defconstant +tk-down+ -2)
(defconstant +tk-right+ -3)
(defconstant +tk-left+ -4)
(defconstant +tk-delete+ -10)
(defconstant +tk-insert+ -11)
(defconstant +tk-page-up+ -12)
(defconstant +tk-page-down+ -13)
(defconstant +tk-home+ -14)
(defconstant +tk-end+ -15)

(defparameter *tk-functional-equivalents*
  `((57399 . 48) (57400 . 49) (57401 . 50) (57402 . 51) (57403 . 52) (57404 . 53) (57405 . 54) (57406 . 55) (57407 . 56) (57408 . 57)
    (57409 . 46) (57410 . 47) (57411 . 42) (57412 . 45) (57413 . 43) (57415 . 61) (57416 . 44)
    (57417 . ,+tk-left+) (57418 . ,+tk-right+) (57419 . ,+tk-up+) (57420 . ,+tk-down+) (57421 . ,+tk-page-up+) (57422 . ,+tk-page-down+)
    (57423 . ,+tk-home+) (57424 . ,+tk-end+) (57425 . ,+tk-insert+) (57426 . ,+tk-delete+)))

(defparameter *tk-legacy*
  `(("up" ,(tesc "[A") ,(tesc "OA")) ("down" ,(tesc "[B") ,(tesc "OB")) ("right" ,(tesc "[C") ,(tesc "OC")) ("left" ,(tesc "[D") ,(tesc "OD"))
    ("home" ,(tesc "[H") ,(tesc "OH") ,(tesc "[1~") ,(tesc "[7~")) ("end" ,(tesc "[F") ,(tesc "OF") ,(tesc "[4~") ,(tesc "[8~"))
    ("insert" ,(tesc "[2~")) ("delete" ,(tesc "[3~")) ("pageUp" ,(tesc "[5~") ,(tesc "[[5~")) ("pageDown" ,(tesc "[6~") ,(tesc "[[6~"))
    ("clear" ,(tesc "[E") ,(tesc "OE"))
    ("f1" ,(tesc "OP") ,(tesc "[11~") ,(tesc "[[A")) ("f2" ,(tesc "OQ") ,(tesc "[12~") ,(tesc "[[B"))
    ("f3" ,(tesc "OR") ,(tesc "[13~") ,(tesc "[[C")) ("f4" ,(tesc "OS") ,(tesc "[14~") ,(tesc "[[D"))
    ("f5" ,(tesc "[15~") ,(tesc "[[E")) ("f6" ,(tesc "[17~")) ("f7" ,(tesc "[18~")) ("f8" ,(tesc "[19~")) ("f9" ,(tesc "[20~"))
    ("f10" ,(tesc "[21~")) ("f11" ,(tesc "[23~")) ("f12" ,(tesc "[24~"))))

(defparameter *tk-legacy-shift*
  `(("up" ,(tesc "[a")) ("down" ,(tesc "[b")) ("right" ,(tesc "[c")) ("left" ,(tesc "[d")) ("clear" ,(tesc "[e"))
    ("insert" ,(tesc "[2$")) ("delete" ,(tesc "[3$")) ("pageUp" ,(tesc "[5$")) ("pageDown" ,(tesc "[6$"))
    ("home" ,(tesc "[7$")) ("end" ,(tesc "[8$"))))

(defparameter *tk-legacy-ctrl*
  `(("up" ,(tesc "Oa")) ("down" ,(tesc "Ob")) ("right" ,(tesc "Oc")) ("left" ,(tesc "Od")) ("clear" ,(tesc "Oe"))
    ("insert" ,(tesc "[2^")) ("delete" ,(tesc "[3^")) ("pageUp" ,(tesc "[5^")) ("pageDown" ,(tesc "[6^"))
    ("home" ,(tesc "[7^")) ("end" ,(tesc "[8^"))))

(defparameter *tk-legacy-ids*
  (let ((table (make-hash-table :test 'equal)))
    (loop for (suffix id) on
          '("OA" "up" "OB" "down" "OC" "right" "OD" "left" "OH" "home" "OF" "end"
            "[E" "clear" "OE" "clear" "Oe" "ctrl+clear" "[e" "shift+clear"
            "[2~" "insert" "[2$" "shift+insert" "[2^" "ctrl+insert"
            "[3$" "shift+delete" "[3^" "ctrl+delete" "[[5~" "pageUp" "[[6~" "pageDown"
            "[a" "shift+up" "[b" "shift+down" "[c" "shift+right" "[d" "shift+left"
            "Oa" "ctrl+up" "Ob" "ctrl+down" "Oc" "ctrl+right" "Od" "ctrl+left"
            "[5$" "shift+pageUp" "[6$" "shift+pageDown" "[7$" "shift+home" "[8$" "shift+end"
            "[5^" "ctrl+pageUp" "[6^" "ctrl+pageDown" "[7^" "ctrl+home" "[8^" "ctrl+end"
            "OP" "f1" "OQ" "f2" "OR" "f3" "OS" "f4"
            "[11~" "f1" "[12~" "f2" "[13~" "f3" "[14~" "f4"
            "[[A" "f1" "[[B" "f2" "[[C" "f3" "[[D" "f4" "[[E" "f5"
            "[15~" "f5" "[17~" "f6" "[18~" "f7" "[19~" "f8" "[20~" "f9"
            "[21~" "f10" "[23~" "f11" "[24~" "f12"
            "b" "alt+left" "f" "alt+right" "p" "alt+up" "n" "alt+down")
          by #'cddr
          do (setf (gethash (tui-esc suffix) table) id))
    table)
  "Legacy sequence -> key identifier.")

;;; ------------------------------------------------------------------ JavaScript numbers

(defun tk-num (text)
  "Number.parseInt(text, 10) of a digit run: the integer rounded to a double, or :INF when it overflows."
  (let* ((n (parse-integer text)) (len (integer-length n)))
    (when (> len 53)
      (let* ((shift (- len 53)) (q (ash n (- shift))) (rem (- n (ash q shift))) (half (ash 1 (1- shift))))
        (when (or (> rem half) (and (= rem half) (oddp q))) (incf q))
        (setf n (ash q shift))))
    (if (>= n (expt 2 1024)) :inf n)))

(defun tk-i32 (value)
  "JavaScript ToInt32 (0 for :INF)."
  (if (integerp value)
      (let ((n (logand value #xFFFFFFFF))) (if (>= n #x80000000) (- n #x100000000) n))
      0))

(defun tk-in (value allowed)
  "Whether String.fromCharCode(VALUE) is one of the characters of the string ALLOWED."
  (and (find (code-char (logand (tk-i32 value) #xFFFF)) allowed) t))

(defun tk-eq (a b) (and (integerp a) (integerp b) (= a b)))
(defun tk-between (lo value hi) (and (integerp value) (<= lo value hi)))

(defun tk-normalize-functional (codepoint)
  (or (and (integerp codepoint) (cdr (assoc codepoint *tk-functional-equivalents*))) codepoint))

(defun tk-normalize-shifted-letter (codepoint modifier)
  (if (and (logtest (logandc2 (tk-i32 modifier) +tk-lock-mask+) +tk-shift+) (tk-between 65 codepoint 90)) (+ codepoint 32) codepoint))

(defun tk-logical-shifted-symbol (codepoint modifier allowed)
  "Values: the normalized code point and the modifier without shift, or NIL."
  (when (and codepoint (logtest (tk-i32 modifier) +tk-shift+))
    (let ((normalized (tk-normalize-functional codepoint)))
      (when (tk-in normalized allowed)
        (values normalized (logandc2 (tk-i32 modifier) +tk-shift+))))))

;;; ------------------------------------------------------------------ sequence parsing

(defun tk-digits-end (data i)
  (loop while (and (< i (length data)) (char<= #\0 (char data i) #\9)) do (incf i))
  i)

(defun tk-match (data lead stages finals)
  "Match ESC [ LEAD digits STAGES FINAL against all of DATA. A stage is (char min-digits required): an optional group of CHAR and digits.
Return the digit groups (NIL for an absent one) and the final character, or NIL."
  (let ((n (length data)) (head (length lead)))
    (when (and (> n (+ 3 head)) (char= (char data 0) +tui-esc+) (char= (char data 1) #\[) (string= lead data :start2 2 :end2 (+ 2 head)))
      (let ((end (tk-digits-end data (+ 2 head))))
        (when (> end (+ 2 head))
          (labels ((walk (stages pos groups)
                     (if (null stages)
                         (when (and (= pos (1- n)) (find (char data pos) finals))
                           (append (reverse groups) (list (char data pos))))
                         (destructuring-bind (c min &optional required) (car stages)
                           (or (when (and (< pos n) (char= (char data pos) c))
                                 (let ((stop (tk-digits-end data (1+ pos))))
                                   (when (>= (- stop pos 1) min)
                                     (walk (cdr stages) stop (cons (subseq data (1+ pos) stop) groups)))))
                               (unless required (walk (cdr stages) pos (cons nil groups))))))))
            (walk stages end (list (subseq data (+ 2 head) end)))))))))

(defun tk-group-num (text) (and text (plusp (length text)) (tk-num text)))

(defun tk-parse-kitty (data)
  "Values: code point, shifted key, base layout key, modifier of a Kitty-style key sequence, or NIL."
  (let ((g (tk-match data "" '((#\: 0) (#\: 1) (#\; 1) (#\: 1)) "u")))
    (when g
      (return-from tk-parse-kitty
        (values (tk-num (first g)) (tk-group-num (second g)) (tk-group-num (third g))
                (let ((m (if (fourth g) (tk-num (fourth g)) 1))) (if (integerp m) (1- m) m))))))
  (let ((g (tk-match data "1;" '((#\: 1)) "ABCD")))
    (when g
      (return-from tk-parse-kitty
        (values (ecase (third g) (#\A +tk-up+) (#\B +tk-down+) (#\C +tk-right+) (#\D +tk-left+))
                nil nil (let ((m (tk-num (first g)))) (if (integerp m) (1- m) m))))))
  (let ((g (tk-match data "" '((#\; 1) (#\: 1)) "~")))
    (when g
      (let* ((key (tk-num (first g)))
             (codepoint (and (integerp key) (cdr (assoc key `((2 . ,+tk-insert+) (3 . ,+tk-delete+) (5 . ,+tk-page-up+) (6 . ,+tk-page-down+)
                                                                (7 . ,+tk-home+) (8 . ,+tk-end+)))))))
        (when codepoint
          (return-from tk-parse-kitty
            (values codepoint nil nil (let ((m (if (second g) (tk-num (second g)) 1))) (if (integerp m) (1- m) m))))))))
  (let ((g (tk-match data "1;" '((#\: 1)) "HF")))
    (when g
      (values (if (char= (third g) #\H) +tk-home+ +tk-end+) nil nil
              (let ((m (tk-num (first g)))) (if (integerp m) (1- m) m))))))

(defun tk-parse-modify-other (data)
  "Values: key code and modifier of an xterm modifyOtherKeys sequence, or NIL."
  (let ((g (tk-match data "27;" '((#\; 1 t)) "~")))
    (when g
      (values (tk-num (second g)) (let ((m (tk-num (first g)))) (if (integerp m) (1- m) m))))))

(defun tk-matches-kitty (data expected-codepoint expected-modifier)
  (multiple-value-bind (codepoint shifted base modifier) (tk-parse-kitty data)
    (when codepoint
      (let* ((actual-mod (logandc2 (tk-i32 modifier) +tk-lock-mask+))
             (expected-mod (logandc2 expected-modifier +tk-lock-mask+))
             (normalized (tk-normalize-shifted-letter (tk-normalize-functional codepoint) modifier))
             (normalized-expected (tk-normalize-shifted-letter (tk-normalize-functional expected-codepoint) expected-modifier)))
        (when (and (= actual-mod expected-mod) (tk-eq normalized normalized-expected)) (return-from tk-matches-kitty t))
        (multiple-value-bind (logical-code logical-mod) (tk-logical-shifted-symbol shifted actual-mod *tk-symbol-keys*)
          (when (and logical-code (= (logandc2 logical-mod +tk-lock-mask+) expected-mod)
                     (tk-eq logical-code (tk-normalize-functional expected-codepoint)))
            (return-from tk-matches-kitty t)))
        (and (= actual-mod expected-mod) base (tk-eq base expected-codepoint)
             (not (tk-between 97 normalized 122)) (not (tk-in normalized *tk-symbol-keys*)))))))

(defun tk-matches-modify-other (data expected-keycode expected-modifier)
  (multiple-value-bind (code modifier) (tk-parse-modify-other data)
    (and code (tk-eq code expected-keycode) (tk-eq modifier expected-modifier))))

(defun tk-matches-printable-modify-other (data expected-keycode expected-modifier)
  (multiple-value-bind (codepoint modifier) (tk-parse-modify-other data)
    (when codepoint
      (or (and (tk-eq modifier expected-modifier)
               (tk-eq (tk-normalize-shifted-letter codepoint modifier) (tk-normalize-shifted-letter expected-keycode expected-modifier)))
          (multiple-value-bind (logical-code logical-mod) (tk-logical-shifted-symbol codepoint modifier *tk-shifted-symbol-keys*)
            (and logical-code (= logical-mod expected-modifier) (tk-eq logical-code expected-keycode)))))))

(defun tk-env-set-p (name) (plusp (length (sb-ext:posix-getenv name))))

(defun tk-windows-terminal-session-p ()
  (and (tk-env-set-p "WT_SESSION") (not (tk-env-set-p "SSH_CONNECTION")) (not (tk-env-set-p "SSH_CLIENT")) (not (tk-env-set-p "SSH_TTY"))))

(defun tk-matches-raw-backspace (data expected-modifier)
  (cond ((string= data (tstr 127)) (= expected-modifier 0))
        ((string/= data (tstr 8)) nil)
        ((tk-windows-terminal-session-p) (= expected-modifier +tk-ctrl+))
        (t (= expected-modifier 0))))

(defun tk-raw-ctrl-char (key)
  (let* ((char (char (tui-js-lower key) 0)) (code (char-code char)))
    (cond ((or (<= 97 code 122) (find char "[\\]_")) (string (code-char (logand code 31))))
          ((char= char #\-) (tstr 31)))))

(defun tk-legacy (data name table) (member data (cdr (assoc name table :test #'string=)) :test #'string=))

(defun tk-legacy-modifier (data key modifier)
  (cond ((= modifier +tk-shift+) (tk-legacy data key *tk-legacy-shift*))
        ((= modifier +tk-ctrl+) (tk-legacy data key *tk-legacy-ctrl*))))

(defun tk-parse-key-id (key-id)
  "Values: the key and the list of its modifier names, or NIL for an invalid identifier."
  (let ((key (tui-js-lower key-id)) (modifiers '()))
    (loop
      (let ((modifier (find-if (lambda (name) (and (> (length key) (length name)) (string= name key :end2 (length name)) (char= (char key (length name)) #\+)))
                               '("shift" "alt" "ctrl" "super"))))
        (unless modifier (return))
        (when (member modifier modifiers :test #'string=) (return-from tk-parse-key-id nil))
        (push modifier modifiers)
        (setf key (subseq key (1+ (length modifier))))))
    (when (or (zerop (length key)) (and (find #\+ key) (string/= key "+")))
      (return-from tk-parse-key-id nil))
    (values key modifiers t)))

(defun tui-matches-key (data key-id)
  "Whether raw terminal DATA is the key KEY-ID (pi-tui's matchesKey)."
  (multiple-value-bind (key names valid) (tk-parse-key-id key-id)
    (unless valid (return-from tui-matches-key nil))
    (let* ((modifier (+ (if (member "shift" names :test #'string=) +tk-shift+ 0) (if (member "alt" names :test #'string=) +tk-alt+ 0)
                        (if (member "ctrl" names :test #'string=) +tk-ctrl+ 0) (if (member "super" names :test #'string=) +tk-super+ 0)))
           (kitty *tui-kitty-protocol-active*) (data (tui-simple data))
           (functional '(("insert" "insert" . -11) ("delete" "delete" . -10) ("home" "home" . -14) ("end" "end" . -15)
                         ("pageup" "pageUp" . -12) ("pagedown" "pageDown" . -13))))
      (flet ((is (&rest strings) (member data strings :test #'string=))
             (kit (cp mod) (tk-matches-kitty data cp mod))
             (other (cp mod) (tk-matches-modify-other data cp mod))
             (is-key (&rest names) (member key names :test #'string=)))
        (cond
          ((is-key "escape" "esc")
           (and (= modifier 0) (or (is (tstr 27)) (kit +tk-escape+ 0) (other +tk-escape+ 0))))
          ((is-key "space")
           (when (and (not kitty) (or (and (= modifier +tk-ctrl+) (is (tstr 0))) (and (= modifier +tk-alt+) (is (tesc " ")))))
             (return-from tui-matches-key t))
           (if (= modifier 0)
               (or (is " ") (kit +tk-space+ 0) (other +tk-space+ 0))
               (or (kit +tk-space+ modifier) (other +tk-space+ modifier))))
          ((is-key "tab")
           (cond ((= modifier +tk-shift+) (or (is (tesc "[Z")) (kit +tk-tab+ +tk-shift+) (other +tk-tab+ +tk-shift+)))
                 ((= modifier 0) (or (is (tstr 9)) (kit +tk-tab+ 0)))
                 (t (or (kit +tk-tab+ modifier) (other +tk-tab+ modifier)))))
          ((is-key "enter" "return")
           (cond ((= modifier +tk-shift+)
                  (or (kit +tk-enter+ +tk-shift+) (kit +tk-kp-enter+ +tk-shift+) (other +tk-enter+ +tk-shift+)
                      (and kitty (is (tescc 13) (tstr 10)))))
                 ((= modifier +tk-alt+)
                  (or (kit +tk-enter+ +tk-alt+) (kit +tk-kp-enter+ +tk-alt+) (other +tk-enter+ +tk-alt+)
                      (and (not kitty) (is (tescc 13)))))
                 ((= modifier 0)
                  (or (is (tstr 13)) (and (not kitty) (is (tstr 10))) (is (tesc "OM"))
                      (kit +tk-enter+ 0) (kit +tk-kp-enter+ 0)))
                 (t (or (kit +tk-enter+ modifier) (kit +tk-kp-enter+ modifier) (other +tk-enter+ modifier)))))
          ((is-key "backspace")
           (cond ((= modifier +tk-alt+)
                  (or (is (tescc 127) (tescc 8)) (kit +tk-backspace+ +tk-alt+) (other +tk-backspace+ +tk-alt+)))
                 ((= modifier +tk-ctrl+)
                  (or (tk-matches-raw-backspace data +tk-ctrl+) (kit +tk-backspace+ +tk-ctrl+) (other +tk-backspace+ +tk-ctrl+)))
                 ((= modifier 0)
                  (or (tk-matches-raw-backspace data 0) (kit +tk-backspace+ 0) (other +tk-backspace+ 0)))
                 (t (or (kit +tk-backspace+ modifier) (other +tk-backspace+ modifier)))))
          ((assoc key functional :test #'string=)
           (destructuring-bind (name . code) (cdr (assoc key functional :test #'string=))
             (if (= modifier 0)
                 (or (tk-legacy data name *tk-legacy*) (kit code 0))
                 (or (tk-legacy-modifier data name modifier) (kit code modifier)))))
          ((is-key "clear")
           (if (= modifier 0) (tk-legacy data "clear" *tk-legacy*) (tk-legacy-modifier data "clear" modifier)))
          ((is-key "up" "down" "left" "right")
           (let ((code (cdr (assoc key `(("up" . ,+tk-up+) ("down" . ,+tk-down+) ("left" . ,+tk-left+) ("right" . ,+tk-right+)) :test #'string=))))
             (cond ((and (string= key "up") (= modifier +tk-alt+)) (or (is (tesc "p")) (kit code +tk-alt+)))
                   ((and (string= key "down") (= modifier +tk-alt+)) (or (is (tesc "n")) (kit code +tk-alt+)))
                   ((and (string= key "left") (= modifier +tk-alt+))
                    (or (is (tesc "[1;3D")) (and (not kitty) (is (tesc "B"))) (is (tesc "b")) (kit code +tk-alt+)))
                   ((and (string= key "right") (= modifier +tk-alt+))
                    (or (is (tesc "[1;3C")) (and (not kitty) (is (tesc "F"))) (is (tesc "f")) (kit code +tk-alt+)))
                   ((and (string= key "left") (= modifier +tk-ctrl+)) (or (is (tesc "[1;5D")) (tk-legacy-modifier data "left" +tk-ctrl+) (kit code +tk-ctrl+)))
                   ((and (string= key "right") (= modifier +tk-ctrl+)) (or (is (tesc "[1;5C")) (tk-legacy-modifier data "right" +tk-ctrl+) (kit code +tk-ctrl+)))
                   ((= modifier 0) (or (tk-legacy data key *tk-legacy*) (kit code 0)))
                   (t (or (tk-legacy-modifier data key modifier) (kit code modifier))))))
          ((and (member key '("f1" "f2" "f3" "f4" "f5" "f6" "f7" "f8" "f9" "f10" "f11" "f12") :test #'string=))
           (and (= modifier 0) (tk-legacy data key *tk-legacy*)))
          ((and (= (length key) 1) (or (char<= #\a (char key 0) #\z) (char<= #\0 (char key 0) #\9) (find (char key 0) *tk-symbol-keys*)))
           (let* ((char (char key 0)) (codepoint (char-code char)) (raw-ctrl (tk-raw-ctrl-char key)) (letter (char<= #\a char #\z)))
             (when (and (= modifier (+ +tk-ctrl+ +tk-alt+)) (not kitty) raw-ctrl (string= data (tui-cat (tstr 27) raw-ctrl)))
               (return-from tui-matches-key t))
             (when (and (= modifier +tk-alt+) (not kitty) (string= data (tui-cat (tstr 27) key)))
               (return-from tui-matches-key t))
             (cond ((= modifier +tk-ctrl+)
                    (or (and raw-ctrl (string= data raw-ctrl)) (kit codepoint +tk-ctrl+) (tk-matches-printable-modify-other data codepoint +tk-ctrl+)))
                   ((= modifier (+ +tk-shift+ +tk-ctrl+))
                    (or (kit codepoint modifier) (tk-matches-printable-modify-other data codepoint modifier)))
                   ((= modifier +tk-shift+)
                    (or (and letter (string= data (string (char-upcase char)))) (kit codepoint +tk-shift+) (tk-matches-printable-modify-other data codepoint +tk-shift+)))
                   ((/= modifier 0) (or (kit codepoint modifier) (tk-matches-printable-modify-other data codepoint modifier)))
                   (t (or (string= data key) (kit codepoint 0) (tk-matches-printable-modify-other data codepoint 0))))))
          (t nil))))))

(defun tk-any-marker (data markers)
  (and (not (search (tesc "[200~") data)) (some (lambda (m) (search m data)) markers) t))

(defun tui-key-release-p (data)
  "Whether DATA is a Kitty key-release event (pi-tui's isKeyRelease)."
  (tk-any-marker data '(":3u" ":3~" ":3A" ":3B" ":3C" ":3D" ":3H" ":3F")))

(defun tui-key-repeat-p (data)
  "Whether DATA is a Kitty key-repeat event (pi-tui's isKeyRepeat)."
  (tk-any-marker data '(":2u" ":2~" ":2A" ":2B" ":2C" ":2D" ":2H" ":2F")))

(defun tk-format-key-name (key-name modifier)
  (let ((effective (logandc2 (tk-i32 modifier) +tk-lock-mask+)))
    (unless (logtest effective (lognot (logior +tk-shift+ +tk-ctrl+ +tk-alt+ +tk-super+)))
      (format nil "~{~A+~}~A"
              (append (and (logtest effective +tk-shift+) '("shift")) (and (logtest effective +tk-ctrl+) '("ctrl"))
                      (and (logtest effective +tk-alt+) '("alt")) (and (logtest effective +tk-super+) '("super")))
              key-name))))

(defun tk-format-parsed-key (codepoint modifier &optional base-layout-key)
  (let* ((normalized (tk-normalize-functional codepoint)) (identity (tk-normalize-shifted-letter normalized modifier))
         (known (or (tk-between 97 identity 122) (tk-between 48 identity 57) (tk-in identity *tk-symbol-keys*)))
         (effective (if known identity (or base-layout-key identity)))
         (name (or (and (integerp effective)
                        (cdr (assoc effective `((,+tk-escape+ . "escape") (,+tk-tab+ . "tab") (,+tk-enter+ . "enter") (,+tk-kp-enter+ . "enter")
                                                (,+tk-space+ . "space") (,+tk-backspace+ . "backspace") (,+tk-delete+ . "delete")
                                                (,+tk-insert+ . "insert") (,+tk-home+ . "home") (,+tk-end+ . "end") (,+tk-page-up+ . "pageUp")
                                                (,+tk-page-down+ . "pageDown") (,+tk-up+ . "up") (,+tk-down+ . "down") (,+tk-left+ . "left")
                                                (,+tk-right+ . "right")))))
                   (and (or (tk-between 48 effective 57) (tk-between 97 effective 122) (tk-in effective *tk-symbol-keys*))
                        (string (code-char (logand (tk-i32 effective) #xFFFF)))))))
    (and name (plusp (length name)) (tk-format-key-name name modifier))))

(defun tui-parse-key (data)
  "The key identifier raw terminal DATA stands for, or NIL (pi-tui's parseKey)."
  (let ((data (tui-simple data)) (active *tui-kitty-protocol-active*))
    (multiple-value-bind (codepoint shifted base modifier) (tk-parse-kitty data)
      (when codepoint
        (multiple-value-bind (logical-code logical-mod) (tk-logical-shifted-symbol shifted modifier *tk-symbol-keys*)
          (return-from tui-parse-key
            (tk-format-parsed-key (if logical-code logical-code codepoint) (if logical-code logical-mod modifier) base)))))
    (multiple-value-bind (code modifier) (tk-parse-modify-other data)
      (when code
        (multiple-value-bind (logical-code logical-mod) (tk-logical-shifted-symbol code modifier *tk-shifted-symbol-keys*)
          (return-from tui-parse-key (tk-format-parsed-key (if logical-code logical-code code) (if logical-code logical-mod modifier))))))
    (flet ((is (&rest strings) (member data strings :test #'string=)))
      (cond
        ((and active (is (tescc 13) (tstr 10))) "shift+enter")
        ((gethash data *tk-legacy-ids*))
        ((is (tstr 27)) "escape")
        ((is (tstr 28)) "ctrl+\\")
        ((is (tstr 29)) "ctrl+]")
        ((is (tstr 31)) "ctrl+-")
        ((is (tescc 27)) "ctrl+alt+[")
        ((is (tescc 28)) "ctrl+alt+\\")
        ((is (tescc 29)) "ctrl+alt+]")
        ((is (tescc 31)) "ctrl+alt+-")
        ((is (tstr 9)) "tab")
        ((or (is (tstr 13)) (and (not active) (is (tstr 10))) (is (tesc "OM"))) "enter")
        ((is (tstr 0)) "ctrl+space")
        ((is " ") "space")
        ((is (tstr 127)) "backspace")
        ((is (tstr 8)) (if (tk-windows-terminal-session-p) "ctrl+backspace" "backspace"))
        ((is (tesc "[Z")) "shift+tab")
        ((and (not active) (is (tescc 13))) "alt+enter")
        ((and (not active) (is (tesc " "))) "alt+space")
        ((is (tescc 127) (tescc 8)) "alt+backspace")
        ((and (not active) (is (tesc "B"))) "alt+left")
        ((and (not active) (is (tesc "F"))) "alt+right")
        ((and (not active) (= (length data) 2) (char= (char data 0) +tui-esc+)
              (let* ((code (char-code (char data 1))) (key (char data 1)))
                (cond ((<= 1 code 26) (format nil "ctrl+alt+~C" (code-char (+ code 96))))
                      ((or (<= 97 code 122) (<= 48 code 57) (find key *tk-symbol-keys*)) (format nil "alt+~C" key))))))
        ((is (tesc "[A")) "up")
        ((is (tesc "[B")) "down")
        ((is (tesc "[C")) "right")
        ((is (tesc "[D")) "left")
        ((is (tesc "[H") (tesc "OH")) "home")
        ((is (tesc "[F") (tesc "OF")) "end")
        ((is (tesc "[3~")) "delete")
        ((is (tesc "[5~")) "pageUp")
        ((is (tesc "[6~")) "pageDown")
        ((= (length data) 1)
         (let ((code (char-code (char data 0))))
           (cond ((<= 1 code 26) (format nil "ctrl+~C" (code-char (+ code 96))))
                 ((<= 32 code 126) data))))))))

(defun tk-from-code-point (value)
  (and (integerp value) (<= 0 value #x10FFFF) (string (code-char value))))

(defun tui-decode-kitty-printable (data)
  "The character of a plain or shifted Kitty CSI-u key event, or NIL (pi-tui's decodeKittyPrintable)."
  (let ((g (tk-match (tui-simple data) "" '((#\: 0) (#\: 1) (#\; 1) (#\: 1)) "u")))
    (when g
      (let* ((codepoint (tk-num (first g))) (shifted (tk-group-num (second g)))
             (mod-value (if (fourth g) (tk-num (fourth g)) 1))
             (modifier (if (integerp mod-value) (tk-i32 (1- mod-value)) 0)))
        (unless (or (logtest modifier (lognot (logior +tk-shift+ +tk-lock-mask+))) (logtest modifier (logior +tk-alt+ +tk-ctrl+)))
          (let ((effective (tk-normalize-functional (if (and (logtest modifier +tk-shift+) shifted) shifted codepoint))))
            (and (integerp effective) (>= effective 32) (tk-from-code-point effective))))))))

(defun tui-decode-printable-key (data)
  "TUI-DECODE-KITTY-PRINTABLE, or the character of a modifyOtherKeys event (pi-tui's decodePrintableKey)."
  (or (tui-decode-kitty-printable data)
      (multiple-value-bind (code modifier) (tk-parse-modify-other (tui-simple data))
        (and code (not (logtest (tk-i32 modifier) (lognot (logior +tk-lock-mask+ +tk-shift+)))) (integerp code) (>= code 32)
             (tk-from-code-point code)))))
