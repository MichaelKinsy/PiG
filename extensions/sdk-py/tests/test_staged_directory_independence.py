import os
import sys

import pig_sdk


def test_importing_the_package_loads_every_module():
    """A Python extension runs from the staged SDK directory of the PiG build that started it (PYTHONPATH, or sys.path in a
    packed runner). Another PiG build that shares the config root evicts the directories beyond the four most recently used
    while the extension runs (internal/contentdirs.Evict), and nothing holds a lease on them. The SDK is safe against that
    only because `import pig_sdk` loads every module of the package: no function imports a submodule from disk afterwards
    (the `from . import host_helpers` inside functions finds the module already in sys.modules). A module that only a
    function imported would raise ImportError in a session that outlives its directory."""
    package_dir = os.path.dirname(pig_sdk.__file__)
    modules = sorted(name[:-3] for name in os.listdir(package_dir) if name.endswith(".py") and name != "__init__.py")
    assert modules
    missing = [name for name in modules if f"pig_sdk.{name}" not in sys.modules]
    assert not missing, f"modules not loaded by `import pig_sdk`: {missing}"
    subpackages = [name for name in os.listdir(package_dir) if os.path.isdir(os.path.join(package_dir, name)) and name != "__pycache__"]
    assert not subpackages, f"the package has subpackages whose import could be deferred: {subpackages}"
