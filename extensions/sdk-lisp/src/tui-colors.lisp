;;;; SPDX-License-Identifier: MIT
;;;; pi-tui's colors.ts and oklab.ts: colors, OKLCH/OKHSL conversion, mixing and ANSI styling. A port of Pi's functions (Python SDK:
;;;; tui/_colors.py). A color is a JSON object: {"kind":"indexed","index":n}, {"kind":"rgb","r","g","b"} or {"kind":"oklch","l","c","h"}.
;;;; JavaScript semantics the numbers depend on are kept: division by zero is an infinity or NaN, Math.round rounds halves toward +Infinity,
;;;; % is fmod, and a number prints in its shortest form. Arithmetic runs with the floating point traps masked.

(in-package #:pig)

(define-condition tui-error (pig-error)
  ((message :initarg :message :reader tui-error-message))
  (:report (lambda (c s) (write-string (tui-error-message c) s)))
  (:documentation "A pi-tui helper rejected its argument (Pi throws an Error; the Python SDK raises ValueError)."))

(defun tc-fail (format &rest arguments)
  (error 'tui-error :message (apply #'format nil format arguments)))

(defmacro with-tc-floats (&body body)
  `(sb-int:with-float-traps-masked (:overflow :invalid :divide-by-zero :inexact) ,@body))

(defmacro def-tc (name lambda-list &body body)
  "DEFUN whose body runs with the floating point traps masked."
  (let ((doc (and (stringp (first body)) (rest body) (list (pop body)))))
    `(defun ,name ,lambda-list ,@doc (with-tc-floats ,@body))))

(defconstant +tc-inf+ sb-ext:double-float-positive-infinity)
(defparameter +tc-nan+ (with-tc-floats (- sb-ext:double-float-positive-infinity sb-ext:double-float-positive-infinity)))

(defun tc-nan-p (x) (/= x x))
(defun tc-finite-p (x) (and (not (tc-nan-p x)) (< (abs x) +tc-inf+)))

