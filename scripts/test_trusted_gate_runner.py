# File metadata (manifest mirror):
# Purpose: Proves fixed-base controllers execute against the subject root while candidate noops, missing dependencies, dirty checkouts and conflicting identity arguments cannot silently pass.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-03
# Updated: 2026-11-03
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

RUNNER = Path(__file__).with_name('trusted_gate_runner.py')

class TrustedRunnerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.repo = Path(self.tmp.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.write('scripts/go_test_scope.py', 'VALUE = "trusted"\n')
        self.write('scripts/source_identity.py', 'VALUE = "identity"\n')
        self.write('scripts/check_fast.py', 'from go_test_scope import VALUE\nimport pathlib, os, sys\nprint("BASE", VALUE, pathlib.Path(__file__).resolve().parents[1], pathlib.Path.cwd(), os.environ.get("CAW_TRUSTED_GATE_RUNNER"), *sys.argv[1:])\n')
        self.write('scripts/verify.py', 'from source_identity import VALUE\nimport pathlib, sys\nprint("QUALITY", VALUE, pathlib.Path(__file__).resolve().parents[1], *sys.argv[1:])\n')
        self.write('scripts/architecture/check_architecture.py', 'from pathlib import Path\nprint("ARCH", Path(__file__).resolve().parent)\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'base')
        self.base = self.git('rev-parse', 'HEAD').strip()
        self.write('scripts/check_fast.py', 'print("CANDIDATE")\n')
        self.write('scripts/verify.py', 'print("CANDIDATE")\n')
        self.git('add', '.')
        self.git('commit', '-qm', 'subject')
        self.subject = self.git('rev-parse', 'HEAD').strip()
    def tearDown(self): self.tmp.cleanup()
    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.repo), *args], text=True)
    def write(self, name, contents):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)
    def run_gate(self, mode, *args, base=None, subject=None):
        return subprocess.run(['python3', str(RUNNER), mode, '--repo', str(self.repo), '--base', base or self.base, '--subject', subject or self.subject, '--', *args], text=True, capture_output=True)
    def test_base_controller_uses_subject_root_and_base_dependency(self):
        p = self.run_gate('fast', '--plan')
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn(f'BASE trusted {self.repo} {self.repo}', p.stdout)
        self.assertNotIn('CANDIDATE', p.stdout)
        self.assertIn('--plan', p.stdout)
    def test_quality_stages_share_subject_identity(self):
        for stage in ('static','web'):
            p = self.run_gate('quality', '--stage', stage)
            self.assertEqual(p.returncode, 0, p.stderr)
            self.assertIn(f'QUALITY identity {self.repo} --stage {stage}', p.stdout)
    def test_architecture_uses_candidate_config_directory(self):
        p = self.run_gate('architecture')
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertIn(f'ARCH {self.repo}/scripts/architecture', p.stdout)
    def test_wrong_sha_and_dirty_tree_rejected(self):
        p = self.run_gate('fast', subject=self.base)
        self.assertNotEqual(p.returncode, 0)
        self.write('scripts/check_fast.py', 'print(1)\n')
        p = self.run_gate('fast')
        self.assertNotEqual(p.returncode, 0)
    def test_identity_arguments_cannot_be_overridden(self):
        p = self.run_gate('fast', '--base', self.subject)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn('reserved', p.stderr)
        p = self.run_gate('fast', '--root', str(self.repo.parent))
        self.assertNotEqual(p.returncode, 0)
        self.assertIn('reserved', p.stderr)
        p = self.run_gate('architecture', '--fixture', 'anything')
        self.assertNotEqual(p.returncode, 0)
        self.assertIn('reserved', p.stderr)
    def test_missing_base_dependency_rejected(self):
        self.git('rm', '-q', 'scripts/go_test_scope.py')
        self.git('commit', '-qm', 'no dependency')
        missing = self.git('rev-parse', 'HEAD').strip()
        p = self.run_gate('fast', base=missing, subject=missing)
        self.assertNotEqual(p.returncode, 0)
        self.assertIn('go_test_scope', p.stderr)

if __name__ == '__main__': unittest.main()
