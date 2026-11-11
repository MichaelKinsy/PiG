;;;; SPDX-License-Identifier: MIT
;;;; The host's replicated pi-tui state: its keybinding table, terminal capabilities and theme palette, published with every state snapshot.
;;;; The pi-tui helpers (tui-get-keybindings, tui-get-capabilities, key-hint, ...) take what is pending when first used and on every change.
;;;; The host has one theme, so extensions sharing a process share it; init-theme installs another until the host's theme changes.

(in-package #:pig)

(defvar *ts-lock* (sb-thread:make-mutex :name "tui state"))
(defvar *ts-keybindings* nil)
(defvar *ts-keybindings-dirty* nil)
(defvar *ts-capabilities* nil)
(defvar *ts-capabilities-dirty* nil)
(defvar *ts-host-palette* nil)
(defvar *ts-installed-palette* nil)
(defvar *ts-theme-cache* nil "(palette . theme) of the theme built last.")

(defun ts-json-equal (a b)
  (cond ((and (hash-table-p a) (hash-table-p b))
         (and (= (hash-table-count a) (hash-table-count b))
              (loop for key being the hash-keys of a using (hash-value value)
                    always (multiple-value-bind (other found) (gethash key b) (and found (ts-json-equal value other))))))
        ((and (vectorp a) (vectorp b) (not (stringp a)) (not (stringp b)))
         (and (= (length a) (length b)) (every #'ts-json-equal a b)))
        ((and (stringp a) (stringp b)) (string= a b))
        ((and (realp a) (realp b)) (= a b))
        (t (eql a b))))

(defun ts-set-host-theme (palette)
  "Record the palette the host published. One that differs from the last ends what init-theme installed; the same one, which every snapshot
repeats, leaves it. A value that is not an object is the empty palette."
  (let ((palette (if (hash-table-p palette) palette (obj))))
    (sb-thread:with-mutex (*ts-lock*)
      (when (or (null *ts-host-palette*) (not (ts-json-equal *ts-host-palette* palette)))
        (when *ts-host-palette* (setf *ts-installed-palette* nil))
        (setf *ts-host-palette* palette)))))

(defun ts-apply-host-state (state)
  "Record the snapshot's keybinding table, terminal capabilities and theme."
  (let ((table (jget state "keybindings")) (caps (jget state "terminalCapabilities")) (palette (jget state "theme")))
    (when (hash-table-p palette) (ts-set-host-theme palette))
    (sb-thread:with-mutex (*ts-lock*)
      (when (and (hash-table-p table) (hash-table-p (jget table "definitions")) (not (and *ts-keybindings* (ts-json-equal table *ts-keybindings*))))
        (setf *ts-keybindings* table *ts-keybindings-dirty* t))
      (when (hash-table-p caps) (setf *ts-capabilities* caps *ts-capabilities-dirty* t)))))

(def-state-hook "tui" (ext state) (ts-apply-host-state state))

(defun ts-take-keybindings ()
  "The table published since the last call, or NIL."
  (sb-thread:with-mutex (*ts-lock*)
    (when *ts-keybindings-dirty* (setf *ts-keybindings-dirty* nil) *ts-keybindings*)))

(defun ts-take-capabilities ()
  "The capabilities published since the last call, or NIL."
  (sb-thread:with-mutex (*ts-lock*)
    (when *ts-capabilities-dirty* (setf *ts-capabilities-dirty* nil) *ts-capabilities*)))

(defun ts-install-theme (palette)
  (sb-thread:with-mutex (*ts-lock*) (setf *ts-installed-palette* palette)))

(defun ts-installed-theme-palette ()
  "The palette of the theme init-theme installed, NIL while the global theme is the host's."
  (sb-thread:with-mutex (*ts-lock*) *ts-installed-palette*))

(defun ts-current-theme ()
  "The global theme: the one init-theme installed, else the theme the host last published, or one with the empty palette (modifiers only)."
  (let ((palette (sb-thread:with-mutex (*ts-lock*) (or *ts-installed-palette* *ts-host-palette*))))
    (let ((cache (sb-thread:with-mutex (*ts-lock*) *ts-theme-cache*)))
      (if (and cache (eq (car cache) palette))
          (cdr cache)
          (let ((theme (make-theme-from-palette (or palette (obj)))))
            (sb-thread:with-mutex (*ts-lock*) (setf *ts-theme-cache* (cons palette theme)))
            theme)))))
