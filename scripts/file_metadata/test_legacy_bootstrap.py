# File metadata (manifest mirror):
# Purpose: 验证 test_exact_four_bridge_additions_only, test_purpose_requires_exact_source_path_mode_and_oid, test_new_record_requires_explicit_authorship_and_rights, test_header_only_oid_change_keeps_revision_and_chmod_increments 等场景，覆盖该模块的授权、错误或数据边界。
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-02
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Small guards for the explicit bootstrap bridge and purpose reuse."""
from pathlib import Path
import sys
import unittest
from types import SimpleNamespace
import json
import subprocess
import tempfile
from datetime import datetime, timezone
HERE=Path(__file__).resolve().parent
sys.path.insert(0,str(HERE))
from legacy_bootstrap import (BootstrapError, ALLOWED_BRIDGE_ADDITIONS, verify_bridge_trees,
    match_purpose, _window_revision, _same_earliest_created, _verify_created,
    prepare_policy_delta, _validate_bootstrap_record, _validate_candidate_records, _exception_for, _record_new, GLOSSARY_VIEWS_RULE, LITERAL_MARKER_RULE)
from git_inventory import GitRepo, TreeEntry
from metadata_core import STYLES, MetadataError, insert_header, metadata_body
import manifest_tool as mt

class BootstrapGuards(unittest.TestCase):
    def test_exact_four_bridge_additions_only(self):
        legacy={'README.md':SimpleNamespace(mode='100644',oid='a'*40)}
        anchor=dict(legacy)
        for index,path in enumerate(sorted(ALLOWED_BRIDGE_ADDITIONS)):
            anchor[path]=SimpleNamespace(mode='100644',oid=f'{index+1:040x}')
        added,removed,changed=verify_bridge_trees(legacy,anchor)
        self.assertEqual(added,sorted(ALLOWED_BRIDGE_ADDITIONS)); self.assertEqual(removed,[]); self.assertEqual(changed,[])
        for bad in ('common-object-change','extra-addition','missing-approved-addition','removal'):
            l=dict(legacy); a=dict(anchor)
            if bad=='common-object-change': a['README.md']=SimpleNamespace(mode='100644',oid='b'*40)
            elif bad=='extra-addition': a['extra.txt']=SimpleNamespace(mode='100644',oid='c'*40)
            elif bad=='missing-approved-addition': a.pop(sorted(ALLOWED_BRIDGE_ADDITIONS)[0])
            else: l['old.txt']=SimpleNamespace(mode='100644',oid='d'*40)
            with self.subTest(bad=bad), self.assertRaises(BootstrapError): verify_bridge_trees(l,a)

    def test_purpose_requires_exact_source_path_mode_and_oid(self):
        proposal={'new.py':{'path':'new.py','mode':'100644','oid':'a'*40,'purpose':'Purpose covering a reviewed test source with a concrete input and output.'}}
        tree={'new.py':SimpleNamespace(mode='100644',oid='a'*40)}
        self.assertIs(match_purpose('new.py',tree['new.py'],tree,proposal),proposal['new.py'])
        for entry in (SimpleNamespace(mode='100755',oid='a'*40),SimpleNamespace(mode='100644',oid='b'*40)):
            with self.assertRaises(BootstrapError): match_purpose('new.py',entry,tree,proposal)
        with self.assertRaises(BootstrapError): match_purpose('other.py',tree['new.py'],tree,proposal)
        for key, value in [('mode','100755'),('oid','b'*40)]:
            forged={'new.py':dict(proposal['new.py'], **{key:value})}
            with self.assertRaises(BootstrapError): match_purpose('new.py',tree['new.py'],tree,forged)

    def test_new_record_requires_explicit_authorship_and_rights(self):
        policy = mt.Policy(json.loads((HERE / 'testdata' / 'anchor-policy.json').read_text()))
        schema = mt.load_json(HERE.parents[1] / 'scripts/file_metadata/schema.json')
        evidence = SimpleNamespace(value='unknown', source={'kind':'unknown', 'reason':'No traceable history exists for this fixture.'})
        repo = SimpleNamespace(path_provenance=lambda *args, **kwargs: SimpleNamespace(created=evidence, updated=evidence))
        proposal = {'purpose':'Synthetic reviewed source illustrates explicit attribution and uncertainty.'}
        with self.assertRaisesRegex(BootstrapError, 'explicit reviewed authors'):
            _record_new('new.py', None, proposal, policy, repo, 'a'*40, schema)
        proposal['authors'] = [{'name':'unknown','role':'unknown','source':'Source is not established for this fixture.','contact':'Project maintainer: +86 13617696321'}]
        proposal['copyright'] = [{'holder':'unknown','notice':'unknown','source':'No rights evidence is available for this fixture.'}]
        rec = _record_new('new.py', None, proposal, policy, repo, 'a'*40, schema)
        mt.validate_record('new.py', rec, policy)
        self.assertEqual(rec['authors'][0]['name'], 'unknown')
        self.assertEqual(rec['copyright'][0]['holder'], 'unknown')

    def test_glossary_generated_record_preserves_exact_tool_and_source(self):
        policy_data = json.loads((HERE / 'testdata' / 'anchor-policy.json').read_text())
        policy_data['file_classes'].insert(0, GLOSSARY_VIEWS_RULE)
        policy = mt.Policy(policy_data)
        schema = mt.load_json(HERE.parents[1] / 'scripts/file_metadata/schema.json')
        evidence = SimpleNamespace(value='unknown', source={'kind':'unknown', 'reason':'No traceable history exists for this fixture.'})
        repo = SimpleNamespace(path_provenance=lambda *args, **kwargs: SimpleNamespace(created=evidence, updated=evidence))
        proposal = {'purpose':'Derived glossary reading view presents the canonical concept data.',
                    'authors':[{'name':'unknown','role':'unknown','source':'Source not established for this fixture.'}],
                    'copyright':[{'holder':'unknown','notice':'unknown','source':'Rights not established for this fixture.'}]}
        for path in GLOSSARY_VIEWS_RULE['paths']:
            rec = _record_new(path, None, proposal, policy, repo, 'a'*40, schema)
            self.assertEqual(rec['handling'], 'generated-manifest-only')
            self.assertEqual(rec['generator'], {'tool':'scripts/check_glossary.py --write-views', 'inputs':['docs/glossary/concepts.json']})
            mt.validate_record(path, rec, policy)
            rec['generator']['inputs'] = ['other.json']
            with self.assertRaisesRegex(mt.ManifestError, 'generator provenance mismatch'):
                mt.validate_record(path, rec, policy)

    def test_literal_marker_exception_is_exact_and_keeps_other_python_strict(self):
        policy_data = json.loads((HERE / 'testdata' / 'anchor-policy.json').read_text())
        policy_data['file_classes'].insert(0, LITERAL_MARKER_RULE)
        policy = mt.Policy(policy_data)
        for path in LITERAL_MARKER_RULE['paths']:
            with self.subTest(path=path):
                self.assertEqual(policy.classify(path)['handling'], 'manifest-only')
        other = policy.classify('scripts/file_metadata/test_other.py')
        self.assertEqual(other['handling'], 'header-and-manifest')
        with self.assertRaises(MetadataError):
            metadata_body(b'# File metadata ' + b'(manifest mirror):\nmalformed\n', STYLES['hash'],
                          other['handling'], file_kind='python', mode='100644')

    def test_header_only_oid_change_keeps_revision_and_chmod_increments(self):
        path='fixture.py'; body=b'print("fixture")\n'
        rec={'purpose':'Fixture script prints one fixed value.','authors':[{'name':'Bai Junhao','email':'baijunhao@cq.chinamobile.com','contact':'+86 13617696321'}],
             'created':{'value':'2026-11-02'},'updated':{'value':'2026-11-02'},'revision':'r3','copyright':[{'notice':'See LICENSE'}]}
        headed=insert_header(body,rec,style=STYLES['hash'],handling='header-and-manifest',file_kind='python',mode='100644')
        before=TreeEntry(path,path.encode(),'100644','blob','a'*40,body)
        after=TreeEntry(path,path.encode(),'100644','blob','b'*40,headed)
        rule={'handling':'header-and-manifest'}
        revision,same_body=_window_revision('r3',before,after,rule,path)
        self.assertTrue(same_body); self.assertEqual(revision,'r3')
        chmod=TreeEntry(path,path.encode(),'100755','blob','b'*40,headed)
        revision,same_body=_window_revision('r3',before,chmod,rule,path)
        self.assertTrue(same_body); self.assertEqual(revision,'r4')
        link_before=TreeEntry(path,path.encode(),'120000','blob','c'*40,b'target-a')
        link_after=TreeEntry(path,path.encode(),'120000','blob','d'*40,b'target-b')
        revision,same_body=_window_revision('r3',link_before,link_after,rule,path)
        self.assertFalse(same_body); self.assertEqual(revision,'r4')

    def test_created_source_must_be_earliest_main_identity_introduction(self):
        with tempfile.TemporaryDirectory(prefix='created-source-fixture-') as td:
            repo=Path(td)
            def git(*args): subprocess.run(['git','-C',str(repo),*args],check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            git('init','-q'); git('config','user.name','Fixture'); git('config','user.email','fixture@example.invalid')
            f=repo/'src'/'item.py'; f.parent.mkdir(); f.write_bytes(b'value=1\n'); git('add','src/item.py'); git('commit','-qm','introduce file')
            first=subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD'],text=True).strip()
            f.write_bytes(b'value=2\n'); git('add','src/item.py'); git('commit','-qm','edit existing file')
            second=subprocess.check_output(['git','-C',str(repo),'rev-parse','HEAD'],text=True).strip()
            g=GitRepo(repo); expected=g.path_provenance('src/item.py',subject=second,style=STYLES['hash'],handling='header-and-manifest',file_kind='python').created
            self.assertEqual(expected.source['commit'],first)
            true_old={'value':expected.value,'source':expected.source}
            self.assertTrue(_same_earliest_created(true_old,expected))
            ts=int(g.run('show','-s','--format=%ct',second).strip())
            later={'value':datetime.fromtimestamp(ts,timezone.utc).date().isoformat(),
                   'source':{'kind':'git-first-introduction','commit':second,'path':'src/item.py','reason':'later edit falsely claimed as creation'}}
            self.assertTrue(_verify_created(g,'src/item.py',later,second)[0])
            self.assertFalse(_same_earliest_created(later,expected))

    def test_exact_candidate_policy_delta_and_unknown_paths_remain_closed(self):
        with open(HERE/'testdata'/'anchor-policy.json', encoding='utf-8') as stream:
            anchor_policy=json.load(stream)
        def entry(path, oid='a'):
            oid=oid*40
            return TreeEntry(path,path.encode(),'100644','blob',oid,b'fixture')
        paths={'docs-site/bun.lock','docs-site/docusaurus.config.js','docs-site/sidebars.js',
               'docs/engineering/file-metadata.md','web/public/huangjueshu-competition-logo.png',
               'testdata/legalnorm/fixture.html',
               *GLOSSARY_VIEWS_RULE['paths'], *LITERAL_MARKER_RULE['paths'],
               'internal/store/migrations/0027_contract_signing.sql','internal/store/migrations/0028_contract_fulfillment.sql',
               'internal/store/migrations/0029_contract_payments.sql','internal/store/migrations/0030_contract_assets.sql',
               'internal/store/migrations/0031_contract_source_observations.sql','internal/store/migrations/0032_clm_outbox_metrics.sql',
               'deploy/app/Dockerfile'}
        anchor={p:entry(p) for p in paths if p=='docs/engineering/file-metadata.md' or p=='deploy/app/Dockerfile'}
        subject={p:entry(p) for p in paths}
        candidate,delta=prepare_policy_delta(anchor_policy,'f'*40,anchor,subject)
        policy=mt.Policy(candidate)
        for path in paths:
            want = 'generated-manifest-only' if path in GLOSSARY_VIEWS_RULE['paths'] else 'manifest-only'
            self.assertEqual(policy.classify(path)['handling'],want)
        protected = policy.classify('testdata/legalnorm/fixture.html')
        self.assertEqual(protected['id'], 'frozen-test-fixtures')
        self.assertEqual(protected['handling'], 'manifest-only')
        self.assertEqual(policy.classify('testdata/legalnorm/other.html')['handling'], 'header-and-manifest')
        with self.assertRaises(MetadataError):
            insert_header(b'<html>fixture</html>', {}, style=STYLES['html'],
                          handling=protected['handling'], file_kind='html', mode='100644')
        self.assertIn('testdata/legalnorm/fixture.html', delta['added_existing_class_paths'])
        self.assertEqual(policy.classify('docs/glossary/other.md')['handling'], 'header-and-manifest')
        self.assertIsNone(policy.classify('docs-site/unknown.js'))
        self.assertIsNone(policy.classify('internal/store/migrations/0033_unreviewed.sql'))
        self.assertEqual(delta['source_anchor_policy_blob_oid'],'f'*40)
        altered=dict(subject); altered['docs/engineering/file-metadata.md']=entry('docs/engineering/file-metadata.md','b')
        with self.assertRaises(BootstrapError): prepare_policy_delta(anchor_policy,'f'*40,anchor,altered)

    def test_logo_unknown_authorship_and_rights_are_preserved(self):
        with open(HERE/'testdata'/'anchor-policy.json', encoding='utf-8') as stream:
            anchor_policy=json.load(stream)
        all_paths={'docs-site/bun.lock','docs-site/docusaurus.config.js','docs-site/sidebars.js',
                   'docs/engineering/file-metadata.md','web/public/huangjueshu-competition-logo.png',
               'testdata/legalnorm/fixture.html',
               *GLOSSARY_VIEWS_RULE['paths'], *LITERAL_MARKER_RULE['paths'],
                   'internal/store/migrations/0027_contract_signing.sql',
                   'internal/store/migrations/0028_contract_fulfillment.sql',
                   'internal/store/migrations/0029_contract_payments.sql',
                   'internal/store/migrations/0030_contract_assets.sql',
                   'internal/store/migrations/0031_contract_source_observations.sql',
                   'internal/store/migrations/0032_clm_outbox_metrics.sql','deploy/app/Dockerfile'}
        def ent(path, letter):
            return TreeEntry(path,path.encode(),'100644','blob',letter*40,b'fixture')
        anchor={p:ent(p,'a') for p in ('docs/engineering/file-metadata.md','deploy/app/Dockerfile')}
        subject={p:ent(p,'a' if p in anchor else 'b') for p in all_paths}
        candidate,_=prepare_policy_delta(anchor_policy,'f'*40,anchor,subject)
        policy=mt.Policy(candidate)
        path='web/public/huangjueshu-competition-logo.png'
        rule=policy.classify(path)
        schema = mt.load_json(HERE.parents[1] / 'scripts/file_metadata/schema.json')
        evidence = SimpleNamespace(value='unknown', source={'kind':'unknown', 'reason':'No traceable history exists for this fixture.'})
        repo = SimpleNamespace(path_provenance=lambda *args, **kwargs: SimpleNamespace(created=evidence, updated=evidence))
        proposal = {'purpose':'Logo asset identifies the competition in the web application.',
                    'authors':[{'name':'unknown','role':'unknown','source':'Author source is not established for this asset.'}],
                    'copyright':[{'holder':'unknown','notice':'No per-file declaration was found; ownership is not inferred.',
                                  'source':'Asset metadata inspection requires rights-source follow-up.'}]}
        good = _record_new(path, None, proposal, policy, repo, 'a'*40, schema)
        _validate_bootstrap_record(path,good,policy)
        forged=dict(good,authors=[{'name':'Bai Junhao','role':'project-author'}])
        with self.assertRaises(mt.ManifestError): _validate_bootstrap_record(path,forged,policy)
        invalid = dict(good, purpose='short')
        failures = _validate_candidate_records({path: invalid}, policy)
        self.assertTrue(failures)
        self.assertEqual(failures[0]['path'], path)

if __name__=='__main__': unittest.main(verbosity=2)
