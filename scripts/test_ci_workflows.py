"""Check the executable workflow syntax, including expression context availability."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class WorkflowSyntaxTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.linter = shutil.which('actionlint')
        if not cls.linter:
            raise RuntimeError('required pinned actionlint tool is unavailable')

    def test_all_repository_workflows_are_valid(self):
        paths = sorted((ROOT / '.github/workflows').glob('*.yml'))
        self.assertTrue(paths)
        result = subprocess.run([self.linter, '-shellcheck=', '-pyflakes=', *map(str, paths)],
                                cwd=ROOT, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_job_level_runner_context_is_rejected_but_step_context_is_valid(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'workflow.yml'
            path.write_text('on: workflow_dispatch\njobs:\n  probe:\n    runs-on: ubuntu-latest\n'
                            '    env:\n      OUT: ${{ runner.temp }}/out\n'
                            '    steps:\n      - run: echo ready\n')
            bad = subprocess.run([self.linter, '-shellcheck=', '-pyflakes=', str(path)],
                                 capture_output=True, text=True)
            self.assertNotEqual(bad.returncode, 0)
            self.assertIn('not allowed', bad.stdout + bad.stderr)
            path.write_text('on: workflow_dispatch\njobs:\n  probe:\n    runs-on: ubuntu-latest\n'
                            '    steps:\n      - run: echo ready\n'
                            '        env:\n          OUT: ${{ runner.temp }}/out\n')
            good = subprocess.run([self.linter, '-shellcheck=', '-pyflakes=', str(path)],
                                  capture_output=True, text=True)
            self.assertEqual(good.returncode, 0, good.stdout + good.stderr)


if __name__ == '__main__':
    unittest.main()
