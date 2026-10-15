import pig_sdk


class _RaisingInvalidator:
    def __init__(self):
        self.disposed = False

    def render(self, width):
        return []

    def set_invalidate(self, invalidate):
        if invalidate is None:
            raise RuntimeError("detach exploded")

    def dispose(self):
        self.disposed = True


def test_raising_set_invalidate_on_cleanup_is_reported_and_dispose_still_runs(capsys):
    # The Go SDK's overlay stop reports a SetInvalidate(nil) panic in the
    # extension log (stderr); the Python SDK reports the same failure and still
    # disposes the component.
    component = _RaisingInvalidator()
    pig_sdk._dispose_remote_component(component)
    err = capsys.readouterr().err
    assert "set_invalidate(None)" in err
    assert "detach exploded" in err
    assert component.disposed
