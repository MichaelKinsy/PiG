package prompts

import "sync/atomic"

// pigletDocsTopic is set while a Piglet is active in this process. It adds the Piglets topic to the docs section, so
// Pi's lines of the section stay byte-identical for Stock PiG without a Piglet.
var pigletDocsTopic atomic.Bool

// SetPigletDocsTopic names the Piglets documentation in the docs section while a Piglet is active.
//
// pig additive (D23, D92): Pi has no Piglets.
func SetPigletDocsTopic(active bool) { pigletDocsTopic.Store(active) }
