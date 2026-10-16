"""G-D8: the Graphiti patches are for one version. On another the service
stops at start instead of running with patches that silently miss."""

import importlib
import importlib.metadata as md
import sys
import unittest


class PatchGuardTest(unittest.TestCase):
    def test_another_graphiti_version_stops_the_service(self):
        real = md.version
        sys.modules.pop("app.patches", None)
        md.version = lambda name: "0.31.0" if name == "graphiti-core" else real(name)
        try:
            with self.assertRaises(RuntimeError):
                importlib.import_module("app.patches")
        finally:
            md.version = real
            sys.modules.pop("app.patches", None)
            importlib.import_module("app.patches")

    def test_the_pinned_version_loads(self):
        sys.modules.pop("app.patches", None)
        importlib.import_module("app.patches")


if __name__ == "__main__":
    unittest.main()
