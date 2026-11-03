// Registers a handler that returns nothing for every agent, turn, message, tool and input event. Pi runs each handler and draws nothing for it,
// so a run with these handlers must lay out the screen exactly as a run without them.
const EVENTS = [
  "input",
  "before_agent_start",
  "agent_start",
  "turn_start",
  "context",
  "before_provider_request",
  "after_provider_response",
  "message_start",
  "message_update",
  "message_end",
  "tool_execution_start",
  "tool_call",
  "tool_execution_update",
  "tool_result",
  "tool_execution_end",
  "turn_end",
  "agent_end",
  "agent_settled",
];

export default function (pi) {
  for (const event of EVENTS) pi.on(event, () => undefined);
}
