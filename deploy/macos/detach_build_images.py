import plistlib
import subprocess
import sys
from pathlib import Path


def detach_build_images(build_dir):
    build_dir = build_dir.resolve()
    info = plistlib.loads(subprocess.check_output(["hdiutil", "info", "-plist"]))
    failed = False
    for image in info["images"]:
        path = image.get("image-path")
        if not path or not Path(path).resolve().is_relative_to(build_dir):
            continue

        # The first entity is the whole image. Detach also removes its volumes.
        device = image["system-entities"][0]["dev-entry"]
        if subprocess.run(["hdiutil", "detach", device]).returncode != 0:
            print(f"Could not detach {device} for {path}.", file=sys.stderr)
            failed = True
    return not failed


if __name__ == "__main__":
    sys.exit(0 if detach_build_images(Path(sys.argv[1])) else 1)
