import io
import plistlib
import unittest
from contextlib import redirect_stderr
from pathlib import Path
from subprocess import CompletedProcess
from tempfile import TemporaryDirectory
from unittest.mock import patch

from detach_build_images import detach_build_images


class DetachBuildImagesTest(unittest.TestCase):
    def test_detaches_only_images_in_the_build_directory(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            build = root / "kuri build"
            alias = root / "alias"
            build.mkdir()
            alias.symlink_to(build)
            selected = alias / "tmp.dmg"
            other = root / "kuri build other" / "tmp.dmg"
            mounted = {"images": [
                {"image-path": str(other), "system-entities": [{"dev-entry": "/dev/disk4"}]},
                {"image-path": str(selected), "system-entities": [{"dev-entry": "/dev/disk5"}]},
            ]}
            with patch("detach_build_images.subprocess.check_output", return_value=plistlib.dumps(mounted)):
                with patch("detach_build_images.subprocess.run", return_value=CompletedProcess([], 0)) as run:
                    self.assertTrue(detach_build_images(build))
                    run.assert_called_once_with(["hdiutil", "detach", "/dev/disk5"])

                errors = io.StringIO()
                with patch("detach_build_images.subprocess.run", return_value=CompletedProcess([], 1)):
                    with redirect_stderr(errors):
                        self.assertFalse(detach_build_images(build))
                self.assertIn(f"/dev/disk5 for {selected}", errors.getvalue())
