"""Minimal temporary-Git integration fixtures for the external gate adapter."""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from dataclasses import replace

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import gate_adapter as ga
import manifest_tool as mt
import legacy_bootstrap as lb
from git_inventory import GitRepo, GitInventoryError, manifest_self_updated
from metadata_core import STYLES, insert_header, metadata_body

SOURCE = HERE.parents[1]


class GateAdapterFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.schema = json.loads((SOURCE / 'scripts/file_metadata/schema.json').read_text())
        cls.policy = json.loads((SOURCE / 'scripts/file_metadata/policy.json').read_text())
        cls.source_manifest = json.loads((SOURCE / 'scripts/file_metadata/manifest.json').read_text())
        cls.policy_obj = mt.Policy(cls.policy)
        cls.file_rec = json.loads(json.dumps(cls.source_manifest['files']['scripts/check_fast.py']))
        cls.registry_records = {
            path: json.loads(json.dumps(cls.source_manifest['files'][path]))
            for path in ('scripts/file_metadata/manifest.json', 'scripts/file_metadata/policy.json',
                         'scripts/file_metadata/schema.json', 'docs/engineering/file-metadata.md',
                         'scripts/check_fast.py')
        }
        # Fixture revisions belong to its synthetic history, independently of
        # how often the source repository's canonical inventory was refreshed.
        cls.file_rec['revision'] = 'r1'
        for record in cls.registry_records.values():
            record['revision'] = 'r1'

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='file-meta-gate-fixture-')
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.base_env = os.environ.copy()
        self.base_env.update({'GIT_AUTHOR_NAME':'Fixture','GIT_AUTHOR_EMAIL':'fixture@example.invalid',
                              'GIT_COMMITTER_NAME':'Fixture','GIT_COMMITTER_EMAIL':'fixture@example.invalid',
                              'GIT_AUTHOR_DATE':'2024-01-01T00:00:00+00:00',
                              'GIT_COMMITTER_DATE':'2024-01-01T00:00:00+00:00'})
        self.git('init', '-q')
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        (self.repo / 'scripts').mkdir()
        self.source_path = self.repo / 'scripts/check.py'
        self.source_path.write_bytes(b'print("root")\n')
        check_fast = self.repo / 'scripts/check_fast.py'
        check_fast.write_text('def check_fast(): return True\n')
        fast_rule = self.policy_obj.classify('scripts/check_fast.py')
        fast_kind, fast_style, _ = mt._native(fast_rule, 'scripts/check_fast.py')
        check_fast.write_bytes(insert_header(check_fast.read_bytes(), self.file_rec, style=fast_style,
            handling=fast_rule['handling'], file_kind=fast_kind, mode='100644'))
        self.git('add', 'scripts/check.py', 'scripts/check_fast.py')
        self.commit('introduce source')
        self.root_sha = self.head()
        self.git('checkout', '-qb', 'legacy-origin')
        subprocess.run(['git', '-C', str(self.repo), 'commit', '--allow-empty', '-qm', 'pre-squash legacy identity'],
            check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.base_env)
        self.legacy_sha = self.head()
        self.git('checkout', '-q', self.root_sha)
        # A source commit used by the reviewed BASE is intentionally on a side
        # branch and is not an ancestor of BASE (the PR was later squash-merged).
        self.git('checkout', '-qb', 'source-side')
        self.source_path.write_bytes(b'print("squashed source")\n')
        self.git('add', 'scripts/check.py')
        self.commit('edit source on pre-squash branch', '2024-02-03T04:05:06+00:00')
        self.side_sha = self.head()
        self.git('checkout', '-q', self.root_sha)
        self.source_path.write_bytes(b'print("squashed source")\n')
        self.record = json.loads(json.dumps(self.file_rec))
        self.record['purpose'] = 'Fixture module prints a fixed value and demonstrates trusted squash provenance inheritance.'
        path_rule = self.policy_obj.classify('scripts/check.py')
        ev = GitRepo(self.repo).path_provenance('scripts/check.py', subject=self.root_sha,
            style=STYLES['slash'], handling=path_rule['handling'], file_kind='python')
        # Replace created with actual root evidence and updated with a real but
        # unreachable side-branch source whose normalized body matches BASE.
        self.record['created'] = {'value': ev.created.value, 'source': ev.created.source}
        side_ev = GitRepo(self.repo).path_provenance('scripts/check.py', subject=self.side_sha,
            style=STYLES['hash'], handling=path_rule['handling'], file_kind='python')
        self.record['updated'] = {'value': side_ev.updated.value, 'source': side_ev.updated.source}
        self.record['revision'] = 'r1'
        self.record['handling'] = 'header-and-manifest'
        self.record['header_state'] = 'pending-header-application'
        self.write_repo_inputs()
        self.git('add', '.')
        self.commit('canonical metadata anchor fixture', '2024-03-03T00:00:00+00:00')
        self.anchor_sha = self.head()
        # Base snapshot contains a valid header from the prior reviewed merge.
        self.source_path.write_bytes(insert_header(self.source_path.read_bytes(), self.record,
            style=STYLES['hash'], handling='header-and-manifest', file_kind='python', mode='100644'))
        self.git('add', '.')
        self.commit('trusted reviewed base', '2024-03-04T00:00:00+00:00')
        self.base_sha = self.head()
        self.make_candidate_header_only()
        self.git('add', '.')
        self.commit('metadata-only candidate update', '2024-03-05T00:00:00+00:00')
        self.subject_sha = self.head()
        self.manifest_path = self.repo / ga.MANIFEST_PATH
        self.schema_path = self.repo / ga.SCHEMA_PATH
        self.policy_path = self.repo / ga.POLICY_PATH

    def git(self, *args):
        return subprocess.run(['git', '-C', str(self.repo), *args], check=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.base_env)

    def commit(self, message, timestamp='2024-01-01T00:00:00+00:00'):
        env = dict(self.base_env, GIT_AUTHOR_DATE=timestamp, GIT_COMMITTER_DATE=timestamp)
        subprocess.run(['git', '-C', str(self.repo), 'commit', '-qm', message], check=True,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)

    def head(self):
        return subprocess.check_output(['git', '-C', str(self.repo), 'rev-parse', 'HEAD'], text=True).strip()

    def write_repo_inputs(self):
        meta = self.repo / 'scripts/file_metadata'
        meta.mkdir(parents=True, exist_ok=True)
        docs = self.repo / 'docs/engineering/file-metadata.md'
        docs.parent.mkdir(parents=True, exist_ok=True)
        docs.write_text('# Fixture metadata policy\n')
        policy = json.loads(json.dumps(self.policy))
        policy['manifest_status'] = ga.INITIALIZED_POLICY_STATUS
        base_schema = json.loads(json.dumps(self.schema))
        base_schema['$defs']['record']['properties']['header_state']['enum'] = [
            'pending-header-application', 'manifest-only']
        (meta / 'schema.json').write_text(json.dumps(base_schema, ensure_ascii=False, indent=2) + '\n')
        (meta / 'policy.json').write_text(json.dumps(policy, ensure_ascii=False, indent=2) + '\n')
        self.policy = policy
        manifest = self.empty_manifest()
        manifest['files']['scripts/check.py'] = json.loads(json.dumps(self.record))
        for path, rec in self.registry_records.items():
            copied = json.loads(json.dumps(rec))
            if path == ga.MANIFEST_PATH:
                copied['updated'] = manifest_self_updated().__dict__
            manifest['files'][path] = copied
        docs_record = manifest['files']['docs/engineering/file-metadata.md']
        docs_rule = self.policy_obj.classify('docs/engineering/file-metadata.md')
        docs_kind, docs_style, _ = mt._native(docs_rule, 'docs/engineering/file-metadata.md')
        if docs_rule['handling'] == 'header-and-manifest':
            docs.write_bytes(insert_header(docs.read_bytes(), docs_record, style=docs_style,
                handling=docs_rule['handling'], file_kind=docs_kind, mode='100644'))
        (meta / 'manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')

    def empty_manifest(self):
        return {
            'schema_version': self.source_manifest['schema_version'],
            'inventory': {'baseline_head': self.root_sha,
                'baseline_timestamp': '2024-01-01T00:00:00+00:00',
                'tracked_file_count': 2,
                'explicit_additions': list(ga.LEGACY_EXPLICIT_ADDITIONS_ORDER),
                'excluded_untracked_paths': []},
            'files': {},
        }

    def make_candidate_header_only(self):
        self.record['purpose'] = 'Fixture module prints a fixed value and demonstrates audited metadata-only header updates.'
        self.source_path.write_bytes(insert_header(b'print("squashed source")\n', self.record,
            style=STYLES['hash'], handling='header-and-manifest', file_kind='python', mode='100644'))
        manifest_path = self.repo / ga.MANIFEST_PATH
        manifest = json.loads(manifest_path.read_text())
        manifest['files']['scripts/check.py'] = json.loads(json.dumps(self.record))
        manifest['files'][ga.MANIFEST_PATH]['revision'] = 'r2'
        manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')

    def run_check(self):
        return ga.check(self.repo, self.base_sha, self.subject_sha,
                        self.manifest_path, self.schema_path, self.policy_path)

    def test_squashed_old_source_inherits_from_canonical_base_and_header_is_metadata_only(self):
        result = self.run_check()
        self.assertEqual(result['status'], 'PASS')
        self.assertEqual(result['metadata_only_normalized_paths'], ['scripts/check.py'])
        self.assertTrue(any(x['path'] == 'scripts/check.py' and x['field'] == 'updated' and
                            x['source'] == 'inherited-from-reviewed-base' for x in result['inherited_base_fields']))
        self.assertNotEqual(self.side_sha, self.base_sha)
        self.assertNotIn(self.side_sha, subprocess.check_output(
            ['git', '-C', str(self.repo), 'rev-list', self.base_sha], text=True).splitlines())

    def test_changed_unreachable_old_source_is_not_accepted_as_inherited(self):
        manifest = json.loads(self.manifest_path.read_text())
        evidence = manifest['files']['scripts/check.py']['updated']
        evidence['source']['commit'] = 'f' * 40
        manifest['files']['scripts/check.py']['updated'] = evidence
        # Header still reflects the original record; first failure may be that
        # mirror mismatch, which is still fail-closed. Restore mirror to prove
        # changed source fields themselves cannot inherit.
        changed = manifest['files']['scripts/check.py']
        self.source_path.write_bytes(insert_header(b'print("squashed source")\n', changed,
            style=STYLES['hash'], handling='header-and-manifest', file_kind='python', mode='100644'))
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        with self.assertRaises(ga.GateError):
            self.run_check()

    def test_initialized_base_rejects_legacy_downgrade(self):
        policy = json.loads(self.policy_path.read_text())
        policy['manifest_status'] = ga.LEGACY_POLICY_STATUS
        self.policy_path.write_text(json.dumps(policy, ensure_ascii=False, indent=2) + '\n')
        with self.assertRaisesRegex(ga.GateError, 'downgrades or changes'):
            self.run_check()

    def test_initialized_policy_allows_only_new_literal_manifest_paths(self):
        base = json.loads(json.dumps(self.policy))
        candidate = json.loads(json.dumps(base))
        rule = {'id': 'new-csv', 'paths': ['docs/architecture/store-ownership.csv'],
                'handling': 'manifest-only', 'reason': 'CSV has no comment syntax.'}
        candidate['file_classes'].append(rule)
        base_tree = {'scripts/check.py': None}
        candidate_tree = {**base_tree, rule['paths'][0]: None}
        ga._validate_initialized_policy_delta(base, candidate, base_tree, candidate_tree)
        with self.assertRaises(ga.GateError):
            ga._validate_initialized_policy_delta(base, candidate, base_tree, base_tree)
        for path in ('scripts/check.py', 'docs/**/*.csv', 'web/e2e/fixtures/new.py', '../escape.csv'):
            altered = json.loads(json.dumps(candidate))
            altered['file_classes'][-1]['paths'] = [path]
            with self.subTest(path=path), self.assertRaises(ga.GateError):
                ga._validate_initialized_policy_delta(base, altered, base_tree, {**candidate_tree, path: None})
        altered = json.loads(json.dumps(candidate))
        altered['file_classes'][0]['handling'] = 'header-and-manifest'
        with self.assertRaises(ga.GateError):
            ga._validate_initialized_policy_delta(base, altered, base_tree, candidate_tree)
        altered = json.loads(json.dumps(candidate))
        altered['revision']['format'] = 'anything'
        with self.assertRaises(ga.GateError):
            ga._validate_initialized_policy_delta(base, altered, base_tree, candidate_tree)

    def test_initialized_schema_is_fixed(self):
        schema = json.loads(self.schema_path.read_text())
        schema['$defs']['record']['properties']['purpose'].pop('minLength', None)
        self.schema_path.write_text(json.dumps(schema, ensure_ascii=False, indent=2) + '\n')
        with self.assertRaisesRegex(ga.GateError, 'schema'):
            self.run_check()

    def test_check_requires_full_fixed_sha_and_rejects_untracked_scope_gap(self):
        with self.assertRaisesRegex(ga.GateError, 'full lowercase Git commit SHA'):
            ga.check(self.repo, 'HEAD', self.subject_sha,
                     self.manifest_path, self.schema_path, self.policy_path)
        extra = self.repo / 'scripts/new_untracked.py'
        extra.write_text('print("untracked")\n')
        with self.assertRaisesRegex(ga.GateError, 'coverage mismatch'):
            self.run_check()

    def test_canonical_self_record_cannot_claim_a_known_updated_source(self):
        manifest = json.loads(self.manifest_path.read_text())
        manifest['files'][ga.MANIFEST_PATH]['updated'] = json.loads(json.dumps(self.record['updated']))
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        with self.assertRaisesRegex(mt.ManifestError, 'exact canonical self-reference unknown'):
            self.run_check()

    def test_dirty_body_change_cannot_borrow_committed_history(self):
        self.source_path.write_bytes(b'print("uncommitted body")\n')
        with self.assertRaises(ga.GateError):
            self.run_check()

    def test_apply_plan_is_hash_bound_and_dry_run_default(self):
        # Recreate BASE without an existing header so the explicit plan can
        # demonstrate a real insertion against a fixed subject body.
        self.source_path.write_bytes(b'print("squashed source")\n')
        self.git('add', 'scripts/check.py')
        self.commit('remove header for application fixture', '2024-03-06T00:00:00+00:00')
        subject = self.head()
        plan = ga._plan_header_apply(self.repo, self.base_sha, subject, self.manifest_path,
                                     self.schema_path, self.policy_path, ['scripts/check.py'])
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        dry = ga.apply_header_plan(output, digest, apply=False)
        self.assertEqual(dry['status'], 'DRY_RUN')
        with self.assertRaisesRegex(ga.GateError, 'plan hash mismatch'):
            ga.apply_header_plan(output, '0' * 64, apply=True)
        approved_mode = stat.S_IMODE(self.source_path.stat().st_mode)
        self.assertEqual(plan['changes'][0]['expected_current_mode'], approved_mode)
        applied = ga.apply_header_plan(output, digest, apply=True)
        self.assertEqual(applied['status'], 'APPLIED')
        self.assertEqual(stat.S_IMODE(self.source_path.stat().st_mode), approved_mode)
        self.assertTrue(Path(applied['backup_dir'], 'index.json').is_file())
        backup_index = json.loads(Path(applied['backup_dir'], 'index.json').read_text())
        self.assertEqual(backup_index[0]['path'], 'scripts/check.py')
        backup_bytes = Path(applied['backup_dir'], backup_index[0]['backup']).read_bytes()
        self.assertEqual(hashlib.sha256(backup_bytes).hexdigest(), backup_index[0]['sha256'])
        self.assertIn(b'File metadata (manifest mirror):', self.source_path.read_bytes())

    def test_apply_rejects_symlinked_parent_and_preserves_target(self):
        plan = ga._plan_header_apply(self.repo, self.base_sha, self.subject_sha,
            self.manifest_path, self.schema_path, self.policy_path, ['scripts/check.py'])
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        original = self.source_path.read_bytes()
        moved = self.repo / 'scripts-moved'
        (self.repo / 'scripts').rename(moved)
        (self.repo / 'scripts').symlink_to(moved, target_is_directory=True)
        with self.assertRaisesRegex(ga.GateError, 'parent path.*symlinked'):
            ga.apply_header_plan(output, digest, apply=True)
        self.assertEqual((moved / 'check.py').read_bytes(), original)

    def test_apply_rechecks_raw_hash_after_plan_creation(self):
        plan = ga._plan_header_apply(self.repo, self.base_sha, self.subject_sha,
            self.manifest_path, self.schema_path, self.policy_path, ['scripts/check.py'])
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        self.source_path.write_bytes(b'changed concurrently\n')
        with self.assertRaisesRegex(ga.GateError, 'raw file changed after plan creation'):
            ga.apply_header_plan(output, digest, apply=True)

    def test_apply_rejects_full_mode_change_even_when_git_mode_is_unchanged(self):
        self.source_path.chmod(0o644)
        plan = ga._plan_header_apply(self.repo, self.base_sha, self.subject_sha,
            self.manifest_path, self.schema_path, self.policy_path, ['scripts/check.py'])
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        before = self.source_path.read_bytes()
        for mode in (0o600, 0o666):
            with self.subTest(mode=oct(mode)):
                self.source_path.chmod(mode)
                with self.assertRaisesRegex(ga.GateError, 'full file mode changed'):
                    ga.apply_header_plan(output, digest, apply=True)
                self.assertEqual(self.source_path.read_bytes(), before)
                self.assertEqual(stat.S_IMODE(self.source_path.stat().st_mode), mode)
        self.source_path.chmod(0o644)

    def test_apply_preserves_nondefault_approved_full_mode(self):
        self.source_path.chmod(0o600)
        plan = ga._plan_header_apply(self.repo, self.base_sha, self.subject_sha,
            self.manifest_path, self.schema_path, self.policy_path, ['scripts/check.py'])
        self.assertEqual(plan['changes'][0]['expected_current_mode'], 0o600)
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        ga.apply_header_plan(output, digest, apply=True)
        self.assertEqual(stat.S_IMODE(self.source_path.stat().st_mode), 0o600)

    def test_apply_rechecks_full_mode_before_each_replacement(self):
        first = self.source_path
        second = self.repo / 'scripts/check_fast.py'
        first.chmod(0o644)
        second.chmod(0o644)
        original_first, original_second = first.read_bytes(), second.read_bytes()
        plan = ga._plan_header_apply(self.repo, self.base_sha, self.subject_sha,
            self.manifest_path, self.schema_path, self.policy_path, ['scripts/check.py', 'scripts/check_fast.py'])
        output = self.repo / 'apply-plan.json'
        output.write_text(json.dumps(plan, sort_keys=True) + '\n')
        digest = hashlib.sha256(output.read_bytes()).hexdigest()
        real_replace = os.replace
        def change_second_before_first_replacement(src, dst):
            if Path(dst) == first:
                second.chmod(0o666)
            return real_replace(src, dst)
        with mock.patch.object(ga.os, 'replace', side_effect=change_second_before_first_replacement):
            with self.assertRaisesRegex(ga.GateError, 'full mode changed before replacement'):
                ga.apply_header_plan(output, digest, apply=True)
        self.assertEqual(first.read_bytes(), original_first)
        self.assertEqual(second.read_bytes(), original_second)
        self.assertEqual(stat.S_IMODE(first.stat().st_mode), 0o644)
        self.assertEqual(stat.S_IMODE(second.stat().st_mode), 0o666)

    def prepare_legacy_foundation(self, *, delete_candidate=(), empty_candidate=(), weaken_policy=False,
                                 weaken_schema=False, prune_legacy=False, inherited_mismatch=False):
        old_legacy_marker = ga.LEGACY_INVENTORY_BASELINE
        old_anchor_marker = ga.LEGACY_CANONICAL_ANCHOR
        old_count = ga.LEGACY_BASE_TRACKED_COUNT
        old_legacy_tree_oid = ga.LEGACY_BASE_TREE_OID
        old_tree_oid = ga.LEGACY_ANCHOR_TREE_OID
        old_manifest_oid = ga.LEGACY_ANCHOR_MANIFEST_OID
        ga.LEGACY_INVENTORY_BASELINE = self.legacy_sha
        ga.LEGACY_CANONICAL_ANCHOR = self.anchor_sha
        ga.LEGACY_BASE_TRACKED_COUNT = 2
        ga.LEGACY_BASE_TREE_OID = GitRepo(self.repo).commit_identity(self.legacy_sha)[1]
        self.addCleanup(setattr, ga, 'LEGACY_INVENTORY_BASELINE', old_legacy_marker)
        self.addCleanup(setattr, ga, 'LEGACY_CANONICAL_ANCHOR', old_anchor_marker)
        self.addCleanup(setattr, ga, 'LEGACY_BASE_TRACKED_COUNT', old_count)
        self.addCleanup(setattr, ga, 'LEGACY_BASE_TREE_OID', old_legacy_tree_oid)
        self.addCleanup(setattr, ga, 'LEGACY_ANCHOR_TREE_OID', old_tree_oid)
        self.addCleanup(setattr, ga, 'LEGACY_ANCHOR_MANIFEST_OID', old_manifest_oid)
        policy = json.loads(self.policy_path.read_text())
        policy['manifest_status'] = ga.LEGACY_POLICY_STATUS
        self.policy_path.write_text(json.dumps(policy, ensure_ascii=False, indent=2) + '\n')
        manifest = json.loads(self.manifest_path.read_text())
        manifest['inventory']['baseline_head'] = self.legacy_sha
        if inherited_mismatch:
            record = manifest['files']['scripts/check.py']
            record['handling'] = 'manifest-only'
            record['header_state'] = 'manifest-only'
            record['exception'] = {'rule_id': 'strict-json', 'source': 'scripts/check.py',
                                   'reason': 'Inherited fixture classification pending explicit reconciliation.'}
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        self.source_path.write_bytes(b'print("root")\n')
        self.git('add', '.')
        self.commit('squash-equivalent reviewed legacy anchor', '2024-03-06T00:00:00+00:00')
        self.anchor_sha = self.head()
        ga.LEGACY_CANONICAL_ANCHOR = self.anchor_sha
        anchor_repo = GitRepo(self.repo)
        ga.LEGACY_ANCHOR_TREE_OID = anchor_repo.commit_identity(self.anchor_sha)[1]
        ga.LEGACY_ANCHOR_MANIFEST_OID = anchor_repo._tree_entry_for_path(
            self.anchor_sha, ga.MANIFEST_PATH.encode())[2]
        with self.assertRaises(GitInventoryError):
            anchor_repo.run('merge-base', '--is-ancestor', self.legacy_sha, self.anchor_sha)
        lb.verify_bridge_trees(anchor_repo.tree_inventory(self.legacy_sha), anchor_repo.tree_inventory(self.anchor_sha))
        # This target is after the trusted anchor and has a real rename/addition
        # drift that the old canonical registry does not claim to cover.
        old_path = self.repo / 'scripts/check.py'
        renamed = self.repo / 'scripts/renamed.py'
        old_path.rename(renamed)
        (self.repo / 'scripts/after-anchor.py').write_text('print("post anchor")\n')
        self.git('add', '-A')
        self.commit('legacy current-tree drift fixture', '2024-03-06T12:00:00+00:00')
        self.base_sha = self.head()
        for path in sorted(ga.FOUNDATION_TOOL_PATHS - {'scripts/check_fast.py'}):
            if path in delete_candidate:
                continue
            target = self.repo / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(f'"""fixture tool {path}"""\n')
        if weaken_policy:
            policy['file_classes'][0]['handling'] = 'header-and-manifest'
        else:
            policy['file_classes'].append({
                'id': 'foundation-fixture-tool', 'paths': ['scripts/file_metadata/gate_adapter.py'],
                'handling': 'manifest-only', 'reason': 'Additive foundation fixture class.'})
        (self.repo / ga.POLICY_PATH).write_text(json.dumps(policy, ensure_ascii=False, indent=2) + '\n')
        schema = json.loads(self.schema_path.read_text())
        schema['$defs']['record']['properties']['header_state']['enum'].extend([
            'upstream-notice-preserved', 'generator-owned-output'])
        if weaken_schema:
            schema['$defs']['record']['properties']['purpose'].pop('minLength', None)
        self.schema_path.write_text(json.dumps(schema, ensure_ascii=False, indent=2) + '\n')
        if 'docs/engineering/file-metadata.md' in delete_candidate:
            (self.repo / 'docs/engineering/file-metadata.md').unlink()
        for path in empty_candidate:
            (self.repo / path).write_bytes(b'')
        self.git('add', '.')
        self.commit('reviewed additive foundation fixture', '2024-03-07T00:00:00+00:00')
        self.subject_sha = self.head()
        if prune_legacy:
            self.git('branch', '-D', 'legacy-origin')
            self.git('reflog', 'expire', '--expire=now', '--all')
            self.git('gc', '--prune=now')
            absent = subprocess.run(['git', '-C', str(self.repo), 'cat-file', '-e',
                f'{self.legacy_sha}^{{commit}}'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertNotEqual(absent.returncode, 0, 'fixture should prune the off-main legacy commit object')
        return self.run_check()

    def test_legacy_base_is_explicit_not_run_and_reports_real_tree_drift(self):
        result = self.prepare_legacy_foundation()
        self.assertEqual(result['status'], 'NOT_RUN')
        self.assertEqual(result['full_metadata_enforcement'], 'NOT_RUN')
        self.assertIn('scripts/renamed.py', result['base_legacy_missing'])
        self.assertIn('scripts/check.py', result['base_legacy_stale'])
        self.assertIn('scripts/after-anchor.py', result['base_legacy_missing'])
        self.assertIn('scripts/check.py->scripts/renamed.py', result['legacy_historical_renames'])
        new_tools = ga.FOUNDATION_TOOL_PATHS - {'scripts/check_fast.py'}
        self.assertTrue(new_tools <= set(result['candidate_registry_missing']))
        self.assertIn('scripts/check_fast.py', self.source_manifest['files'])
        self.assertTrue(new_tools <= set(result['candidate_additions']))

    def test_legacy_foundation_reports_inherited_semantic_mismatch_as_not_run(self):
        result = self.prepare_legacy_foundation(inherited_mismatch=True)
        self.assertEqual(result['status'], 'NOT_RUN')
        self.assertEqual(result['full_metadata_enforcement'], 'NOT_RUN')
        self.assertIn({'path':'scripts/check.py', 'reason':'scripts/check.py: handling differs from policy'},
                      result['deferred_legacy_record_validation'])
        # The immutable canonical legacy blob must still be retained exactly.
        self.assertEqual(self.manifest_path.read_bytes(),
                         subprocess.check_output(['git','-C',str(self.repo),'show',self.base_sha+':'+ga.MANIFEST_PATH]))

    def test_legacy_transition_uses_reachable_anchor_after_off_main_object_pruned(self):
        result = self.prepare_legacy_foundation(prune_legacy=True)
        self.assertEqual(result['status'], 'NOT_RUN')
        self.assertEqual(result['legacy_bridge_evidence'], 'reviewed-anchor-attestation')

    def test_bootstrap_accepts_only_reconstructed_manifest_and_metadata_only_tree(self):
        self.prepare_legacy_foundation()
        base = self.subject_sha
        repo = GitRepo(self.repo)
        tree = repo.tree_inventory(base)
        base_policy = json.loads((self.repo / ga.POLICY_PATH).read_text())
        policy = json.loads(json.dumps(base_policy))
        policy['manifest_status'] = ga.INITIALIZED_POLICY_STATUS
        schema_bytes = (self.repo / ga.SCHEMA_PATH).read_bytes()
        candidate = {'files':{path:json.loads(json.dumps(self.file_rec)) for path in tree}}
        reconstructed = json.loads(json.dumps(candidate))
        audit = {'report':{'status':'PASS','failures':[]}}
        with mock.patch.object(lb, 'bootstrap', return_value=(reconstructed,audit,{'policy':json.loads(json.dumps(policy))})), \
             mock.patch.object(mt, 'validate_schema'), mock.patch.object(mt, 'validate_record'), \
             mock.patch.object(ga, '_exact_record_header'):
            result = ga._validate_bootstrap_transition(repo,base,base,base_policy,schema_bytes,candidate,policy,tree,require_headers=True)
            self.assertEqual(result['status'], 'PASS')
            forged = json.loads(json.dumps(candidate))
            forged['files']['scripts/check_fast.py']['purpose'] = 'tampered reviewed attribution'
            with self.assertRaisesRegex(ga.GateError, 'manifest differs'):
                ga._validate_bootstrap_transition(repo,base,base,base_policy,schema_bytes,forged,policy,tree,require_headers=True)
            altered = dict(tree)
            original = altered['scripts/check_fast.py']
            altered['scripts/check_fast.py'] = replace(original, mode='100755' if original.mode == '100644' else '100644')
            with self.assertRaisesRegex(ga.GateError, 'changed mode'):
                ga._validate_bootstrap_transition(repo,base,base,base_policy,schema_bytes,candidate,policy,altered,require_headers=True)

    def test_bootstrap_requires_trusted_base_permission_and_unchanged_tool(self):
        self.prepare_legacy_foundation()
        base = self.subject_sha
        repo = GitRepo(self.repo)
        base_tree = repo.tree_inventory(base)
        base_policy = json.loads((self.repo / ga.POLICY_PATH).read_text())
        candidate_policy = json.loads(json.dumps(base_policy))
        candidate_policy['manifest_status'] = ga.INITIALIZED_POLICY_STATUS
        schema_bytes = (self.repo / ga.SCHEMA_PATH).read_bytes()
        without_permission = dict(base_policy)
        without_permission.pop('reviewed_bootstrap', None)
        with self.assertRaisesRegex(ga.GateError, 'does not authorize'):
            ga._validate_bootstrap_transition(repo, base, base, without_permission, schema_bytes, {}, candidate_policy, base_tree, require_headers=False)
        path = 'scripts/file_metadata/gate_adapter.py'
        tampered = dict(base_tree)
        original = tampered[path]
        tampered[path] = replace(original, content=original.content + b'\nmalicious_code()\n', oid='f'*40)
        with self.assertRaisesRegex(ga.GateError, 'cannot change the trusted tool body'):
            ga._validate_bootstrap_transition(repo, base, base, base_policy, schema_bytes, {}, candidate_policy, tampered, require_headers=False)
        tampered[path] = replace(original, mode='100755' if original.mode=='100644' else '100644')
        with self.assertRaisesRegex(ga.GateError, 'changed mode'):
            ga._validate_bootstrap_transition(repo, base, base, base_policy, schema_bytes, {}, candidate_policy, tampered, require_headers=False)

    def test_legacy_transition_rejects_missing_new_tool_or_removed_base_governance(self):
        with self.assertRaisesRegex(ga.GateError, 'lacks required canonical/tool objects'):
            self.prepare_legacy_foundation(delete_candidate={'scripts/file_metadata/gate_adapter.py'})

    def test_legacy_transition_rejects_deleting_base_governance_object(self):
        with self.assertRaisesRegex(ga.GateError, 'deleted existing governance objects'):
            self.prepare_legacy_foundation(delete_candidate={'docs/engineering/file-metadata.md'})

    def test_legacy_transition_rejects_empty_existing_governance_object(self):
        with self.assertRaisesRegex(ga.GateError, 'replaced existing governance objects with empty content'):
            self.prepare_legacy_foundation(empty_candidate={'docs/engineering/file-metadata.md'})

    def test_legacy_transition_rejects_policy_weakening_but_accepts_additions(self):
        with self.assertRaisesRegex(ga.GateError, 'cannot remove or weaken an existing file class'):
            self.prepare_legacy_foundation(weaken_policy=True)

    def test_foundation_accepts_only_exact_glossary_generated_catalog(self):
        base = json.loads(json.dumps(self.policy_data)) if hasattr(self, 'policy_data') else json.loads(json.dumps(self.policy))
        base['file_classes'] = [x for x in base['file_classes'] if x['id'] != lb.GLOSSARY_VIEWS_RULE['id']]
        candidate = json.loads(json.dumps(base))
        candidate['file_classes'].insert(0, json.loads(json.dumps(lb.GLOSSARY_VIEWS_RULE)))
        ga._validate_foundation_delta(base, candidate, self.schema, self.schema)
        candidate['file_classes'][0]['inputs'] = ['other.json']
        with self.assertRaisesRegex(ga.GateError, 'foundation may add only literal classes'):
            ga._validate_foundation_delta(base, candidate, self.schema, self.schema)

    def test_foundation_literal_marker_catalog_is_exact(self):
        base = json.loads(json.dumps(self.policy))
        base['file_classes'] = [x for x in base['file_classes'] if x['id'] != lb.LITERAL_MARKER_RULE['id']]
        candidate = json.loads(json.dumps(base))
        candidate['file_classes'].insert(0, json.loads(json.dumps(lb.LITERAL_MARKER_RULE)))
        ga._validate_foundation_delta(base, candidate, self.schema, self.schema)
        candidate['file_classes'][0]['paths'].append('scripts/file_metadata/other.py')
        with self.assertRaisesRegex(ga.GateError, 'exact reviewed rule'):
            ga._validate_foundation_delta(base, candidate, self.schema, self.schema)
        candidate['file_classes'][0]['paths'] = ['scripts/file_metadata/metadata_core.py']
        with self.assertRaisesRegex(ga.GateError, 'exact reviewed rule'):
            ga._validate_foundation_delta(base, candidate, self.schema, self.schema)

    def test_legacy_transition_rejects_schema_constraint_weakening(self):
        with self.assertRaisesRegex(ga.GateError, 'schema changed constraints outside additive reviewed enum values'):
            self.prepare_legacy_foundation(weaken_schema=True)

    def test_check_rejects_noncanonical_candidate_manifest_override(self):
        shadows = []
        for name, source in (('shadow-manifest.json', self.manifest_path),
                             ('shadow-schema.json', self.schema_path), ('shadow-policy.json', self.policy_path)):
            shadow = self.repo / name
            shadow.write_bytes(source.read_bytes())
            shadows.append((name, shadow))
        for name, shadow in shadows:
            args = {'manifest': self.manifest_path, 'schema': self.schema_path, 'policy': self.policy_path}
            args[name.split('-')[1].split('.')[0]] = shadow
            with self.subTest(name=name), self.assertRaisesRegex(ga.GateError, 'path must be canonical'):
                ga.check(self.repo, self.base_sha, self.subject_sha,
                    args['manifest'], args['schema'], args['policy'])

    def test_dirty_mode_change_cannot_claim_a_committed_revision(self):
        self.source_path.chmod(0o755)
        with self.assertRaisesRegex(ga.GateError, 'uncommitted source body/mode changes'):
            self.run_check()

    def test_committed_mode_change_increments_revision_and_inherits_body_date(self):
        rule = self.policy_obj.classify('scripts/check.py')
        kind, style, _ = mt._native(rule, 'scripts/check.py')
        self.record['revision'] = 'r2'
        body = metadata_body(self.source_path.read_bytes(), style, rule['handling'], file_kind=kind, mode='100644')
        self.source_path.write_bytes(insert_header(body, self.record, style=style,
            handling=rule['handling'], file_kind=kind, mode='100755'))
        self.source_path.chmod(0o755)
        manifest = json.loads(self.manifest_path.read_text())
        manifest['files']['scripts/check.py'] = json.loads(json.dumps(self.record))
        manifest['files'][ga.MANIFEST_PATH]['revision'] = 'r2'
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        self.git('add', '.')
        self.commit('committed mode-only source revision', '2024-03-06T00:00:00+00:00')
        self.subject_sha = self.head()
        result = self.run_check()
        self.assertEqual(result['status'], 'PASS')
        updated = result['inherited_base_fields']
        self.assertTrue(any(x['path'] == 'scripts/check.py' and x['field'] == 'updated' for x in updated))

    def test_deleted_and_recreated_same_path_cannot_inherit_created(self):
        old_record = json.loads(json.dumps(self.record))
        self.source_path.unlink()
        self.git('add', '-A')
        self.commit('delete source identity', '2024-03-06T00:00:00+00:00')
        self.source_path.write_text('print("root")\n')
        self.git('add', 'scripts/check.py')
        self.commit('recreate same spelling as new file', '2024-03-07T00:00:00+00:00')
        renamed_path = self.repo / 'scripts/renamed_again.py'
        self.source_path.rename(renamed_path)
        self.git('add', '-A')
        self.commit('rename recreated identity', '2024-03-07T12:00:00+00:00')
        new_subject = self.head()
        rule = self.policy_obj.classify('scripts/check.py')
        kind, style, _ = mt._native(rule, 'scripts/check.py')
        evidence = GitRepo(self.repo).path_provenance('scripts/renamed_again.py', subject=new_subject,
            style=style, handling=rule['handling'], file_kind=kind)
        new_record = json.loads(json.dumps(old_record))
        new_record['revision'] = 'r1'
        new_record['created'] = {'value': evidence.created.value, 'source': evidence.created.source}
        new_record['updated'] = {'value': evidence.updated.value, 'source': evidence.updated.source}
        # Deliberately restore old created evidence after making a verified
        # net A->B rename that hides the intermediate delete/re-add.
        new_record['created'] = old_record['created']
        body = renamed_path.read_bytes()
        renamed_path.write_bytes(insert_header(body, new_record, style=style,
            handling=rule['handling'], file_kind=kind, mode='100644'))
        manifest = json.loads(self.manifest_path.read_text())
        del manifest['files']['scripts/check.py']
        manifest['files']['scripts/renamed_again.py'] = json.loads(json.dumps(new_record))
        manifest['inventory']['explicit_additions'] = sorted(set(manifest['inventory']['explicit_additions']) | {'scripts/renamed_again.py'})
        manifest['files'][ga.MANIFEST_PATH]['revision'] = 'r2'
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        self.git('add', '.')
        self.commit('add new identity metadata', '2024-03-08T00:00:00+00:00')
        self.subject_sha = self.head()
        self.assertEqual(ga._renames(GitRepo(self.repo), self.base_sha, self.subject_sha).get('scripts/renamed_again.py'),
                         'scripts/check.py')
        with self.assertRaisesRegex(ga.GateError, 'new or changed provenance differs'):
            self.run_check()

    def test_continuous_verified_rename_inherits_created_and_updated(self):
        old_path = self.source_path
        new_path = self.repo / 'scripts/moved.py'
        old_path.rename(new_path)
        self.git('add', '-A')
        self.commit('continuous identity rename', '2024-03-06T00:00:00+00:00')
        manifest = json.loads(self.manifest_path.read_text())
        record = manifest['files'].pop('scripts/check.py')
        manifest['files']['scripts/moved.py'] = record
        manifest['inventory']['explicit_additions'] = sorted(set(manifest['inventory']['explicit_additions']) | {'scripts/moved.py'})
        manifest['files'][ga.MANIFEST_PATH]['revision'] = 'r2'
        self.manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n')
        self.git('add', '.')
        self.commit('continuous rename metadata', '2024-03-07T00:00:00+00:00')
        self.subject_sha = self.head()
        result = self.run_check()
        self.assertEqual(result['status'], 'PASS')
        self.assertIn({'from': 'scripts/check.py', 'to': 'scripts/moved.py'}, result['verified_renames'])
        self.assertTrue(any(x['path'] == 'scripts/moved.py' and x['field'] == 'created'
                            for x in result['inherited_base_fields']))

    def test_second_parent_base_lineage_fails_closed_after_same_name_recreation(self):
        self.git('checkout', '-q', self.root_sha)
        self.git('checkout', '-qb', 'target-recreated-path')
        self.source_path.unlink()
        self.git('add', '-A')
        self.commit('target deletes original A', '2024-03-06T00:00:00+00:00')
        self.source_path.write_text('print("target new A")\n')
        self.git('add', 'scripts/check.py')
        self.commit('target recreates A', '2024-03-06T12:00:00+00:00')
        base = self.head()

        self.git('checkout', '-q', self.root_sha)
        self.git('checkout', '-qb', 'candidate-retains-old-A')
        self.source_path.write_text('print("candidate old A")\n')
        self.git('add', 'scripts/check.py')
        self.commit('candidate retains separate same-name A', '2024-03-07T00:00:00+00:00')
        merge = subprocess.run(['git', '-C', str(self.repo), 'merge', '--no-ff', '--no-commit',
            'target-recreated-path'], stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.base_env)
        self.assertNotEqual(merge.returncode, 0, 'same-path divergence should require an explicit conflict resolution')
        self.source_path.write_text('print("candidate old A")\n')
        self.git('add', 'scripts/check.py')
        self.commit('merge target as second parent keeping old A', '2024-03-08T00:00:00+00:00')
        subject = self.head()
        parents = GitRepo(self.repo).run('rev-list', '--parents', '-n', '1', subject).decode().split()
        self.assertEqual(parents[2], base)
        repo = GitRepo(self.repo)
        repo.run('merge-base', '--is-ancestor', base, subject)
        first_parent_chain = set(repo.run('rev-list', '--first-parent', subject).splitlines())
        self.assertNotIn(base.encode(), first_parent_chain)
        with self.assertRaisesRegex(ga.GateError, 'unsupported identity lineage.*first-parent chain'):
            ga._window_identity_origin(repo, base, subject, 'scripts/check.py',
                base_on_first_parent=base.encode() in first_parent_chain)

    def test_cli_check_and_plan_apply_use_repo_relative_defaults(self):
        adapter = HERE / 'gate_adapter.py'
        check = subprocess.run([sys.executable, str(adapter), 'check', '--repo', str(self.repo),
            '--base', self.base_sha, '--subject', self.subject_sha], cwd=HERE,
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertEqual(json.loads(check.stdout)['status'], 'PASS')
        output = self.repo / 'cli-plan.json'
        plan = subprocess.run([sys.executable, str(adapter), 'plan-apply', '--repo', str(self.repo),
            '--base', self.base_sha, '--subject', self.subject_sha, '--path', 'scripts/check.py',
            '--output', str(output)], cwd=HERE, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertEqual(plan.returncode, 0, plan.stderr)
        self.assertEqual(json.loads(plan.stdout)['status'], 'DRY_RUN')


if __name__ == '__main__':
    unittest.main(verbosity=2)
