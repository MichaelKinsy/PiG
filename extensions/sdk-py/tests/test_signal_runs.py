import gc
import weakref

from pig_sdk.extension_runner import _signal_wire
from pig_sdk.provider import ProviderSignal


# The getSignal answer of D235: `{"aborted", "run", "endedAborted"?}`. Pi gives every run its own signal object, so the object is the run: the token is new when
# the function returns another object than the one of the last answer, and `endedAborted` says the previous object had aborted when the object changed.
def _wire(*signals):
    """A wrapper over a function that answers the given signals in turn, the last one for every later call."""
    queue = list(signals)
    current = [queue[0]]

    def function():
        return current[0]

    def advance():
        queue.pop(0)
        current[0] = queue[0]

    return _signal_wire(function), advance


def test_first_answer_mints_a_token_and_the_same_object_keeps_it():
    signal = ProviderSignal()
    wire, _ = _wire(signal)
    first = wire()
    assert first["aborted"] is False
    assert type(first["run"]) is int and first["run"] > 0
    assert "endedAborted" not in first
    assert wire() == first
    signal.set()
    assert wire() == {"aborted": True, "run": first["run"]}


def test_a_different_object_mints_the_next_token():
    first, second = ProviderSignal(), ProviderSignal()
    wire, advance = _wire(first, second)
    one = wire()
    advance()
    two = wire()
    assert two["run"] > one["run"]
    assert "endedAborted" not in two


def test_a_none_answer_touches_nothing():
    state = {"signal": None}
    wire = _signal_wire(lambda: state["signal"])
    assert wire() is None
    first = ProviderSignal()
    state["signal"] = first
    one = wire()
    state["signal"] = None
    assert wire() is None
    state["signal"] = first
    assert wire() == one


def test_ended_aborted_is_set_at_the_change_and_repeated_with_the_token():
    first, second, third = ProviderSignal(), ProviderSignal(), ProviderSignal()
    wire, advance = _wire(first, second, third)
    wire()
    first.set()
    advance()
    two = wire()
    assert two["endedAborted"] is True
    assert wire() == two
    second.set()
    assert wire() == {"aborted": True, "run": two["run"], "endedAborted": True}
    advance()
    three = wire()
    assert three["endedAborted"] is True and three["run"] > two["run"]


def test_ended_aborted_is_not_set_when_the_previous_object_was_live():
    first, second = ProviderSignal(), ProviderSignal()
    wire, advance = _wire(first, second)
    wire()
    advance()
    assert "endedAborted" not in wire()
    first.set()
    assert "endedAborted" not in wire()


def test_two_wrappers_never_share_a_token():
    one, two = _signal_wire(ProviderSignal), _signal_wire(ProviderSignal)
    tokens = [one()["run"], two()["run"]]
    # ProviderSignal() as the function is a new object per answer: every answer is a new run, across both wrappers.
    tokens += [one()["run"], two()["run"]]
    assert len(set(tokens)) == 4
    shared = ProviderSignal()
    a, b = _signal_wire(lambda: shared), _signal_wire(lambda: shared)
    assert a()["run"] != b()["run"]


def test_the_last_signal_is_held_while_it_is_last():
    signal = ProviderSignal()
    state = {"signal": signal}
    wire = _signal_wire(lambda: state["signal"])
    wire()
    ref = weakref.ref(signal)
    state["signal"] = None
    del signal
    gc.collect()
    assert ref() is not None
    state["signal"] = ProviderSignal()
    wire()
    gc.collect()
    assert ref() is None
