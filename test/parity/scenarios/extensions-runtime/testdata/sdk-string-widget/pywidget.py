import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("pywidget")
    ext.on_event(
        "session_start",
        lambda ctx, _event: ctx.set_widget("sdk-wide", ["WIDGET-START " + "W" * 150 + " tail words wrap here", "short entry"]),
    )
    return ext
