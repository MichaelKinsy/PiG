;;;; SPDX-License-Identifier: MIT
;;;; The wire: 4-byte big-endian length, then one JSON envelope, over the host's
;;;; AF_UNIX stream socket (coding/extension/host/subprocess/protocol.go).
;;;; Unix-like systems use sb-bsd-sockets; Windows reaches the same AF_UNIX
;;;; socket through Winsock with sb-alien, as the Python SDK does with ctypes.

(in-package #:pig)

(defconstant +max-frame-size+ (* 128 1024 1024)
  "Must match MaxFrameSize in the host's protocol.go and the other SDKs.")

(defstruct (conn (:constructor %make-conn))
  (impl nil)
  (in nil)
  (out nil)
  (write-lock (sb-thread:make-mutex :name "pig wire write"))
  (closed nil))

#-win32
(defun socket-conn (socket)
  "A CONN over a connected sb-bsd-sockets socket."
  ;; One bidirectional stream: sb-bsd-sockets keeps a single stream per socket. Its input and output buffers
  ;; are independent, so the reader thread and the (mutex-serialised) writers do not contend.
  (let ((stream (sb-bsd-sockets:socket-make-stream socket :input t :output t :buffering :full
                                                          :element-type '(unsigned-byte 8))))
    (%make-conn :impl socket :in stream :out stream)))

#-win32
(defun connect-unix (path)
  (let ((socket (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (handler-case (sb-bsd-sockets:socket-connect socket path)
      (error (e) (ignore-errors (sb-bsd-sockets:socket-close socket)) (error e)))
    (socket-conn socket)))

#-win32
(defun conn-read-exact (conn buffer)
  "Fill BUFFER; the number of bytes read (short only at end of file)."
  (read-sequence buffer (conn-in conn)))

#-win32
(defun conn-write-all (conn buffer)
  (write-sequence buffer (conn-out conn))
  (finish-output (conn-out conn)))

#-win32
(defun conn-shutdown (conn)
  "Wake a thread blocked reading CONN: shut both directions down."
  (let ((fd (ignore-errors (sb-bsd-sockets:socket-file-descriptor (conn-impl conn)))))
    (when fd
      (sb-alien:alien-funcall
       (sb-alien:extern-alien "shutdown" (function sb-alien:int sb-alien:int sb-alien:int))
       fd 2))))

#-win32
(defun conn-close (conn)
  (unless (conn-closed conn)
    (setf (conn-closed conn) t)
    (ignore-errors (sb-bsd-sockets:socket-close (conn-impl conn)))))

#+win32
(progn
  (sb-alien:load-shared-object "ws2_32.dll")
  (defconstant +winsock-af-unix+ 1)
  (defconstant +winsock-unix-path-max+ 108)
  (defconstant +winsock-sock-stream+ 1)
  (defconstant +winsock-invalid-socket+ (1- (expt 2 64)))

  (defun winsock-error (what)
    (let ((code (sb-alien:alien-funcall
                 (sb-alien:extern-alien "WSAGetLastError" (function sb-alien:int)))))
      (error 'host-call-error :message (format nil "~A failed: Winsock error ~D" what code))))

  (defun connect-unix (path)
    (let ((bytes (sb-ext:string-to-octets path :external-format :utf-8)))
      (when (>= (length bytes) +winsock-unix-path-max+)
        (error 'host-call-error
               :message (format nil "socket path is ~D bytes; AF_UNIX allows at most ~D: ~A"
                                (length bytes) (1- +winsock-unix-path-max+) path)))
      ;; sb-bsd-sockets has run WSAStartup when it loaded.
      (let ((handle (sb-alien:alien-funcall
                     (sb-alien:extern-alien "socket" (function sb-alien:unsigned-long sb-alien:int sb-alien:int sb-alien:int))
                     +winsock-af-unix+ +winsock-sock-stream+ 0)))
        (when (= handle +winsock-invalid-socket+) (winsock-error "socket"))
        (let ((address (make-array (+ 2 +winsock-unix-path-max+) :element-type '(unsigned-byte 8) :initial-element 0)))
          (setf (aref address 0) +winsock-af-unix+)
          (replace address bytes :start1 2)
          (sb-sys:with-pinned-objects (address)
            (unless (zerop (sb-alien:alien-funcall
                            (sb-alien:extern-alien "connect" (function sb-alien:int sb-alien:unsigned-long sb-sys:system-area-pointer sb-alien:int))
                            handle (sb-sys:vector-sap address) (length address)))
              (let ((e (winsock-error "connect"))) e))))
        (%make-conn :impl handle))))

  (defun conn-read-exact (conn buffer)
    (let ((got 0) (want (length buffer)))
      (sb-sys:with-pinned-objects (buffer)
        (loop while (< got want)
              do (let ((n (sb-alien:alien-funcall
                           (sb-alien:extern-alien "recv" (function sb-alien:int sb-alien:unsigned-long sb-sys:system-area-pointer sb-alien:int sb-alien:int))
                           (conn-impl conn) (sb-sys:sap+ (sb-sys:vector-sap buffer) got) (min (- want got) #x100000) 0)))
                   (cond ((plusp n) (incf got n))
                         ((zerop n) (return))
                         (t (if (conn-closed conn) (return) (winsock-error "recv")))))))
      got))

  (defun conn-write-all (conn buffer)
    (let ((sent 0) (total (length buffer)))
      (sb-sys:with-pinned-objects (buffer)
        (loop while (< sent total)
              do (let ((n (sb-alien:alien-funcall
                           (sb-alien:extern-alien "send" (function sb-alien:int sb-alien:unsigned-long sb-sys:system-area-pointer sb-alien:int sb-alien:int))
                           (conn-impl conn) (sb-sys:sap+ (sb-sys:vector-sap buffer) sent) (min (- total sent) #x100000) 0)))
                   (if (plusp n) (incf sent n) (winsock-error "send")))))))

  (defun conn-shutdown (conn)
    (sb-alien:alien-funcall
     (sb-alien:extern-alien "shutdown" (function sb-alien:int sb-alien:unsigned-long sb-alien:int))
     (conn-impl conn) 2))

  (defun conn-close (conn)
    (unless (conn-closed conn)
      (setf (conn-closed conn) t)
      (sb-alien:alien-funcall
       (sb-alien:extern-alien "closesocket" (function sb-alien:int sb-alien:unsigned-long))
       (conn-impl conn)))))

(defun frame-octets (envelope)
  "The header and body of ENVELOPE in one vector, so a frame is one write."
  (let* ((octets (let ((*encode-limit* (+ +max-frame-size+ 4))) (encode-json envelope :reserve 4)))
         (size (- (length octets) 4)))
    (when (> size +max-frame-size+)
      (error 'host-call-error :message (format nil "frame too large: ~D bytes exceeds ~D" size +max-frame-size+)))
    (setf (aref octets 0) (ldb (byte 8 24) size) (aref octets 1) (ldb (byte 8 16) size)
          (aref octets 2) (ldb (byte 8 8) size) (aref octets 3) (ldb (byte 8 0) size))
    octets))

(defun send-frame (conn envelope)
  "Write ENVELOPE as one frame. Frames of different threads never interleave."
  (let ((octets (frame-octets envelope)))
    (sb-thread:with-mutex ((conn-write-lock conn))
      (conn-write-all conn octets))))

(defun send-frame-locked (conn envelope)
  "SEND-FRAME for a caller that already holds the connection's write lock."
  (conn-write-all conn (frame-octets envelope)))

(defun read-frame (conn)
  "The next envelope, or :EOF when the host closed the connection."
  (let ((header (make-array 4 :element-type '(unsigned-byte 8))))
    (let ((got (conn-read-exact conn header)))
      (when (zerop got) (return-from read-frame :eof))
      (when (< got 4) (return-from read-frame :eof)))
    (let ((size (logior (ash (aref header 0) 24) (ash (aref header 1) 16) (ash (aref header 2) 8) (aref header 3))))
      (when (> size +max-frame-size+)
        (error 'host-call-error :message (format nil "frame too large: ~D" size)))
      (let ((body (make-array size :element-type '(unsigned-byte 8))))
        (when (< (conn-read-exact conn body) size) (return-from read-frame :eof))
        (decode-json body)))))
