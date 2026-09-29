import unittest
from unittest.mock import patch

import pig_sdk


class ProviderRequestSignalTests(unittest.TestCase):
    def test_producer_uses_the_armed_signal_through_completion_and_cancellation(self):
        # Pi's provider callback and its iterator share the request AbortSignal. The armed Context owns the corresponding Python signal.
        for boundary in ("complete", "before-bind", "between-events", "after-iterator"):
            with self.subTest(boundary=boundary):
                ext = pig_sdk.Extension("provider-signal")
                frames = []
                contexts = []
                final = {"role": "assistant", "content": [{"type": "text", "text": "final"}]}
                start = {"type": "start", "partial": final}
                delta = {"type": "text_delta", "contentIndex": 0, "delta": "final", "partial": final}
                done = {"type": "done", "reason": "stop", "message": final}

                def cancel(ctx):
                    with ext._state_lock:
                        ctx._parent.state = "cancelled"
                        ext._active[ctx.request_id] = (ctx._cancelled, "test cancellation")
                        ctx._cancelled.set()

                class Stream(pig_sdk.ModelEventStream):
                    def events(self):
                        yield start
                        if boundary == "between-events":
                            cancel(contexts[0])
                        yield delta
                        if boundary == "after-iterator":
                            cancel(contexts[0])
                            return
                        yield done

                def produce(ctx, model, transcript, options):
                    contexts.append(ctx)
                    self.assertIs(ctx._cancelled, ext._active[ctx.request_id][0])
                    if boundary == "before-bind":
                        cancel(ctx)
                        return pig_sdk.ModelEventStream()
                    stream = Stream()
                    stream.end(final)
                    return stream

                ext.register_provider("fixture", {"streamSimple": produce})
                request = {"type": "request", "id": "producer", "request": {
                    "method": "provider_stream_simple", "tool": "fixture",
                    "args": {"model": {}, "context": {}, "options": {}},
                }}
                with patch.object(ext, "_send_locked", side_effect=frames.append):
                    armed = ext._arm_request(request)
                    ext._handle_request(request, armed)

                responses = [frame["response"] for frame in frames if frame["type"] == "response"]
                updates = [frame["notify"]["args"]["result"] for frame in frames
                           if frame["type"] == "notify" and frame["notify"]["method"] == "provider_stream_event"]
                if boundary == "complete":
                    self.assertEqual(responses, [{"result": final, "error": None}])
                    self.assertEqual(updates, [start, delta, done])
                    self.assertFalse(armed.is_cancelled())
                else:
                    self.assertEqual(responses, [{"result": None, "error": {"message": "provider stream aborted"}}])
                    self.assertEqual(updates, {"before-bind": [], "between-events": [start], "after-iterator": [start, delta]}[boundary])
                    self.assertTrue(armed.is_cancelled())
                self.assertEqual(ext._provider_active, {})
                self.assertEqual(ext._active, {})
                self.assertEqual(ext._request_parents, {})


if __name__ == "__main__":
    unittest.main()
