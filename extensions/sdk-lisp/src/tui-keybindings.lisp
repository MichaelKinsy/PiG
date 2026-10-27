;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's keybindings.ts: the effective keys of every keybinding. A KeybindingsManager resolves each keybinding id to the user's override or the
;;;; definition's default keys, matches raw terminal input against them and reports conflicts between user overrides. The host's effective table
;;;; (every tui.* and app.* definition with the user's keybindings.json overrides) is installed as the global manager when the host publishes it.

(in-package #:pig)

(defstruct (tui-keybinding-definition (:constructor make-tui-keybinding-definition (default-keys &optional description)))
  "pi-tui's KeybindingDefinition: the default keys (one key identifier or a vector of them) and the description."
  default-keys description)

(defstruct (tui-keybinding-conflict (:constructor tkb-conflict (key keybindings)))
  "pi-tui's KeybindingConflict: a key claimed by more than one keybinding (a list of keybinding ids)."
  key keybindings)

(defparameter *tui-keybindings*
  (let ((table (make-hash-table :test 'equal)))
    (loop for (id keys description) in *tui-keybinding-table*
          do (setf (gethash id table) (make-tui-keybinding-definition (coerce keys 'simple-vector) description)))
    table)
  "pi-tui's TUI_KEYBINDINGS: keybinding id -> tui-keybinding-definition.")

(defun tkb-normalize-keys (keys)
  "KEYS (NIL, null, one key or a sequence of keys) as a list of the distinct keys, in order."
  (remove-duplicates (cond ((or (null keys) (eq keys :null)) '()) ((stringp keys) (list keys)) (t (coerce keys 'list)))
                     :test #'string= :from-end t))

(defun tkb-definition (value)
  (if (tui-keybinding-definition-p value)
      value
      (let ((description (jget value "description")))
        (make-tui-keybinding-definition (jget value "defaultKeys" (vector)) (and (stringp description) description)))))

(defstruct (tui-keybindings-manager (:conc-name tkm-) (:constructor tkm-new))
  "pi-tui's KeybindingsManager."
  (definitions (make-hash-table :test 'equal)) (user-bindings (make-hash-table :test 'equal)) (keys-by-id (make-hash-table :test 'equal)) (conflicts '()))

(defun tkm-rebuild (manager)
  (let ((keys-by-id (make-hash-table :test 'equal)) (claims '()) (conflicts '()) (definitions (tkm-definitions manager)))
    (loop for keybinding being the hash-keys of (tkm-user-bindings manager) using (hash-value keys)
          do (when (nth-value 1 (gethash keybinding definitions))
               (dolist (key (tkb-normalize-keys keys))
                 (let ((entry (or (assoc key claims :test #'string=) (car (push (list key) claims)))))
                   (unless (member keybinding (cdr entry) :test #'string=) (setf (cdr entry) (append (cdr entry) (list keybinding))))))))
    (dolist (entry (reverse claims))
      (when (> (length (cdr entry)) 1) (push (tkb-conflict (car entry) (copy-list (cdr entry))) conflicts)))
    (loop for id being the hash-keys of definitions using (hash-value definition)
          do (let ((user (gethash id (tkm-user-bindings manager))))
               (setf (gethash id keys-by-id)
                     (tkb-normalize-keys (if (or (null user) (eq user :null)) (tui-keybinding-definition-default-keys definition) user)))))
    (setf (tkm-keys-by-id manager) keys-by-id (tkm-conflicts manager) (nreverse conflicts))
    manager))

(defun tkb-copy-bindings (bindings)
  (let ((table (make-hash-table :test 'equal)))
    (when (hash-table-p bindings) (maphash (lambda (k v) (setf (gethash k table) v)) bindings))
    table))

(defun tui-make-keybindings-manager (definitions &optional user-bindings)
  "A manager over DEFINITIONS (keybinding id -> tui-keybinding-definition, or an object {\"defaultKeys\", \"description\"}) with the USER-BINDINGS
(id -> a key or a vector of keys) overriding their defaults."
  (let ((manager (tkm-new)))
    (maphash (lambda (id value) (setf (gethash id (tkm-definitions manager)) (tkb-definition value))) definitions)
    (setf (tkm-user-bindings manager) (tkb-copy-bindings user-bindings))
    (tkm-rebuild manager)))

(defun tui-keybindings-from-wire (table)
  "The manager of the host's published table: {\"definitions\": {...}, \"userBindings\": {...}}."
  (tui-make-keybindings-manager (or (jget table "definitions") (obj)) (jget table "userBindings")))

(defun tui-keybindings-matches (manager data keybinding)
  "Whether the raw terminal DATA is one of the keys of KEYBINDING."
  (and (some (lambda (key) (tui-matches-key data key)) (gethash keybinding (tkm-keys-by-id manager))) t))

(defun tui-keybindings-get-keys (manager keybinding)
  "The keys of KEYBINDING as a vector of key identifiers."
  (coerce (gethash keybinding (tkm-keys-by-id manager)) 'simple-vector))

(defun tui-keybindings-get-definition (manager keybinding)
  "The tui-keybinding-definition of KEYBINDING, or NIL."
  (values (gethash keybinding (tkm-definitions manager))))

(defun tui-keybindings-get-conflicts (manager)
  "The keys claimed by more than one keybinding's user override, as a list of tui-keybinding-conflict."
  (mapcar (lambda (c) (tkb-conflict (tui-keybinding-conflict-key c) (copy-list (tui-keybinding-conflict-keybindings c)))) (tkm-conflicts manager)))

(defun tui-keybindings-set-user-bindings (manager user-bindings)
  "Replace the user's overrides (id -> key or vector of keys)."
  (setf (tkm-user-bindings manager) (tkb-copy-bindings user-bindings))
  (tkm-rebuild manager)
  nil)

(defun tui-keybindings-get-user-bindings (manager)
  "A copy of the user's overrides."
  (tkb-copy-bindings (tkm-user-bindings manager)))

(defun tui-keybindings-get-resolved-bindings (manager)
  "Every id to its resolved keys: a lone key as a string, several (or none) as a vector, as Pi gives them."
  (let ((resolved (make-hash-table :test 'equal)))
    (loop for id being the hash-keys of (tkm-definitions manager)
          do (let ((keys (gethash id (tkm-keys-by-id manager))))
               (setf (gethash id resolved) (if (= (length keys) 1) (first keys) (coerce keys 'simple-vector)))))
    resolved))

(defvar *tui-global-keybindings* nil)

(defun tui-set-keybindings (manager)
  "Install the global manager (pi-tui's setKeybindings)."
  (ts-take-keybindings)
  (setf *tui-global-keybindings* manager)
  nil)

(defun tui-get-keybindings ()
  "The global manager, over *tui-keybindings* until one is installed; the host's published table replaces it (pi-tui's getKeybindings)."
  (let ((published (ts-take-keybindings)))
    (when published (setf *tui-global-keybindings* (tui-keybindings-from-wire published)))
    (or *tui-global-keybindings* (setf *tui-global-keybindings* (tui-make-keybindings-manager *tui-keybindings*)))))
