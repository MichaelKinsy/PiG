import json

from pig_sdk.serialize_conversation import serialize_conversation

# Pi's serializeConversation (core/compaction/utils.ts:101-155) runs on JavaScript strings, which can hold an unpaired surrogate: JSON.stringify writes it
# as its \uXXXX escape, and the truncateForSummary cut goes through sliceHead: a cut between the two halves of a pair drops the pair's high surrogate too, a
# cut after an unpaired high surrogate keeps it. The expected strings are Node's output.


def test_unpaired_surrogates_match_pi():
    cases = [
        ('{"role":"toolResult","content":"' + "a" * 1999 + '\\ud83d\\ude00"}',
         '"[Tool result]: ' + "a" * 1999 + '\\n\\n[... 1 more characters truncated]"'),
        ('{"role":"toolResult","content":"' + "a" * 1999 + '\\ud83dx"}',
         '"[Tool result]: ' + "a" * 1999 + '\\ud83d\\n\\n[... 1 more characters truncated]"'),
        ('{"role":"assistant","content":[{"type":"toolCall","name":"t","arguments":{"s":"\\udc00","k\\ud83d":1}}]}',
         '"[Assistant tool calls]: t(s=\\"\\\\udc00\\", k\\ud83d=1)"'),
        ('{"role":"assistant","content":[{"type":"toolCall","name":"t","arguments":"\\ud83d\\ud83d\\ude00"}]}',
         '"[Assistant tool calls]: t(0=\\"\\\\ud83d\\", 1=\\"\\\\ud83d\\", 2=\\"\\\\ude00\\")"'),
    ]
    for message, want in cases:
        assert serialize_conversation([json.loads(message)]) == json.loads(want)
