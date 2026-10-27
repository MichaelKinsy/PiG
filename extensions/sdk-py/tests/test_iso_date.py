import pytest

from pig_sdk import host_helpers
from pig_sdk.host_helpers import parse_date, set_host_helpers

# The values are what Node 24.22.1 (V8) answers for Date.parse of each text.
CANONICAL = {
    "1970-01-01T00:00:00.000Z": 0,
    "0000-01-01T00:00:00.000Z": -62167219200000,
    "0000-03-01T00:00:00.000Z": -62162035200000,
    "0001-01-01T00:00:00.000Z": -62135596800000,
    "9999-12-31T23:59:59.999Z": 253402300799999,
    "2000-02-29T12:34:56.789Z": 951827696789,
    "1900-02-28T23:59:59.999Z": -2203891200001,
    "1900-03-01T00:00:00.000Z": -2203891200000,
    "2100-03-01T00:00:00.000Z": 4107542400000,
    "2024-02-29T00:00:00.000Z": 1709164800000,
    "2026-10-24T18:30:15.042Z": 1792866615042,
    "1969-12-31T23:59:59.999Z": -1,
    "1600-02-29T00:00:00.000Z": -11670998400000,
    "2026-12-31T23:59:59.999Z": 1798761599999,
    "2026-01-01T00:00:00.001Z": 1767225600001,
}

# Texts V8 reads that the local parser must leave to the host: another form of the same instant, a field the legacy parser accepts although it is out of range
# (February 30 is March 2), and text that is not a date. The full-width and Arabic-Indic digits are 24 characters long.
LEFT_TO_THE_HOST = [
    "2026-01-02T03:04:05.678", "2026-01-02T03:04:05.678+00:00", "+012026-01-02T03:04:05.678Z", "-000001-01-01T00:00:00.000Z", "2026-01-02t03:04:05.678Z",
    "2026-01-02T03:04:05.678z", "2026-02-30T00:00:00.000Z", "2026-13-01T00:00:00.000Z", "2026-01-02T24:00:00.000Z", "2026-01-02T03:04:60.000Z",
    "2026-01-02T03:60:05.000Z", "2026-00-10T00:00:00.000Z", "2026-04-31T00:00:00.000Z", "2026-01-02 03:04:05.678Z", "2026-01-02T03:04:05.678Z\n",
    "２０２６-01-02T03:04:05.678Z", "٢٠٢٦-01-02T03:04:05.678Z", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.67Z", "2026-01-02T03:04:05.678Zx", "", "2026",
    "2026-01-02T0x:04:05.678Z", "2026-01-02T03:04:05.67:Z", "2026-01-02T03:04:0/.678Z", "202:-01-02T03:04:05.678Z", "2026-01-02T03:04:05.@78Z", "2026-01-00T00:00:00.000Z",
]


@pytest.mark.parametrize("text,want", list(CANONICAL.items()))
def test_parse_iso_date_matches_date_parse(text, want):
    assert host_helpers._parse_iso_date(text) == want


@pytest.mark.parametrize("text", LEFT_TO_THE_HOST)
def test_parse_iso_date_leaves_other_text_to_the_host(text):
    assert host_helpers._parse_iso_date(text) is None


def test_parse_date_asks_the_host_only_for_text_the_local_parser_leaves():
    calls = []

    def host(text):
        calls.append(text)
        return -7.0

    set_host_helpers(parse_date=host)
    try:
        for text, want in CANONICAL.items():
            assert parse_date(text) == want
        assert calls == []
        for text in LEFT_TO_THE_HOST:
            assert parse_date(text) == -7.0
        assert calls == LEFT_TO_THE_HOST
    finally:
        set_host_helpers()
