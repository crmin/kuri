import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


class BuildVersionTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.repo = Path(work.name) / "repo"
        self.scripts = self.repo / "deploy" / "macos"
        self.scripts.mkdir(parents=True)
        source = Path(__file__).resolve().parent
        shutil.copy(source / "build-version.sh", self.scripts)
        (self.scripts / "VERSION").write_text("v1.3.0\n")
        self.git("init", "-q")
        self.git("add", ".")
        self.git("commit", "-qm", "version")

        commands = Path(work.name) / "bin"
        commands.mkdir()
        date = commands / "date"
        date.write_text('#!/bin/sh\n[ "$1" = -u ] && [ "$2" = +%Y%m%d%H%M ] || exit 1\nprintf "202611272359\\n"\n')
        date.chmod(0o755)
        self.env = dict(os.environ, TZ="Asia/Seoul", PATH=f"{commands}:{os.environ['PATH']}")

    def git(self, *args):
        subprocess.run(["git", "-c", "user.name=kuri tests", "-c", "user.email=tests@example.invalid", "-C", self.repo, *args], check=True)

    def version(self):
        return subprocess.check_output(["bash", self.scripts / "build-version.sh"], env=self.env, text=True).strip()

    def test_release_uses_exact_tag(self):
        self.git("tag", "release/v1.4.0")
        self.assertEqual(self.version(), "v1.4.0")

    def test_development_uses_target_and_utc_minutes(self):
        self.git("tag", "release/v1.2.2")
        self.git("commit", "--allow-empty", "-qm", "development")
        self.assertEqual(self.version(), "v1.3.0-rc202611272359")

    def test_modified_source_builds_as_development(self):
        self.git("tag", "release/v1.3.0")
        (self.repo / "development.go").touch()
        self.assertEqual(self.version(), "v1.3.0-rc202611272359")
