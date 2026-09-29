# Custom messages during settlement

Pi0.87.1 `agent-session.ts:_emitAgentSettled` awaits extension handlers before emitting the public `agent_settled` event. A custom message appended by such a handler therefore reaches every subscriber before settlement.

PiG previously dispatched the extension handler inside its mode-output funnel. Direct Session listeners observed `custom:start, custom:end, settled`, but the mode channel observed `settled, custom:start, custom:end`. `TestSettledCustomMessagePrecedesSettlementPublication` reproduces that exact failure on a real Session.

The Session now owns the extension dispatch before queueing the public settlement event. The funnel still serializes public listener and mode delivery, but no longer repeats extension dispatch. This also lets a handler append a batch larger than the channel capacity without blocking the consumer that must drain it.

The regression covers batches of1 and128 and compares the complete listener and mode-output event sequences. It reuses the existing Session/faux harness; its event consumer acknowledges the established FlushEvents marker. No sleeps, timeout increases or production test hooks are added.

Session30 compares those complete records with published Pi using two real Session subscribers. Three strict pairs pass. Reversing handler/publication order compiles and fails both native cases and the pair. Full agent/coding/internal-codingagent packages, focused race3, native/Windows vet and package lint pass. The full Session family passes. This is not a claim that all custom-message reentrancy/error combinations are closed.
