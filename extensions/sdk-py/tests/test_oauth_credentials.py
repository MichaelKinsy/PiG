"""Pi's OAuthCredentials is ``{ refresh, access, expires: number, [key]: unknown }`` (packages/ai/src/auth/types.ts:23-28)."""

import json
import unittest

from pig_sdk import OAuthCredentials


def round_trip(value):
    return json.loads(json.dumps(OAuthCredentials._from_wire(json.loads(json.dumps(value)))._to_wire()))


class OAuthCredentialsTest(unittest.TestCase):
    def test_exact_expiry_and_provider_keys_round_trip(self):
        meta = {"k": [1, None, ""]}
        for value in (
            {"refresh": "r", "access": "a", "expires": 1700000000000.5, "meta": meta, "projectId": ""},
            {"refresh": "r", "access": "a", "expires": 1e21, "meta": meta},
            {"refresh": "r", "access": "a", "expires": "soon"},
            {"refresh": "r", "access": "a", "expires": None},
            {"refresh": "r", "access": "a", "expires": 42, "accountId": "acct", "type": "oauth"},
        ):
            with self.subTest(value=value):
                self.assertEqual(round_trip(value), value)

    def test_fractional_expiry_is_not_truncated(self):
        creds = OAuthCredentials._from_wire({"refresh": "r", "access": "a", "expires": 10.25})
        self.assertEqual(creds.expires, 10.25)
        creds.expires = 2.5
        self.assertEqual(creds._to_wire()["expires"], 2.5)
        creds.expires = -0.0
        self.assertEqual(json.dumps(creds._to_wire()["expires"]), "0")

    def test_absent_expiry_stays_absent(self):
        creds = OAuthCredentials._from_wire({"refresh": "r", "access": "a"})
        self.assertFalse(creds.has_expires())
        self.assertNotIn("expires", creds._to_wire())
        creds = OAuthCredentials(access="x", expires=7)
        self.assertTrue(creds.has_expires())
        creds.clear_expires()
        self.assertNotIn("expires", creds._to_wire())

    def test_assigning_expires_after_an_absent_value_writes_it(self):
        # A refresh callback that assigns a new expiry to a credential stored without one must return that expiry, as `{ ...creds, expires }` does in Pi; the Go and Rust SDKs apply absence only while the integer projection is still 0.
        creds = OAuthCredentials._from_wire({"refresh": "r", "access": "a"})
        creds.expires = 1700000000000.5
        self.assertTrue(creds.has_expires())
        self.assertEqual(creds._to_wire()["expires"], 1700000000000.5)
        creds.clear_expires()
        creds.expires = 7
        self.assertEqual(creds._to_wire()["expires"], 7)

    def test_integer_expiry_beyond_two_to_the_53_is_a_javascript_number(self):
        # JSON.parse reads 1152921504606847000 as the double 2**60, and JSON.stringify writes that double back as 1152921504606847000 (packages/ai/src/auth/types.ts:24-27 declares expires a number). The Go SDK projects the same double.
        creds = OAuthCredentials._from_wire(json.loads('{"refresh":"r","access":"a","expires":1152921504606847000}'))
        self.assertEqual(creds.expires - 2**60, 0)
        self.assertEqual(creds.expires - 1, 2.0**60)
        self.assertEqual(json.dumps(creds._to_wire()["expires"]), "1152921504606847000")
        self.assertEqual(json.dumps(OAuthCredentials(expires=2**60)._to_wire()["expires"]), "1152921504606847000")
        self.assertEqual(json.dumps(OAuthCredentials(expires=2**53 + 2)._to_wire()["expires"]), "9007199254740994")
        self.assertEqual(json.dumps(OAuthCredentials(expires=-(2**60))._to_wire()["expires"]), "-1152921504606847000")
        # A safe integer stays an exact int; a value above the double range is Infinity, which JSON.stringify writes as null.
        self.assertIs(type(OAuthCredentials._from_wire({"expires": 2**53}).expires), int)
        self.assertIsNone(OAuthCredentials(expires=10**400)._to_wire()["expires"])


if __name__ == "__main__":
    unittest.main()
