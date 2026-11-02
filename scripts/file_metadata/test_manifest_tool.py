# File metadata (manifest mirror):
# Purpose: 验证 test_real_manifest_is_valid_under_current_strict_schema, test_real_schema_not_condition_rejects_unknown_with_nonunknown_source, test_supported_schema_semantics_not_ref_siblings_and_false, test_unknown_keyword_and_invalid_schema_shape_fail_closed 等场景，覆盖该模块的授权、错误或数据边界。
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-02
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Targeted safety fixtures for manifest validation (source checkout is read-only)."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import manifest_tool as mt
from git_inventory import GitRepo, compare_manifest_coverage, manifest_self_updated, GitInventoryError
from metadata_core import STYLES

REPO = HERE.parents[1]

class ManifestPrototypeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.schema = mt.load_json(REPO / 'scripts/file_metadata/schema.json')
        cls.manifest = mt.load_json(REPO / 'scripts/file_metadata/manifest.json')
        cls.policy_data = mt.load_json(REPO / 'scripts/file_metadata/policy.json')
        cls.policy = mt.Policy(cls.policy_data)

    def test_real_manifest_is_valid_under_current_strict_schema(self):
        mt.validate_schema(self.manifest, self.schema)

    def test_real_schema_not_condition_rejects_unknown_with_nonunknown_source(self):
        bad = json.loads(json.dumps(self.manifest))
        bad['files']['README.md']['created']['value'] = 'unknown'
        with self.assertRaisesRegex(mt.ManifestError, 'schema validation failed'):
            mt.validate_schema(bad, self.schema)

    def test_supported_schema_semantics_not_ref_siblings_and_false(self):
        with self.assertRaises(mt.ManifestError): mt.validate_schema('not-a-number', {'not': {'type': 'string'}})
        sibling_ref = {'$defs': {'text': {'type': 'string'}}, '$ref': '#/$defs/text', 'minLength': 4}
        with self.assertRaises(mt.ManifestError): mt.validate_schema('abc', sibling_ref)
        false_child = {'type': 'object', 'properties': {'blocked': False}}
        with self.assertRaises(mt.ManifestError): mt.validate_schema({'blocked': 'x'}, false_child)

    def test_unknown_keyword_and_invalid_schema_shape_fail_closed(self):
        with self.assertRaisesRegex(mt.ManifestError, 'unsupported JSON Schema keyword'):
            mt.validate_schema({}, {'type': 'object', 'maxProperties': 3})
        with self.assertRaisesRegex(mt.ManifestError, 'invalid required list'):
            mt.validate_schema({}, {'required': 'not-an-array'})

    def test_real_policy_record_samples_and_docker_directive_guard(self):
        for path in ('README.md', 'internal/store/migrations/0001_legacy_core.sql',
                     'internal/documentparse/docx_symbol.go', 'web/src/api/wire.generated.ts'):
            mt.validate_record(path, self.manifest['files'][path], self.policy)
        docker = json.loads(json.dumps(self.manifest['files']['deploy/app/Dockerfile']))
        docker['handling'] = 'header-and-manifest'
        self.assertEqual(self.policy.classify('deploy/app/Dockerfile')['handling'], 'manifest-only')
        with self.assertRaisesRegex(mt.ManifestError, 'handling differs from policy'):
            mt.validate_record('deploy/app/Dockerfile', docker, self.policy)

    def test_self_record_requires_canonical_unknown_and_is_not_reusable(self):
        path = 'scripts/file_metadata/manifest.json'
        rec = json.loads(json.dumps(self.manifest['files'][path]))
        rec['updated'] = json.loads(json.dumps(self.manifest['files']['README.md']['updated']))
        with self.assertRaisesRegex(mt.ManifestError, 'exact canonical self-reference'):
            mt.validate_record(path, rec, self.policy)
        other = json.loads(json.dumps(self.manifest['files']['README.md']))
        other['updated'] = {'value': 'unknown', 'source': manifest_self_updated().source}
        with self.assertRaisesRegex(mt.ManifestError, 'cannot be reused'):
            mt.validate_record('README.md', other, self.policy)

    def test_real_manifest_entrypoint_rejects_explicit_baseline_override(self):
        with self.assertRaisesRegex(mt.ManifestError, 'does not match manifest'):
            mt.validate_manifest(REPO, self.manifest, self.schema, self.policy_data,
                                 'f' * 40, baseline='0' * 40)

    def test_missing_and_extra_path_coverage(self):
        missing, extra = compare_manifest_coverage({'a.py', 'b.json'}, {'a.py', 'stale.py'})
        self.assertEqual(missing, ['b.json'])
        self.assertEqual(extra, ['stale.py'])

    def test_unknown_author_and_rights_need_explicit_reasons(self):
        rec = json.loads(json.dumps(self.manifest['files']['README.md']))
        rec['authors'] = [{'name':'unknown', 'role':'unknown', 'source':'No trustworthy author evidence is available.'}]
        rec['copyright'] = [{'holder':'unknown', 'notice':'unknown', 'source':'No trustworthy rights evidence is available.'}]
        mt.validate_record('README.md', rec, self.policy)
        rec['authors'][0]['source'] = ''
        with self.assertRaisesRegex(mt.ManifestError, 'explicit reason'):
            mt.validate_record('README.md', rec, self.policy)
        rec['authors'] = [{'name':'forged', 'role':'project-author', 'source':'Invented project attribution.'}]
        with self.assertRaisesRegex(mt.ManifestError, 'project author'):
            mt.validate_record('README.md', rec, self.policy)

    def test_unknown_rights_preserve_descriptive_notice_and_require_source(self):
        rec = json.loads(json.dumps(self.manifest['files']['README.md']))
        notice = 'No per-file copyright or license declaration was found; project ownership is not inferred.'
        rec['copyright'] = [{'holder':'unknown', 'notice':notice, 'source':'Asset metadata inspection requires rights-source follow-up.'}]
        mt.validate_record('README.md', rec, self.policy)
        self.assertEqual(rec['copyright'][0]['notice'], notice)
        rec['copyright'][0]['source'] = ''
        with self.assertRaisesRegex(mt.ManifestError, 'copyright requires'):
            mt.validate_record('README.md', rec, self.policy)

    def test_unknown_date_requires_reason(self):
        rec = json.loads(json.dumps(self.manifest['files']['README.md']))
        rec['created'] = {'value': 'unknown', 'source': {'kind': 'unknown', 'reason': ''}}
        with self.assertRaisesRegex(mt.ManifestError, 'unknown created requires explicit reason'):
            mt.validate_record('README.md', rec, self.policy)

    def test_two_snapshot_windows_anchor_prior_blob_and_verify_netzero(self):
        with tempfile.TemporaryDirectory(prefix='file-meta-git-fixture-') as td:
            repo = Path(td)
            def git(*args):
                subprocess.run(['git', '-C', str(repo), *args], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            def head(): return subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD'], text=True).strip()
            def commit_manifest(manifest, message):
                mp = repo / 'scripts' / 'file_metadata' / 'manifest.json'
                mp.parent.mkdir(parents=True, exist_ok=True)
                mp.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
                git('add', 'scripts/file_metadata/manifest.json')
                git('commit', '-qm', message)
            git('init', '-q')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            (repo / 'README.md').write_text('fixture root\n', encoding='utf-8')
            git('add', 'README.md'); git('commit', '-qm', 'root baseline')
            root_sha = head()
            (repo / 'src').mkdir(); (repo / 'src' / 'a.py').write_bytes(b'print(1)\n')
            # A first manifest commit records the future source as unknown; a
            # follow-up commit can then cite the real introduction SHA.
            def record(path, purpose, created, updated, handling='header-and-manifest', state='pending-header-application', exception=None):
                r = json.loads(json.dumps(self.manifest['files']['README.md']))
                r.update({'purpose': purpose, 'created': created, 'updated': updated,
                          'handling': handling, 'header_state': state, 'revision': 'r1'})
                if exception is not None: r['exception'] = exception
                return r
            unknown = {'value': 'unknown', 'source': {'kind': 'unknown', 'reason': 'Fixture commit identity not available until after the commit is created.'}}
            self_rec = record('scripts/file_metadata/manifest.json', 'Canonical registry for the isolated fixture file metadata and its Git evidence.', unknown, {'value': 'unknown', 'source': manifest_self_updated().source}, 'manifest-only', 'manifest-only', {'rule_id':'strict-json','source':'scripts/file_metadata/manifest.json','reason':'Strict JSON fixture registry remains byte-for-byte valid and is authoritative for the isolated fixture.'})
            initial = {'schema_version': 1, 'inventory': {
                'baseline_head': root_sha,
                'baseline_timestamp': self.manifest['inventory']['baseline_timestamp'],
                'tracked_file_count': 1,
                'explicit_additions': ['scripts/file_metadata/manifest.json', 'src/a.py'],
                'excluded_untracked_paths': []},
                'files': {
                    'README.md': record('README.md', 'Fixture root note records the isolated test repository purpose and baseline boundary.', unknown, unknown),
                    'src/a.py': record('src/a.py', 'Fixture Python module prints one literal value and supplies a tracked body for metadata window verification.', unknown, unknown),
                    'scripts/file_metadata/manifest.json': self_rec}}
            git('add', 'src/a.py')
            commit_manifest(initial, 'add canonical fixture manifest and source')
            intro = head()
            py = repo / 'src' / 'a.py'
            py.write_bytes(b'print(9)\n'); git('add', 'src/a.py'); git('commit', '-qm', 'pre-baseline body change')
            prebaseline_edit = head()
            py.write_bytes(b'print(1)\n'); git('add', 'src/a.py'); git('commit', '-qm', 'pre-baseline body revert')
            (repo / 'src' / 'copy.py').write_bytes(b'print(1)\n'); git('add', 'src/copy.py'); git('commit', '-qm', 'add unrelated identical-body path')
            copy_commit = head()
            history_head = head()
            # Now populate true Git provenance in a later snapshot. This commit
            # is the baseline manifest blob for two tested consecutive windows.
            base_manifest = json.loads(json.dumps(initial))
            for path in ('README.md', 'src/a.py'):
                rule = self.policy.classify(path)
                kind, style, _ = mt._native(rule, path)
                ev = GitRepo(repo).path_provenance(path, subject=history_head, style=style, handling=rule['handling'], file_kind=kind)
                base_manifest['files'][path]['created'] = {'value': ev.created.value, 'source': ev.created.source}
                base_manifest['files'][path]['updated'] = {'value': ev.updated.value, 'source': ev.updated.source}
            copy_rule = self.policy.classify('src/copy.py')
            copy_kind, copy_style, _ = mt._native(copy_rule, 'src/copy.py')
            copy_ev = GitRepo(repo).path_provenance('src/copy.py', subject=history_head, style=copy_style,
                handling=copy_rule['handling'], file_kind=copy_kind)
            base_manifest['files']['src/copy.py'] = record('src/copy.py', 'Second fixture Python module has the same literal body but a separate Git identity for source-path rejection.',
                {'value':copy_ev.created.value,'source':copy_ev.created.source},
                {'value':copy_ev.updated.value,'source':copy_ev.updated.source})
            base_manifest['inventory']['explicit_additions'] = ['scripts/file_metadata/manifest.json', 'src/a.py', 'src/copy.py']
            self_kind, self_style, _ = mt._native(self.policy.classify('scripts/file_metadata/manifest.json'), 'scripts/file_metadata/manifest.json')
            self_ev = GitRepo(repo).path_provenance('scripts/file_metadata/manifest.json', subject=history_head, style=self_style, handling='manifest-only', file_kind=self_kind)
            base_manifest['files']['scripts/file_metadata/manifest.json']['created'] = {'value': self_ev.created.value, 'source': self_ev.created.source}
            commit_manifest(base_manifest, 'record fixture provenance after introduction')
            baseline = head()
            previous, previous_oid, previous_raw = mt.load_anchored_previous(repo, baseline)
            self.assertEqual(previous, base_manifest)
            base_repo = GitRepo(repo)
            base_entry = base_repo.tree_inventory(baseline)['src/a.py']
            py_rule = self.policy.classify('src/a.py')
            _, py_style, _ = mt._native(py_rule, 'src/a.py')
            mt._verify_recorded_updated(base_repo, 'src/a.py', previous['files']['src/a.py'], base_entry,
                target_commit=baseline, rule=py_rule, kind='python', style=py_style)
            bad_date = json.loads(json.dumps(previous['files']['src/a.py']))
            bad_date['updated']['value'] = '2000-01-01'
            with self.assertRaisesRegex(mt.ManifestError, 'UTC date'):
                mt._verify_recorded_updated(base_repo, 'src/a.py', bad_date, base_entry,
                    target_commit=baseline, rule=py_rule, kind='python', style=py_style)
            bad_path = json.loads(json.dumps(previous['files']['src/a.py']))
            bad_path['updated']['source']['path'] = 'README.md'
            with self.assertRaisesRegex(mt.ManifestError, 'verified rename/history chain'):
                mt._verify_recorded_updated(base_repo, 'src/a.py', bad_path, base_entry,
                    target_commit=baseline, rule=py_rule, kind='python', style=py_style)
            same_body_other_file = json.loads(json.dumps(previous['files']['src/a.py']))
            copy_date = subprocess.check_output(['git', '-C', str(repo), 'show', '-s', '--format=%cI', copy_commit], text=True).strip()
            from datetime import datetime, timezone
            same_body_other_file['updated'] = {'value': datetime.fromisoformat(copy_date).astimezone(timezone.utc).date().isoformat(),
                'source': {'kind':'git-last-content-change','commit':copy_commit,'path':'src/copy.py','reason':'same bytes at an unrelated path must not establish file identity'}}
            with self.assertRaisesRegex(mt.ManifestError, 'not in Git follow history|verified rename/history chain'):
                mt._verify_recorded_updated(base_repo, 'src/a.py', same_body_other_file, base_entry,
                    target_commit=baseline, rule=py_rule, kind='python', style=py_style)
            tree_oid = subprocess.check_output(['git', '-C', str(repo), 'write-tree'], text=True).strip()
            orphan = subprocess.check_output(['git', '-C', str(repo), 'commit-tree', tree_oid, '-m', 'unrelated fixture source'], text=True).strip()
            nonancestor = json.loads(json.dumps(previous['files']['src/a.py']))
            nonancestor['updated']['source']['commit'] = orphan
            nonancestor['updated']['source']['path'] = 'src/a.py'
            with self.assertRaisesRegex(mt.ManifestError, 'not an ancestor'):
                mt._verify_recorded_updated(base_repo, 'src/a.py', nonancestor, base_entry,
                    target_commit=baseline, rule=py_rule, kind='python', style=py_style)
            wrong_body_source = json.loads(json.dumps(previous['files']['src/a.py']))
            edit_date = subprocess.check_output(['git', '-C', str(repo), 'show', '-s', '--format=%cI', prebaseline_edit], text=True).strip()
            from datetime import datetime, timezone
            wrong_body_source['updated'] = {'value': datetime.fromisoformat(edit_date).astimezone(timezone.utc).date().isoformat(),
                'source': {'kind':'git-last-content-change','commit':prebaseline_edit,'path':'src/a.py','reason':'synthetic ancestor body edit with differing blob'}}
            with self.assertRaisesRegex(mt.ManifestError, 'source body differs'):
                mt._verify_recorded_updated(base_repo, 'src/a.py', wrong_body_source, base_entry,
                    target_commit=baseline, rule=py_rule, kind='python', style=py_style)

            # Window 1 has edit then revert. Commit an actual candidate registry
            # blob, then rebuild from that fixed subject and verify full history.
            py = repo / 'src' / 'a.py'
            py.write_bytes(b'print(2)\n'); git('add', 'src/a.py'); git('commit', '-qm', 'window one intermediate edit')
            intermediate = head()
            edited_entry = GitRepo(repo).tree_inventory(intermediate)['src/a.py']
            py.write_bytes(b'print(1)\n'); git('add', 'src/a.py'); git('commit', '-qm', 'window one revert')
            provisional = mt.build_candidate_manifest(repo, previous, self.schema, self.policy_data,
                baseline=baseline, subject=head(), reviewed_additions={})
            provisional['files']['scripts/file_metadata/manifest.json']['revision'] = 'r2'
            commit_manifest(provisional, 'window one candidate manifest')
            subject_one = head()
            candidate_one = mt.build_candidate_manifest(repo, previous, self.schema, self.policy_data,
                baseline=baseline, subject=subject_one, reviewed_additions={})
            self.assertEqual(candidate_one['files']['src/a.py']['revision'], previous['files']['src/a.py']['revision'])
            self.assertEqual(candidate_one['files']['src/a.py']['updated'], previous['files']['src/a.py']['updated'])
            self.assertEqual(candidate_one, json.loads((repo / 'scripts/file_metadata/manifest.json').read_text(encoding='utf-8')))
            mt.validate_manifest(repo, candidate_one, self.schema, self.policy_data, subject_one, verify_history=True)

            # Window 2 has no source change. The previous manifest is anchored
            # by the canonical blob in subject_one even though its own embedded
            # baseline_head describes the older initialization snapshot.
            second_previous, second_oid, second_raw = mt.load_anchored_previous(repo, subject_one)
            self.assertNotEqual(second_previous['inventory']['baseline_head'], subject_one)
            provisional_two = mt.build_candidate_manifest(repo, second_previous, self.schema, self.policy_data,
                baseline=subject_one, subject=subject_one, reviewed_additions={})
            provisional_two['files']['scripts/file_metadata/manifest.json']['revision'] = 'r3'
            commit_manifest(provisional_two, 'window two candidate manifest')
            subject_two = head()
            candidate_two = mt.build_candidate_manifest(repo, second_previous, self.schema, self.policy_data,
                baseline=subject_one, subject=subject_two, reviewed_additions={})
            self.assertEqual(candidate_two['files']['src/a.py']['updated'], second_previous['files']['src/a.py']['updated'])
            self.assertEqual(candidate_two['files']['src/a.py']['revision'], second_previous['files']['src/a.py']['revision'])
            self.assertEqual(candidate_two, json.loads((repo / 'scripts/file_metadata/manifest.json').read_text(encoding='utf-8')))
            mt.validate_manifest(repo, candidate_two, self.schema, self.policy_data, subject_two, verify_history=True)

            # A regular-file chmod increments the independent revision while
            # retaining the validated body update evidence.
            third_previous, _, _ = mt.load_anchored_previous(repo, subject_two)
            py.chmod(0o755); git('add', 'src/a.py'); git('commit', '-qm', 'window three chmod only')
            provisional_three = mt.build_candidate_manifest(repo, third_previous, self.schema, self.policy_data,
                baseline=subject_two, subject=head(), reviewed_additions={})
            provisional_three['files']['scripts/file_metadata/manifest.json']['revision'] = f"r{int(third_previous['files']['scripts/file_metadata/manifest.json']['revision'][1:]) + 1}"
            commit_manifest(provisional_three, 'window three candidate manifest')
            subject_three = head()
            candidate_three = mt.build_candidate_manifest(repo, third_previous, self.schema, self.policy_data,
                baseline=subject_two, subject=subject_three, reviewed_additions={})
            self.assertEqual(candidate_three['files']['src/a.py']['revision'], 'r2')
            self.assertEqual(candidate_three['files']['src/a.py']['updated'], third_previous['files']['src/a.py']['updated'])
            mt.validate_manifest(repo, candidate_three, self.schema, self.policy_data, subject_three, verify_history=True)

            # Caller-supplied prior JSON cannot replace the baseline blob.
            forged = json.loads(json.dumps(second_previous)); forged['files']['src/a.py']['updated']['source']['path'] = 'README.md'
            with self.assertRaisesRegex(mt.ManifestError, 'differs from canonical baseline Git blob'):
                mt.validate_manifest(repo, candidate_two, self.schema, self.policy_data, subject_two,
                    verify_history=True, previous_manifest=forged)
            fake_file = repo.parent / (repo.name + '-forged-previous.json')
            try:
                fake_file.write_bytes(second_raw + b' ')
                with self.assertRaisesRegex(mt.ManifestError, 'bytes differ from canonical baseline Git blob'):
                    mt.validate_manifest(repo, candidate_two, self.schema, self.policy_data, subject_two,
                        verify_history=True, previous_manifest_path=fake_file)
            finally:
                fake_file.unlink(missing_ok=True)

    def test_manifest_self_helper_is_exact(self):
        self.assertEqual(manifest_self_updated().value, 'unknown')
        with self.assertRaises(GitInventoryError): manifest_self_updated('other.json')

if __name__ == '__main__': unittest.main(verbosity=2)
