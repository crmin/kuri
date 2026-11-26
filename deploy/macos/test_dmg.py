import os
import plistlib
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from ds_store import DSStore
from mac_alias import Alias


class DiskImageTest(unittest.TestCase):
    def test_dmg_contents_and_layout(self):
        subprocess.run(["hdiutil", "verify", str(image)], check=True, stdout=subprocess.DEVNULL)
        directory = tempfile.TemporaryDirectory(prefix="kuri-dmg-test-")
        self.addCleanup(directory.cleanup)
        mount = Path(directory.name) / "volume"
        mount.mkdir()
        attached = plistlib.loads(subprocess.check_output([
            "hdiutil", "attach", "-readonly", "-nobrowse", "-noautoopen", "-plist",
            "-mountpoint", str(mount), str(image),
        ]))
        device = attached["system-entities"][0]["dev-entry"]
        self.addCleanup(subprocess.run, ["hdiutil", "detach", device], check=True)

        app = mount / "kuri.app"
        self.assertTrue(stat.S_ISDIR(app.lstat().st_mode), "expected an actual app bundle")
        applications = mount / "Applications"
        self.assertTrue(applications.is_symlink())
        self.assertEqual(os.readlink(applications), "/Applications")

        contents = app / "Contents"
        info = plistlib.loads((contents / "Info.plist").read_bytes())
        self.assertEqual(info["CFBundleExecutable"], "kuri-launcher")
        self.assertTrue((contents / "Resources" / info["CFBundleIconFile"]).is_file())
        for name in ("kuri", info["CFBundleExecutable"]):
            binary = contents / "MacOS" / name
            self.assertTrue(os.access(binary, os.X_OK), f"{name} is not executable")
            arches = subprocess.check_output(["lipo", "-archs", str(binary)], text=True)
            self.assertEqual(set(arches.split()), {"arm64", "x86_64"}, name)

        layout = mount / ".DS_Store"
        self.assertTrue(layout.is_file(), "missing Finder layout")
        with DSStore.open(str(layout), "r") as store:
            self.assertEqual(store["."]["icvl"], (b"type", b"icnv"))
            self.assertEqual(store["kuri.app"]["Iloc"], (150, 150))
            self.assertEqual(store["Applications"]["Iloc"], (450, 150))
            options = store["."]["icvp"]
            self.assertEqual(options["backgroundType"], 2)
            background = Alias.from_bytes(options["backgroundImageAlias"])
            self.assertEqual(background.target.filename, ".background.png")
        self.assertTrue((mount / ".background.png").is_file())


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(f"Usage: {sys.argv[0]} <kuri.dmg>")
    image = Path(sys.argv.pop(1)).resolve(strict=True)
    unittest.main()
