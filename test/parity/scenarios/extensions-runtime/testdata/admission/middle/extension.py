from pathlib import Path
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    order = Path("d77-order")
    if order.read_text() != "A":
        raise RuntimeError("B admitted before A")
    with order.open("a") as stream:
        stream.write("B")
    return pig_sdk.Extension("middle")
