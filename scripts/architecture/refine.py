import csv,json,re,collections,pathlib
import argparse
parser=argparse.ArgumentParser()
parser.add_argument('--workspace', required=True)
P=pathlib.Path(parser.parse_args().workspace).resolve(); root=P/'source'; funcs=[json.loads(x) for x in (P/'symbols.jsonl').read_text().splitlines()]; typed=json.loads((P/'typed-calls.json').read_text()); ids={s['id']:s for s in funcs}; index=collections.defaultdict(list)
for s in funcs:index[s['receiver']+'.'+s['name']].append(s['id'])
edges=[];ambiguous=[]
for e in typed['edges']:
 targets=index[e['target']]
 if len(targets)==1:edges.append({**e,'target_id':targets[0]})
 elif len(targets)>1:ambiguous.append({**e,'candidate_targets':targets})
reachable={s['id'] for s in funcs if s['name'] in ('loadState','replaceRows','updateStateWithLocks')}; direct=collections.defaultdict(list);incoming=collections.defaultdict(list)
for e in edges:incoming[e['target_id']].append(e);direct[e['caller']].append(e)
changed=True
while changed:
 prev=len(reachable);reachable.update(e['caller'] for e in edges if e['target_id'] in reachable);changed=len(reachable)!=prev
(P/'typed-edges.jsonl').write_text(''.join(json.dumps(e)+'\n' for e in edges)); (P/'typed-ambiguous.json').write_text(json.dumps(ambiguous,indent=2)+'\n')
(P/'legacy-typed-edges.jsonl').write_text(''.join(json.dumps(e)+'\n' for e in edges if e['caller'] in reachable and e['target_id'] in reachable))
def legacy_path(start):
 queue=collections.deque([(start, [])]); seen={start}
 while queue:
  node,path=queue.popleft()
  if node in ids and ids[node]['name'] in ('loadState','replaceRows','updateStateWithLocks'):
   return ' -> '.join(path+[node])
  for edge in direct[node]:
   target=edge['target_id']
   if target not in seen:
    seen.add(target);queue.append((target,path+[node+'@'+str(edge['line'])]))
 return ''
old=list(csv.DictReader((P/'store-ownership.csv').open()));
for row in old:
 k=f"{row['file']}:{row['line']}:{row['receiver']}.{row['symbol']}";row['typed_production_callers']=';'.join(sorted({f"{e['caller']}@{e['line']}" for e in incoming[k]}));row['legacy_writer_reachability']='STATIC_PATH' if k in reachable else 'NOT_OBSERVED_STATIC';row['legacy_static_path']=legacy_path(k) if k in reachable else '';row['source_sha']=json.loads((P/'source-info.json').read_text())['source_sha'];row.pop('candidate_callers')
with (P/'store-ownership-candidate.csv').open('w',newline='') as f:
 w=csv.DictWriter(f,fieldnames=list(old[0]));w.writeheader();w.writerows(old)
# SQL verbs with literal table references; include catalog-only relations and all migrations.
cat=json.loads((root/'internal/store/table_catalog.json').read_text()); catnames={x['name']:x for x in cat['relations']}; prior={r['table']:r for r in csv.DictReader((P/'tables.csv').open())};names=set(catnames)|set(prior)
pat=re.compile(r'\b(?:from|join|update|into|table|references|truncate|delete\s+from)\s+(?:only\s+|if\s+(?:not\s+)?exists\s+)?(?:(?:public|\{\{SCHEMA\}\})\.)?"?([a-z_][a-z_0-9]*)',re.I)
references=collections.defaultdict(lambda:collections.defaultdict(set)); migfiles=sorted((root/'internal/store/migrations').glob('*.sql'))
for s in json.loads((P/'ast.json').read_text())['symbols']:
 if s['test']:continue
 for st in s['strings']:
  for m in pat.finditer(st):
   n=m.group(1).lower()
   if n in names:
    k=f"{s['file']}:{s['line']}:{s['receiver']}.{s['name']}";bucket='store_method' if s['file'].startswith('internal/store/') and s['receiver'].lstrip('*')=='Store' else ('store_other' if s['file'].startswith('internal/store/') else 'other_service');references[n][bucket].add(k)
for file in migfiles:
 for num,line in enumerate(file.read_text().splitlines(),1):
  for m in pat.finditer(line):
   n=m.group(1).lower()
   if n in names:references[n]['migration'].add(f'{file.relative_to(root)}:{num}')
with (P/'catalog-table-map.csv').open('w',newline='') as f:
 w=csv.writer(f);w.writerow(['table','catalog_class','migration_sql_lines','store_methods_sql_literals','other_store_symbols_sql_literals','other_service_symbols_sql_literals','status'])
 for n in sorted(names):
  ref=references[n];status='direct Store SQL literal' if ref['store_method'] else ('other Store SQL literal' if ref['store_other'] else ('external service SQL literal' if ref['other_service'] else ('migration-only observed; runtime owner UNKNOWN' if ref['migration'] else 'UNKNOWN: no SQL literal match')))
  w.writerow([n,catnames.get(n,{}).get('classification','NOT_IN_CATALOG')]+[';'.join(sorted(ref[k])) for k in ['migration','store_method','store_other','other_service']]+[status])
# Real characterization filenames and candidate same-stem tests, with line evidence, not coverage claims.
raw=json.loads((P/'ast.json').read_text())['symbols']; tests=[s for s in raw if s['test']]; production=sorted({str(p.relative_to(root)) for p in (root/'internal/store').glob('*.go') if not p.name.endswith('_test.go')}); orig={r['production_file']:r for r in csv.DictReader((P/'production-test-map.csv').open())}
with (P/'characterization-map.csv').open('w',newline='') as f:
 w=csv.writer(f);w.writerow(['production_file','explicit_characterization_test_candidates','other_test_candidates','test_assessment','issue_146_file'])
 for file in production:
  row=orig[file]; callers=set(row['candidate_tests_by_call_name'].split(';'))-{''};stem=pathlib.Path(file).stem
  named={f"{s['file']}:{s['line']}:{s['name']}" for s in tests if s['file'].startswith('internal/store/') and pathlib.Path(s['file']).name in (stem+'_test.go',stem+'_characterization_test.go') and s['name'].startswith('Test')}
  alltest=callers|named; char=sorted(x for x in alltest if 'characterization' in x.lower());other=sorted(alltest-set(char));status='candidate references require behavioral review' if alltest else 'GAP_CANDIDATE: no direct lexical or same-stem test evidence';w.writerow([file,';'.join(char),';'.join(other),status,row['issue_146_file']])
print(json.dumps({'typed_edges':len(edges),'ambiguous_typed_edges':len(ambiguous),'typed_static_legacy_store_methods':sum(r['legacy_writer_reachability']=='STATIC_PATH' for r in old),'catalog_relations':len(catnames),'catalog_and_migration_union':len(names),'catalog_not_migration_regex':sorted(set(catnames)-set(prior)),'migration_not_catalog':sorted(set(prior)-set(catnames)),'catalog_statuses':dict(collections.Counter(('direct Store SQL literal' if references[n]['store_method'] else 'other Store SQL literal' if references[n]['store_other'] else 'external service SQL literal' if references[n]['other_service'] else 'migration-only observed; runtime owner UNKNOWN' if references[n]['migration'] else 'UNKNOWN: no SQL literal match') for n in names))},indent=2))
