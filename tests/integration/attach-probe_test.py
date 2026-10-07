#!/usr/bin/env python3
"""Exercise the PTY probe against a slow Docker command and a failing launcher."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest


PROBE = Path(__file__).with_name("attach-probe.py")


class AttachProbeTest(unittest.TestCase):
    def run_probe(self, launcher_fails=False):
        with tempfile.TemporaryDirectory(prefix="hcorral-attach-probe-") as directory:
            root = Path(directory)
            docker = root / "docker"
            docker.write_text("#!/usr/bin/env python3\n" + textwrap.dedent("""\
                import os
                from pathlib import Path
                import sys
                import time

                root = Path(os.environ["PROBE_TEST_ROOT"])
                if "list-clients" in sys.argv:
                    count = root / "probes"
                    attempts = int(count.read_text()) + 1 if count.exists() else 1
                    count.write_text(str(attempts))
                    if attempts == 1:
                        time.sleep(6)  # Longer than the per-command timeout.
                    print("hcorral")
                elif "detach-client" in sys.argv:
                    (root / "detached").touch()
                else:
                    sys.exit(2)
                """))
            docker.chmod(0o755)
            launcher = root / "launcher.py"
            launcher.write_text(textwrap.dedent("""\
                import os
                from pathlib import Path
                import sys
                import time

                if os.environ["PROBE_TEST_FAIL"] == "1":
                    sys.exit(7)
                print("fixture attached", flush=True)
                marker = Path(os.environ["PROBE_TEST_ROOT"]) / "detached"
                while not marker.exists():
                    time.sleep(0.05)
                """))
            result = subprocess.run(
                [sys.executable, str(PROBE), "fixture", sys.executable, str(launcher)],
                env=dict(os.environ, PATH=f"{root}{os.pathsep}{os.environ['PATH']}",
                         PROBE_TEST_ROOT=str(root), PROBE_TEST_FAIL=str(int(launcher_fails)),
                         HCORRAL_TEST_ATTACH_TEXT="fixture attached"),
                capture_output=True, text=True, timeout=20,
            )
            probes = root / "probes"
            return result, int(probes.read_text()) if probes.exists() else 0, (root / "detached").exists()

    def test_slow_docker_probe_recovers_and_detaches(self):
        result, attempts, detached = self.run_probe()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertGreaterEqual(attempts, 2)
        self.assertTrue(detached)

    def test_launcher_failure_is_not_hidden(self):
        result, _, detached = self.run_probe(launcher_fails=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("launcher exited before clean detach", result.stderr)
        self.assertFalse(detached)


if __name__ == "__main__":
    unittest.main()
