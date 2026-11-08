import CoreGraphics
import Foundation
let pid = Int32(CommandLine.arguments[1])!
let list = CGWindowListCopyWindowInfo([.optionAll], kCGNullWindowID) as! [[String: Any]]
var n = 0
for w in list where (w[kCGWindowOwnerPID as String] as? Int32) == pid {
  let b = w[kCGWindowBounds as String] as? [String: Any] ?? [:]
  print("window id=\(w[kCGWindowNumber as String] ?? "") layer=\(w[kCGWindowLayer as String] ?? "") onscreen=\(w[kCGWindowIsOnscreen as String] ?? "no") name=\(w[kCGWindowName as String] ?? "") bounds=\(b["Width"] ?? 0)x\(b["Height"] ?? 0)")
  n += 1
}
print("count=\(n)")
