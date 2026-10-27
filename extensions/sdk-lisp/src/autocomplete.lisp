;;;; SPDX-License-Identifier: MIT
;;;; ctx-add-autocomplete-provider: autocomplete provider factories over the host's contract (Pi's addAutocompleteProvider).
;;;; A factory receives the provider the host's chain holds so far and returns the provider that wraps it; the host retains the result.

(in-package #:pig)

(defstruct (autocomplete-provider
            (:constructor make-autocomplete-provider
                (&key trigger-characters ((:get-suggestions get-suggestions-fn)) ((:apply-completion apply-completion-fn))
                   ((:should-trigger-file-completion trigger-fn)))))
  "A provider: TRIGGER-CHARACTERS (a list of strings), GET-SUGGESTIONS (lambda (ctx lines cursor-line cursor-col force)), APPLY-COMPLETION
(lambda (ctx lines cursor-line cursor-col item prefix)) and optionally SHOULD-TRIGGER-FILE-COMPLETION (lambda (ctx lines cursor-line cursor-col)).
LINES is a vector of strings; cursor columns are UTF-16 code units; suggestions and completions are the JSON objects of Pi's shapes."
  trigger-characters get-suggestions-fn apply-completion-fn trigger-fn)

(defun autocomplete-get-suggestions (provider ctx lines cursor-line cursor-col &optional force)
  "PROVIDER's suggestions at the cursor: a JSON object with items and prefix, or NIL."
  (funcall (autocomplete-provider-get-suggestions-fn provider) ctx lines cursor-line cursor-col force))

(defun autocomplete-apply-completion (provider ctx lines cursor-line cursor-col item prefix)
  "The JSON object (lines, cursorLine, cursorCol) of applying ITEM in place of PREFIX."
  (funcall (autocomplete-provider-apply-completion-fn provider) ctx lines cursor-line cursor-col item prefix))

(defun autocomplete-should-trigger-file-completion (provider ctx lines cursor-line cursor-col)
  "Whether file completion should open here; true for a provider with no opinion."
  (let ((fn (autocomplete-provider-trigger-fn provider)))
    (if fn (and (funcall fn ctx lines cursor-line cursor-col) t) t)))

;;; ---------------------------------------------------------------- the provider the host's chain holds

(defvar *acomp-random* (make-random-state t))
(defvar *acomp-random-lock* (sb-thread:make-mutex :name "pig autocomplete ids"))

(defun acomp-new-id ()
  (sb-thread:with-mutex (*acomp-random-lock*) (format nil "~(~32,'0x~)" (random (expt 2 128) *acomp-random*))))

(defun acomp-release (weak id)
  "Tell the host the extension dropped the provider ID. Runs on the finalizer thread, which holds no SDK lock."
  (let ((ext (sb-ext:weak-pointer-value weak)))
    (when (and ext (not (extension-shutdown ext)))
      (ignore-errors (notify-host ext "ui.autocomplete.release" (obj "id" id))))))

(defun acomp-invoke (ctx id operation lines cursor-line cursor-col &rest members)
  (when (ctx-cancelled-p ctx) (error "autocomplete cancelled"))
  (let ((args (obj "id" id "operation" operation "lines" lines "cursorLine" cursor-line "cursorCol" cursor-col)))
    (loop for (key value) on members by #'cddr do (setf (gethash key args) value))
    (ctx-call-host ctx "ui.autocomplete.invoke" args)))

(defun acomp-current (ctx descriptor)
  "The provider the host holds under DESCRIPTOR, called through the host; the host drops it once the extension does."
  (let* ((id (jget descriptor "id"))
         (triggers (jget descriptor "triggerCharacters"))
         (provider (make-autocomplete-provider
                    :trigger-characters (if (vectorp triggers) (coerce triggers 'list) '())
                    :get-suggestions (lambda (ctx lines line col force)
                                       (acomp-invoke ctx id "getSuggestions" lines line col "force" (if force t :false)))
                    :apply-completion (lambda (ctx lines line col item prefix)
                                        (acomp-invoke ctx id "applyCompletion" lines line col "item" item "prefix" prefix))
                    :should-trigger-file-completion
                    (and (eq (jget descriptor "hasFileTrigger") t)
                         (lambda (ctx lines line col)
                           (eq (acomp-invoke ctx id "shouldTriggerFileCompletion" lines line col) t))))))
    (let ((weak (sb-ext:make-weak-pointer (ctx-extension ctx))))
      (sb-ext:finalize provider (lambda () (acomp-release weak id)) :dont-save t))
    provider))

;;; ---------------------------------------------------------------- the registry

(defstruct (acomp-registry (:constructor make-acomp-registry ()))
  (lock (sb-thread:make-mutex :name "pig autocomplete"))
  (factories (make-hash-table :test 'equal)) (invoked (make-hash-table :test 'equal)) (providers (make-hash-table :test 'equal)))

(defun acomp-registry-of (ext) (extension-extra ext :autocomplete #'make-acomp-registry))

(defun ctx-add-autocomplete-provider (ctx &optional factory)
  "Append FACTORY, (lambda (ctx current)) returning an AUTOCOMPLETE-PROVIDER that wraps CURRENT, and wait for the host's rebuilt provider chain.
Without a UI it does nothing."
  (when (ctx-has-ui-p ctx)
    (unless (functionp factory) (error "autocomplete factory is missing"))
    (let* ((registry (acomp-registry-of (ctx-extension ctx))) (key (acomp-new-id)))
      (sb-thread:with-mutex ((acomp-registry-lock registry)) (setf (gethash key (acomp-registry-factories registry)) factory))
      (unwind-protect (ctx-call-host ctx "ui.addAutocompleteProvider" (obj "factoryId" key))
        (sb-thread:with-mutex ((acomp-registry-lock registry))
          (unless (gethash key (acomp-registry-invoked registry)) (remhash key (acomp-registry-factories registry)))))))
  nil)

(defun acomp-wrap (registry ctx args)
  (let ((factory (sb-thread:with-mutex ((acomp-registry-lock registry))
                   (let ((key (jget args "factoryId")))
                     (setf (gethash key (acomp-registry-invoked registry)) t)
                     (or (gethash key (acomp-registry-factories registry)) (error "unknown autocomplete factory ~S" key))))))
    (let ((provider (funcall factory ctx (acomp-current ctx (jget args "current")))) (id (acomp-new-id)))
      (unless (and (autocomplete-provider-p provider) (autocomplete-provider-get-suggestions-fn provider)
                   (autocomplete-provider-apply-completion-fn provider))
        (error "autocomplete factory must return a provider"))
      (sb-thread:with-mutex ((acomp-registry-lock registry)) (setf (gethash id (acomp-registry-providers registry)) provider))
      (obj "id" id "triggerCharacters" (coerce (autocomplete-provider-trigger-characters provider) 'simple-vector)
           "hasFileTrigger" (if (autocomplete-provider-trigger-fn provider) t :false)))))

(defun acomp-dispatch (ext ctx args)
  (let ((registry (acomp-registry-of ext)) (operation (jget args "operation")))
    (if (equal operation "wrap")
        (acomp-wrap registry ctx args)
        (let ((provider (sb-thread:with-mutex ((acomp-registry-lock registry))
                          (or (gethash (jget args "id") (acomp-registry-providers registry))
                              (error "unknown autocomplete provider ~S" (jget args "id")))))
              (lines (jget args "lines")) (line (jget args "cursorLine")) (col (jget args "cursorCol")))
          (cond ((equal operation "getSuggestions")
                 (autocomplete-get-suggestions provider ctx lines line col (eq (jget args "force") t)))
                ((equal operation "applyCompletion")
                 (autocomplete-apply-completion provider ctx lines line col (jget args "item") (jget args "prefix")))
                ((equal operation "shouldTriggerFileCompletion")
                 (if (autocomplete-should-trigger-file-completion provider ctx lines line col) t :false))
                (t (error "unknown autocomplete operation: ~A" operation)))))))

(defun acomp-request-args (req) (let ((args (jget req "args"))) (if (hash-table-p args) args (obj))))

(def-request "autocomplete.sync" (ext ctx req) (acomp-dispatch ext ctx (acomp-request-args req)))
(def-request "autocomplete.suggest" (ext ctx req) (acomp-dispatch ext ctx (acomp-request-args req)))
(def-notify "autocomplete.release" (ext args)
  (let ((registry (acomp-registry-of ext)))
    (sb-thread:with-mutex ((acomp-registry-lock registry)) (remhash (jget args "id") (acomp-registry-providers registry)))))

(def-stop-hook "autocomplete" (ext)
  (let ((registry (acomp-registry-of ext)))
    (sb-thread:with-mutex ((acomp-registry-lock registry))
      (clrhash (acomp-registry-factories registry)) (clrhash (acomp-registry-invoked registry)) (clrhash (acomp-registry-providers registry)))))