(defun tc-f (value)
  "VALUE as a double; a JSON null reads as NaN."
  (cond ((realp value) (coerce value 'double-float))
        ((eq value :null) +tc-nan+)
        (t (tc-fail "expected a number, got ~S" value))))

(defun tc-str (value)
  "Number.prototype.toString of the number VALUE."
  (let ((x (tc-f value)))
    (cond ((tc-nan-p x) "NaN") ((= x +tc-inf+) "Infinity") ((= x (- +tc-inf+)) "-Infinity")
          ((zerop x) "0") (t (js-number-string x)))))

(defun tc-round (value)
  "Math.round."
  (cond ((or (tc-nan-p value) (not (tc-finite-p value)) (zerop value)) value)
        ((< 0 value 0.5d0) 0d0)
        ((<= -0.5d0 value -0d0) -0d0)
        (t (let ((floor (floor value))) (coerce (if (>= (- value floor) 0.5d0) (1+ floor) floor) 'double-float)))))

(defun tc-max (a b) (if (or (tc-nan-p a) (tc-nan-p b)) +tc-nan+ (if (> a b) a b)))
(defun tc-min (a b) (if (or (tc-nan-p a) (tc-nan-p b)) +tc-nan+ (if (< a b) a b)))
(defun tc-sqrt (value) (cond ((tc-nan-p value) value) ((>= value 0) (sqrt value)) (t +tc-nan+)))
(defun tc-pow (base exponent) (sb-kernel::%pow base exponent))

(defun tc-cbrt (value)
  (if (tc-finite-p value)
      (sb-alien:alien-funcall (sb-alien:extern-alien "cbrt" (function double-float double-float)) value)
      value))

(defun tc-fmod (a b)
  "JavaScript a % b."
  (if (tc-finite-p a) (coerce (rem (rational a) (rational b)) 'double-float) +tc-nan+))

(defun tc-hue (hue) (tc-fmod (+ (tc-fmod hue 360d0) 360d0) 360d0))

(defun tc-require-finite (value name)
  (unless (tc-finite-p value) (tc-fail "~A must be finite" name)))

;;; ------------------------------------------------------------------ constructors

(defun tc-color (kind &rest members) (apply #'obj "kind" kind members))

(def-tc tui-indexed-color (index)
  "An ANSI 256-color palette index as a color (pi-tui's indexedColor)."
  (let ((x (if (or (realp index) (eq index :null)) (tc-f index) +tc-nan+)))
    (unless (and (tc-finite-p x) (= x (ffloor x)) (<= 0 x 255))
      (tc-fail "ANSI color index must be an integer from 0 to 255: ~A" (tc-str x)))
    (tc-color "indexed" "index" (round x))))

(def-tc tui-rgb-color (r g b)
  "An sRGB color with channels 0-255 (pi-tui's rgbColor)."
  (loop for (name value) in `(("r" ,r) ("g" ,g) ("b" ,b))
        do (let ((x (tc-f value)))
             (tc-require-finite x name)
             (when (or (< x 0) (> x 255)) (tc-fail "~A must be between 0 and 255: ~A" name (tc-str x)))))
  (tc-color "rgb" "r" r "g" g "b" b))

(def-tc tui-oklch-color (l c h)
  "An OKLCH color (pi-tui's oklchColor): L 0-1, C not negative, H in degrees (normalized to 0-360)."
  (let ((lf (tc-f l)) (cf (tc-f c)) (hf (tc-f h)))
    (tc-require-finite lf "l") (tc-require-finite cf "c") (tc-require-finite hf "h")
    (when (or (< lf 0) (> lf 1)) (tc-fail "l must be between 0 and 1: ~A" (tc-str lf)))
    (when (< cf 0) (tc-fail "c must not be negative: ~A" (tc-str cf)))
    (tc-color "oklch" "l" l "c" c "h" (tc-hue hf))))

;;; ------------------------------------------------------------------ oklab.ts

(defparameter *tc-linear-to-lms*
  #(#(0.4122214694707629d0 0.5363325372617349d0 0.0514459932675022d0)
    #(0.2119034958178251d0 0.6806995506452344d0 0.1073969535369405d0)
    #(0.0883024591900564d0 0.2817188391361215d0 0.6299787016738222d0)))
(defparameter *tc-lms-to-lab*
  #(#(0.210454268309314d0 0.793617774702305d0 -0.0040720430116193d0)
    #(1.9779985324311684d0 -2.42859224204858d0 0.450593709617411d0)
    #(0.0259040424655478d0 0.7827717124575296d0 -0.8086757549230774d0)))
(defparameter *tc-lab-to-lms*
  #(#(1d0 0.3963377773761749d0 0.2158037573099136d0)
    #(1d0 -0.1055613458156586d0 -0.0638541728258133d0)
    #(1d0 -0.0894841775298119d0 -1.2914855480194092d0)))
(defparameter *tc-lms-to-linear*
  #(#(4.0767416360759583d0 -3.3077115392580629d0 0.2309699031821043d0)
    #(-1.2684379732850315d0 2.6097573492876882d0 -0.341319376002657d0)
    #(-0.0041960761386756d0 -0.7034186179359362d0 1.7076146940746117d0)))
(defparameter *tc-saturation-fit*
  #(#(-1.8817031d0 -0.80936501d0 1.19086277d0 1.76576728d0 0.59662641d0 0.75515197d0 0.56771245d0)
    #(1.8144408d0 -1.19445267d0 0.73956515d0 -0.45954404d0 0.08285427d0 0.12541073d0 -0.14503204d0)
    #(0.13110758d0 1.81333971d0 1.35733652d0 -0.00915799d0 -1.1513021d0 -0.50559606d0 0.00692167d0))
  "Per RGB channel: the x and y of the channel test, then the five saturation coefficients.")
(defconstant +tc-k1+ 0.206d0)
(defconstant +tc-k2+ 0.03d0)
(defparameter +tc-k3+ (/ (+ 1d0 0.206d0) (+ 1d0 0.03d0)))
(defconstant +tc-max-value+ 1.7976931348623157d308)

(defun tc-multiply (m x y z)
  (flet ((row (r) (+ (* (svref r 0) x) (* (svref r 1) y) (* (svref r 2) z))))
    (list (row (svref m 0)) (row (svref m 1)) (row (svref m 2)))))

(defun tc-oklab-to-okhsl-lightness (x)
  (* 0.5d0 (+ (- (* +tc-k3+ x) +tc-k1+) (tc-sqrt (+ (tc-pow (- (* +tc-k3+ x) +tc-k1+) 2d0) (* 4 +tc-k2+ +tc-k3+ x))))))

(defun tc-okhsl-to-oklab-lightness (x) (/ (+ (* x x) (* +tc-k1+ x)) (* +tc-k3+ (+ x +tc-k2+))))

(defun tc-linear-to-srgb (value)
  (if (> value 0.0031308d0) (- (* 1.055d0 (tc-pow value (/ 1d0 2.4d0))) 0.055d0) (* 12.92d0 value)))

(defun tc-srgb-to-linear (value)
  (if (<= value 0.04045d0) (/ value 12.92d0) (tc-pow (/ (+ value 0.055d0) 1.055d0) 2.4d0)))

(defun tc-oklab-to-linear (l a b)
  (destructuring-bind (x y z) (tc-multiply *tc-lab-to-lms* l a b)
    (tc-multiply *tc-lms-to-linear* (tc-pow x 3d0) (tc-pow y 3d0) (tc-pow z 3d0))))

(defun tc-rgb-to-oklab (r g b)
  (destructuring-bind (x y z) (tc-multiply *tc-linear-to-lms* (tc-srgb-to-linear (/ r 255d0)) (tc-srgb-to-linear (/ g 255d0)) (tc-srgb-to-linear (/ b 255d0)))
    (tc-multiply *tc-lms-to-lab* (tc-cbrt x) (tc-cbrt y) (tc-cbrt z))))

(defun tc-linear-to-rgb (linear)
  (mapcar (lambda (v) (tc-round (* (tc-min 1d0 (tc-max 0d0 (tc-linear-to-srgb v))) 255))) linear))

(defun tc-lms-slopes (a b)
  (loop for row across *tc-lab-to-lms* collect (+ (* (svref row 1) a) (* (svref row 2) b))))

(defun tc-max-saturation (a b)
  (let* ((channel (loop for index from 0 to 2
                        when (or (= index 2) (let ((fit (svref *tc-saturation-fit* index))) (> (+ (* (svref fit 0) a) (* (svref fit 1) b)) 1)))
                          return index))
         (fit (svref *tc-saturation-fit* channel)) (weights (svref *tc-lms-to-linear* channel))
         (saturation (+ (svref fit 2) (* (svref fit 3) a) (* (svref fit 4) b) (* (svref fit 5) a a) (* (svref fit 6) a b)))
         (slopes (tc-lms-slopes a b))
         (base (mapcar (lambda (k) (+ 1 (* saturation k))) slopes)))
    (flet ((dot (values) (let ((total 0)) (loop for i from 0 for v in values do (setf total (+ total (* (svref weights i) v)))) total)))
      (let ((f (dot (mapcar (lambda (v) (tc-pow v 3d0)) base)))
            (f1 (dot (loop for s in slopes for v in base collect (* 3 s (tc-pow v 2d0)))))
            (f2 (dot (loop for s in slopes for v in base collect (* 6 (tc-pow s 2d0) v)))))
        (- saturation (/ (* f f1) (- (* f1 f1) (* 0.5d0 f f2))))))))

(defun tc-cusp (a b)
  (let* ((saturation (tc-max-saturation a b)) (lms (tc-oklab-to-linear 1d0 (* saturation a) (* saturation b))) (peak (first lms)))
    (dolist (v (rest lms)) (setf peak (tc-max peak v)))
    (let ((lightness (tc-cbrt (/ 1d0 peak)))) (values lightness (* lightness saturation)))))

(defun tc-max-chroma (a b lightness cusp-l cusp-c)
  (if (<= lightness cusp-l)
      (/ (* cusp-c lightness) cusp-l)
      (let* ((tt (/ (* cusp-c (- lightness 1)) (- cusp-l 1))) (slopes (tc-lms-slopes a b))
             (lms (mapcar (lambda (k) (+ lightness (* tt k))) slopes))
             (cubes (mapcar (lambda (v) (tc-pow v 3d0)) lms))
             (first (loop for s in slopes for v in lms collect (* 3 s (tc-pow v 2d0))))
             (second (loop for s in slopes for v in lms collect (* 6 (tc-pow s 2d0) v)))
             (smallest nil))
        (loop for row across *tc-lms-to-linear*
              do (flet ((dot (v) (+ (* (svref row 0) (first v)) (* (svref row 1) (second v)) (* (svref row 2) (third v)))))
                   (let* ((f (- (dot cubes) 1)) (f1 (dot first)) (f2 (dot second))
                          (u (/ f1 (- (* f1 f1) (* 0.5d0 f f2))))
                          (step (if (>= u 0) (- (* f u)) +tc-max-value+)))
                     (setf smallest (if smallest (tc-min smallest step) step)))))
        (+ tt smallest))))

(defun tc-chroma-stops (lightness a b)
  "Values: C0, C-mid and C-max."
  (multiple-value-bind (cusp-l cusp-c) (tc-cusp a b)
    (let* ((c-max (tc-max-chroma a b lightness cusp-l cusp-c))
           (k (/ c-max (tc-min (* lightness (/ cusp-c cusp-l)) (* (- 1 lightness) (/ cusp-c (- 1 cusp-l))))))
           (mid-s (+ 0.11516993d0 (/ 1d0 (+ 7.4477897d0 (* 4.1590124d0 b)
                                           (* a (+ -2.19557347d0 (* 1.75198401d0 b)
                                                   (* a (+ -2.13704948d0 (- (* 10.02301043d0 b))
                                                           (* a (+ -4.24894561d0 (* 5.38770819d0 b) (* 4.69891013d0 a)))))))))))
           (mid-t (+ 0.11239642d0 (/ 1d0 (+ 1.6132032d0 (- (* 0.68124379d0 b))
                                           (* a (+ 0.40370612d0 (* 0.90148123d0 b)
                                                   (* a (+ -0.27087943d0 (* 0.6122399d0 b)
                                                           (* a (+ 0.00299215d0 (- (* 0.45399568d0 b)) (- (* 0.14661872d0 a))))))))))))
           (c-mid (* 0.9d0 k (tc-sqrt (tc-sqrt (/ 1d0 (+ (/ 1d0 (tc-pow (* lightness mid-s) 4d0)) (/ 1d0 (tc-pow (* (- 1 lightness) mid-t) 4d0))))))))
           (c0 (tc-sqrt (/ 1d0 (+ (/ 1d0 (tc-pow (* lightness 0.4d0) 2d0)) (/ 1d0 (tc-pow (* (- 1 lightness) 0.8d0) 2d0)))))))
      (values c0 c-mid c-max))))

(defun tc-okhsl-to-rgb (hue saturation lightness)
  (let* ((big-l (tc-okhsl-to-oklab-lightness lightness)) (lb 0d0) (lc 0d0))
    (when (and (< 0 big-l 1) (> saturation 0))
      (let* ((angle (/ (* 2 pi (tc-hue hue)) 360d0)) (a (cos angle)) (b (sin angle)))
        (multiple-value-bind (c0 c-mid c-max) (tc-chroma-stops big-l a b)
          (let ((chroma (if (< saturation 0.8d0)
                            (let* ((tt (* 1.25d0 saturation)) (k1 (* 0.8d0 c0)))
                              (/ (* tt k1) (- 1 (* (- 1 (/ k1 c-mid)) tt))))
                            (let* ((tt (* 5 (- saturation 0.8d0))) (k1 (/ (* 0.2d0 (tc-pow c-mid 2d0) (tc-pow 1.25d0 2d0)) c0)))
                              (+ c-mid (/ (* tt k1) (- 1 (* (- 1 (/ k1 (- c-max c-mid))) tt))))))))
            (setf lb (* chroma a) lc (* chroma b))))))
    (tc-linear-to-rgb (tc-oklab-to-linear big-l lb lc))))

(defun tc-rgb-to-okhsl (r g b)
  "Values: hue, saturation, lightness."
  (destructuring-bind (big-l lab-a lab-b) (tc-rgb-to-oklab r g b)
    (let ((chroma (sb-kernel::%hypot lab-a lab-b)) (lightness (tc-oklab-to-okhsl-lightness big-l)))
      (if (or (< chroma 1d-9) (<= lightness 0) (>= lightness 1))
          (values 0 0 lightness)
          (let ((hue (tc-fmod (+ (/ (* (atan lab-b lab-a) 180) pi) 360) 360d0)))
            (multiple-value-bind (c0 c-mid c-max) (tc-chroma-stops big-l (/ lab-a chroma) (/ lab-b chroma))
              (let ((saturation (if (< chroma c-mid)
                                    (let ((k1 (* 0.8d0 c0))) (* 0.8d0 (/ chroma (+ k1 (* (- 1 (/ k1 c-mid)) chroma)))))
                                    (let* ((k1 (/ (* 0.2d0 (tc-pow c-mid 2d0) (tc-pow 1.25d0 2d0)) c0)) (offset (- chroma c-mid)))
                                      (+ 0.8d0 (* 0.2d0 (/ offset (+ k1 (* (- 1 (/ k1 (- c-max c-mid))) offset)))))))))
                (values hue (tc-min 1d0 (tc-max 0d0 saturation)) lightness))))))))

;;; ------------------------------------------------------------------ colors.ts

(def-tc tui-okhsl-color (h s l)
  "An OKHSL color converted to sRGB (pi-tui's okhslColor): H in degrees, S and L 0-1."
  (let ((hf (tc-f h)) (sf (tc-f s)) (lf (tc-f l)))
    (tc-require-finite hf "h") (tc-require-finite sf "s") (tc-require-finite lf "l")
    (when (or (< sf 0) (> sf 1)) (tc-fail "s must be between 0 and 1: ~A" (tc-str sf)))
    (when (or (< lf 0) (> lf 1)) (tc-fail "l must be between 0 and 1: ~A" (tc-str lf)))
    (destructuring-bind (r g b) (tc-okhsl-to-rgb hf sf lf) (tui-rgb-color r g b))))

(defparameter *tc-basic-colors*
  #((0 0 0) (128 0 0) (0 128 0) (128 128 0) (0 0 128) (128 0 128) (0 128 128) (192 192 192)
    (128 128 128) (255 0 0) (0 255 0) (255 255 0) (0 0 255) (255 0 255) (0 255 255) (255 255 255)))
(defparameter *tc-cube-values* #(0 95 135 175 215 255))
(defparameter *tc-gray-values* (coerce (loop for i below 24 collect (+ 8 (* i 10))) 'simple-vector))

(defun tc-kind (color)
  (let ((kind (jget color "kind")))
    (if (member kind '("indexed" "rgb" "oklch") :test #'equal) kind (tc-fail "not a pi-tui color: ~A" (to-json color)))))

(defun tc-channel (color key) (tc-f (jget color key)))

(defun tc-indexed-to-rgb (index)
  (setf index (round index))
  (cond ((< index 16) (mapcar (lambda (v) (coerce v 'double-float)) (svref *tc-basic-colors* index)))
        ((< index 232)
         (let ((cube (- index 16)))
           (list (coerce (svref *tc-cube-values* (floor cube 36)) 'double-float) (coerce (svref *tc-cube-values* (floor (mod cube 36) 6)) 'double-float)
                 (coerce (svref *tc-cube-values* (mod cube 6)) 'double-float))))
        (t (let ((gray (coerce (+ 8 (* (- index 232) 10)) 'double-float))) (list gray gray gray)))))

(defun tc-in-gamut-p (linear) (every (lambda (v) (<= -1d-7 v (+ 1 1d-7))) linear))

(defun tc-oklch-to-rgb (l c h)
  (let* ((radians (/ (* h pi) 180)) (cos (cos radians)) (sin (sin radians)))
    (flet ((at-chroma (chroma) (tc-oklab-to-linear l (* chroma cos) (* chroma sin))))
      (let ((direct (at-chroma c)))
        (if (tc-in-gamut-p direct)
            (tc-linear-to-rgb direct)
            (let ((linear (at-chroma 0d0)) (low 0d0) (high c))
              (dotimes (k 20)
                (let* ((chroma (/ (+ low high) 2)) (candidate (at-chroma chroma)))
                  (if (tc-in-gamut-p candidate) (setf low chroma linear candidate) (setf high chroma))))
              (tc-linear-to-rgb linear)))))))

(defun tc-to-rgb (color)
  "The sRGB channels of COLOR as a list of three doubles."
  (let ((kind (tc-kind color)))
    (cond ((string= kind "indexed") (tc-indexed-to-rgb (jget color "index")))
          ((string= kind "rgb") (list (tc-channel color "r") (tc-channel color "g") (tc-channel color "b")))
          (t (tc-oklch-to-rgb (tc-channel color "l") (tc-channel color "c") (tc-channel color "h"))))))

(def-tc tui-color-to-rgb (color)
  "The sRGB channels of COLOR as {\"r\",\"g\",\"b\"} (pi-tui's colorToRgb)."
  (destructuring-bind (r g b) (tc-to-rgb color) (obj "r" r "g" g "b" b)))

(defun tc-to-oklch (color)
  (if (string= (tc-kind color) "oklch")
      (list (tc-channel color "l") (tc-channel color "c") (tc-channel color "h"))
      (destructuring-bind (l a b) (apply #'tc-rgb-to-oklab (tc-to-rgb color))
        (list l (sb-kernel::%hypot a b) (tc-fmod (+ (/ (* (atan b a) 180) pi) 360) 360d0)))))

(def-tc tui-color-to-oklch (color)
  "COLOR as {\"l\",\"c\",\"h\"} (pi-tui's colorToOklch)."
  (destructuring-bind (l c h) (tc-to-oklch color) (obj "l" l "c" c "h" h)))

(def-tc tui-color-to-okhsl (color)
  "COLOR as {\"h\",\"s\",\"l\"} (pi-tui's colorToOkhsl)."
  (multiple-value-bind (h s l) (apply #'tc-rgb-to-okhsl (tc-to-rgb color)) (obj "h" h "s" s "l" l)))

(def-tc tui-color-to-hex (color)
  "COLOR as \"#rrggbb\" (pi-tui's colorToHex)."
  (format nil "#~{~A~}" (mapcar (lambda (v) (let ((r (tc-round v)))
                                              (if (tc-finite-p r) (string-downcase (format nil "~2,'0x" (round r))) (format nil "~2@A" (tc-str r)))))
                                (tc-to-rgb color))))

(def-tc tui-mix-colors (first second amount &optional (space "oklch"))
  "FIRST blended toward SECOND by AMOUNT (0-1) in SPACE, \"oklch\" or \"srgb\" (pi-tui's mixColors)."
  (let ((x (tc-f amount)))
    (tc-require-finite x "amount")
    (when (or (< x 0) (> x 1)) (tc-fail "amount must be between 0 and 1: ~A" (tc-str x)))
    (if (equal space "srgb")
        (destructuring-bind (ar ag ab) (tc-to-rgb first)
          (destructuring-bind (br bg bb) (tc-to-rgb second)
            (tui-rgb-color (+ ar (* (- br ar) x)) (+ ag (* (- bg ag) x)) (+ ab (* (- bb ab) x)))))
        (destructuring-bind (al ac ah) (tc-to-oklch first)
          (destructuring-bind (bl bc bh) (tc-to-oklch second)
            (let* ((first-hue (if (< ac 1d-7) bh ah)) (second-hue (if (< bc 1d-7) first-hue bh))
                   (hue-delta (- (tc-fmod (+ (- second-hue first-hue) 540) 360d0) 180)))
              (tui-oklch-color (+ al (* (- bl al) x)) (+ ac (* (- bc ac) x)) (+ first-hue (* hue-delta x)))))))))

;;; ------------------------------------------------------------------ parseColor

(defun tc-hex-p (c) (or (char<= #\0 c #\9) (char<= #\a c #\f) (char<= #\A c #\F)))

(defun tc-digits-end (text i) (loop while (and (< i (length text)) (char<= #\0 (char text i) #\9)) do (incf i)) i)

(defun tc-number-end (text i)
  "The end of the number [+-]?(digits(.digits*)?|.digits)(e[+-]?digits)? at I, or NIL."
  (let ((n (length text)) (j i))
    (when (and (< j n) (find (char text j) "+-")) (incf j))
    (let ((int-end (tc-digits-end text j)))
      (cond ((> int-end j) (setf j int-end)
             (when (and (< j n) (char= (char text j) #\.)) (setf j (tc-digits-end text (1+ j)))))
            ((and (< j n) (char= (char text j) #\.) (> (tc-digits-end text (1+ j)) (1+ j))) (setf j (tc-digits-end text (1+ j))))
            (t (return-from tc-number-end nil))))
    (when (and (< j n) (char-equal (char text j) #\e))
      (let ((k (1+ j)))
        (when (and (< k n) (find (char text k) "+-")) (incf k))
        (when (> (tc-digits-end text k) k) (setf j (tc-digits-end text k)))))
    j))

(defun tc-parse-number (text)
  "The double of the number TEXT (float() of Python): an infinity when it overflows."
  (let* ((n (length text)) (i 0) (sign 1) (mantissa 0) (scale 0) (exp 0))
    (when (find (char text 0) "+-") (when (char= (char text 0) #\-) (setf sign -1)) (incf i))
    (loop while (and (< i n) (char<= #\0 (char text i) #\9)) do (setf mantissa (+ (* mantissa 10) (digit-char-p (char text i)))) (incf i))
    (when (and (< i n) (char= (char text i) #\.))
      (incf i)
      (loop while (and (< i n) (char<= #\0 (char text i) #\9)) do (setf mantissa (+ (* mantissa 10) (digit-char-p (char text i)))) (decf scale) (incf i)))
    (when (and (< i n) (char-equal (char text i) #\e))
      (incf i)
      (let ((exp-sign 1))
        (when (find (char text i) "+-") (when (char= (char text i) #\-) (setf exp-sign -1)) (incf i))
        (setf exp (* exp-sign (parse-integer text :start i)))))
    (let ((e (+ scale exp)))
      (cond ((zerop mantissa) (if (minusp sign) -0d0 0d0))
            ((> (+ e (length (princ-to-string mantissa))) 330) (if (minusp sign) (- +tc-inf+) +tc-inf+))
            ((< (+ e (length (princ-to-string mantissa))) -330) (if (minusp sign) -0d0 0d0))
            (t (let ((v (* sign mantissa (expt 10 e))))
                 (handler-case (coerce v 'double-float)
                   (error () (cond ((< (abs v) 1) (if (minusp sign) -0d0 0d0)) ((minusp sign) (- +tc-inf+)) (t +tc-inf+))))))))))

(defun tc-ws-p (c)
  (let ((code (char-code c)))
    (or (<= 9 code 13) (= code 32) (= code #xa0) (= code #x1680) (<= #x2000 code #x200a) (= code #x2028) (= code #x2029)
        (= code #x202f) (= code #x205f) (= code #x3000) (= code #xfeff))))

(defun tc-skip-ws (text i min)
  "The index after the whitespace run at I, or NIL when it is shorter than MIN."
  (let ((j i)) (loop while (and (< j (length text)) (tc-ws-p (char text j))) do (incf j)) (and (>= (- j i) min) j)))

(defun tc-scan (text spec)
  "Match SPEC, a list of (:lit string) (:ws min) (:num) (:opt string) items, case-insensitively against all of TEXT; the list of number
texts and (for :opt) the strings found, or NIL."
  (let ((i 0) (found '()))
    (dolist (item spec)
      (ecase (first item)
        (:lit (let ((end (+ i (length (second item)))))
                (unless (and (<= end (length text)) (string-equal (second item) text :start2 i :end2 end)) (return-from tc-scan nil))
                (setf i end)))
        (:ws (setf i (or (tc-skip-ws text i (second item)) (return-from tc-scan nil))))
        (:num (let ((end (tc-number-end text i))) (unless end (return-from tc-scan nil)) (push (subseq text i end) found) (setf i end)))
        (:opt (let ((end (+ i (length (second item)))))
                (if (and (<= end (length text)) (string-equal (second item) text :start2 i :end2 end))
                    (progn (push (second item) found) (setf i end))
                    (push nil found))))))
    (and (= i (length text)) (nreverse found))))

(def-tc tui-parse-color (value)
  "A color from \"#rgb\", \"#rrggbb\", \"oklch(...)\", \"okhsl(...)\" or a palette index (pi-tui's parseColor)."
  (unless (stringp value) (return-from tui-parse-color (tui-indexed-color value)))
  (when (and (member (length value) '(4 7)) (char= (char value 0) #\#) (every #'tc-hex-p (subseq value 1)))
    (let ((digits (if (= (length value) 4) (loop for c across (subseq value 1) collect c collect c) (coerce (subseq value 1) 'list))))
      (flet ((byte-at (k) (parse-integer (coerce (subseq digits k (+ k 2)) 'string) :radix 16)))
        (return-from tui-parse-color (tui-rgb-color (byte-at 0) (byte-at 2) (byte-at 4))))))
  (let ((m (tc-scan value '((:lit "oklch(") (:ws 0) (:num) (:opt "%") (:ws 1) (:num) (:ws 1) (:num) (:opt "deg") (:ws 0) (:lit ")")))))
    (when m
      (return-from tui-parse-color
        (tui-oklch-color (/ (tc-parse-number (first m)) (if (second m) 100 1)) (tc-parse-number (third m)) (tc-parse-number (fourth m))))))
  (let ((m (tc-scan value '((:lit "okhsl(") (:ws 0) (:num) (:opt "deg") (:ws 1) (:num) (:opt "%") (:ws 1) (:num) (:opt "%") (:ws 0) (:lit ")")))))
    (when m
      (return-from tui-parse-color
        (tui-okhsl-color (tc-parse-number (first m)) (/ (tc-parse-number (third m)) (if (fourth m) 100 1))
                         (/ (tc-parse-number (fifth m)) (if (sixth m) 100 1))))))
  (tc-fail "Invalid color value: ~A" value))

;;; ------------------------------------------------------------------ ANSI

(defun tc-find-closest (values target)
  (let ((index 0) (distance +tc-inf+))
    (loop for i from 0 for v across values
          do (let ((d (abs (- target v)))) (when (< d distance) (setf index i distance d))))
    index))

(defun tc-color-distance (a b)
  (destructuring-bind (dr dg db) (mapcar #'- a b) (+ (* dr dr 0.299d0) (* dg dg 0.587d0) (* db db 0.114d0))))

(defun tc-rgb-to-ansi256 (rgb)
  (destructuring-bind (r g b) rgb
    (let* ((ri (tc-find-closest *tc-cube-values* r)) (gi (tc-find-closest *tc-cube-values* g)) (bi (tc-find-closest *tc-cube-values* b))
           (cube (mapcar (lambda (i) (coerce (svref *tc-cube-values* i) 'double-float)) (list ri gi bi)))
           (cube-index (+ 16 (* 36 ri) (* 6 gi) bi))
           (gray (tc-round (+ (* 0.299d0 r) (* 0.587d0 g) (* 0.114d0 b))))
           (gray-offset (tc-find-closest *tc-gray-values* gray))
           (gray-value (coerce (svref *tc-gray-values* gray-offset) 'double-float))
           (spread (- (max r g b) (min r g b))))
      (if (and (< spread 10) (< (tc-color-distance rgb (list gray-value gray-value gray-value)) (tc-color-distance rgb cube)))
          (+ 232 gray-offset)
          cube-index))))

(defun tc-color-ansi (color mode background)
  (let ((layer (if background 48 38)))
    (if (string= (tc-kind color) "indexed")
        (tui-esc (format nil "[~D;5;~Dm" layer (jget color "index")))
        (let ((rgb (tc-to-rgb color)))
          (if (equal mode "truecolor")
              (tui-esc (format nil "[~D;2;~{~A~^;~}m" layer (mapcar (lambda (v) (tc-str (tc-round v))) rgb)))
              (tui-esc (format nil "[~D;5;~Dm" layer (tc-rgb-to-ansi256 rgb))))))))

(def-tc tui-foreground-ansi (color mode)
  "The SGR sequence selecting COLOR as the foreground in MODE, \"truecolor\" or \"256color\" (pi-tui's foregroundAnsi)."
  (tc-color-ansi color mode nil))

(def-tc tui-background-ansi (color mode)
  "The SGR sequence selecting COLOR as the background in MODE, \"truecolor\" or \"256color\" (pi-tui's backgroundAnsi)."
  (tc-color-ansi color mode t))

(defun tc-opt (options key)
  "The member KEY (a string; the keyword of the same name in a plist) of OPTIONS, with false and null read as NIL."
  (let ((v (if (hash-table-p options) (gethash key options) (getf options (intern (string-upcase key) :keyword)))))
    (and v (not (eq v :false)) (not (eq v :null)) v)))

(defun tui-style-text-with-ansi (text fg-ansi bg-ansi options)
  "TEXT wrapped in the SGR sequences FG-ANSI and BG-ANSI (NIL or empty: none) and the attributes of OPTIONS (bold dim italic underline inverse
strikethrough; a hash-table or plist), closing them in reverse order (pi-tui's styleTextWithAnsi)."
  (let ((prefix "") (suffix ""))
    (flet ((op (code) (setf prefix (tui-cat prefix (tui-esc code)))) (cl (code) (setf suffix (tui-cat (tui-esc code) suffix))))
      (when (and (stringp fg-ansi) (plusp (length fg-ansi))) (setf prefix (tui-cat prefix fg-ansi)) (cl "[39m"))
      (when (and (stringp bg-ansi) (plusp (length bg-ansi))) (setf prefix (tui-cat prefix bg-ansi)) (cl "[49m"))
      (when (tc-opt options "bold") (op "[1m"))
      (when (tc-opt options "dim") (op "[2m"))
      (when (or (tc-opt options "bold") (tc-opt options "dim")) (cl "[22m"))
      (when (tc-opt options "italic") (op "[3m") (cl "[23m"))
      (when (tc-opt options "underline") (op "[4m") (cl "[24m"))
      (when (tc-opt options "inverse") (op "[7m") (cl "[27m"))
      (when (tc-opt options "strikethrough") (op "[9m") (cl "[29m")))
    (tui-cat prefix text suffix)))

(def-tc tui-style-text (text options mode)
  "TEXT wrapped in the SGR sequences of OPTIONS (a hash-table or plist: bold dim italic underline inverse strikethrough, fg and bg colors) in
MODE (pi-tui's styleText)."
  (let ((fg (tc-opt options "fg")) (bg (tc-opt options "bg")))
    (tui-style-text-with-ansi text (and fg (tc-color-ansi fg mode nil)) (and bg (tc-color-ansi bg mode t)) options)))

(defun tui-color-ansi (color mode background)
  "The SGR sequence selecting the wire COLOR (a JSON object as the host sends colors) as the foreground, or the background when BACKGROUND."
  (with-tc-floats (tc-color-ansi color mode background)))

(defun tui-wire-color (value)
  "A copy of the wire color VALUE, or NIL when it is not a well-formed pi-tui color."
  (flet ((num (key) (let ((v (jget value key))) (and (realp v) (tc-finite-p (coerce v 'double-float)) v))))
    (when (hash-table-p value)
      (let ((kind (jget value "kind")))
        (cond ((equal kind "indexed") (let ((index (num "index"))) (and index (= index (floor index)) (<= 0 index 255) (obj "kind" "indexed" "index" (floor index)))))
              ((equal kind "rgb") (and (num "r") (num "g") (num "b") (obj "kind" "rgb" "r" (num "r") "g" (num "g") "b" (num "b"))))
              ((equal kind "oklch") (and (num "l") (num "c") (num "h") (obj "kind" "oklch" "l" (num "l") "c" (num "c") "h" (num "h")))))))))
