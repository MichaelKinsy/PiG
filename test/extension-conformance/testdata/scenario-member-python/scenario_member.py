"""The Python scenario member: a second extension of the SDK under test, loaded beside
the scenario driver so that, when the scenario runs shared, both are members of one
cell (one process). It registers one command and reads no Spec.
"""

import pig_sdk as sdk


def new_extension():
    extension = sdk.Extension("scenario-member-python")
    extension.command("member-ping", "The cell member's command", lambda ctx, args: None)
    return extension
