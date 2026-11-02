# File metadata (manifest mirror):
# Purpose: Combines parsed Store declarations, SQL migration references and candidate test calls into an explicit method inventory while retaining unknown ownership and external evidence maps.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-02
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

import csv,json,re,collections,pathlib
import argparse
parser=argparse.ArgumentParser()
parser.add_argument('--workspace', required=True)
P=pathlib.Path(parser.parse_args().workspace).resolve(); root=P/'source'; data=json.loads((P/'ast.json').read_text()); syms=data['symbols']; touched=set((P/'issue-146-files.txt').read_text().splitlines())
prod=[s for s in syms if s['file'].startswith('internal/store/') and not s['test']]; methods=[s for s in prod if s['receiver'].lstrip('*')=='Store']; tests=[s for s in syms if s['test']]; mig=sorted((root/'internal/store/migrations').glob('*.sql'))
pat=re.compile(r'\b(?:create\s+table\s+(?:if\s+not\s+exists\s+)?|alter\s+table\s+(?:if\s+exists\s+)?|drop\s+table\s+(?:if\s+exists\s+)?|references\s+)(?:(?:public|\{\{SCHEMA\}\})\.)?"?([a-z_][a-z_0-9]*)',re.I)
tables=collections.defaultdict(set)
for p in mig:
 text=p.read_text(); text=re.sub(r'--[^\n]*|/\*.*?\*/',' ',text,flags=re.S)
 for m in pat.finditer(text):tables[m.group(1).lower()].add(str(p.relative_to(root)))
# Name matches on string literals are explicit textual evidence only; no SQL parsing or dynamic reconstruction.
tablepat=re.compile(r'\b(?:'+ '|'.join(map(re.escape,sorted(tables,key=len,reverse=True)))+r')\b',re.I)
for s in syms:
 s['tables']=sorted({m.group().lower() for st in s['strings'] for m in tablepat.finditer(st)})
# Conservative direct SQL-based ownership; multi-domain and unresolved are UNKNOWN.
domainprefix={'clm_approval_':'Approval','clm_fulfillment_':'Fulfillment','clm_payment_':'Payment','clm_asset_':'Asset','subject_':'Subject','evidence_':'Evidence','operator_':'Operator','legal_':'Legal','governed_knowledge_':'Knowledge'}
exact={'subjects':'Subject','documents':'Document','parsed_documents':'Document','sessions':'Identity','audit_events':'Audit','business_audit_events':'Audit','contracts':'Contract'}
def domain(s):
 ds=set()
 for t in s['tables']:
  d=exact.get(t)
  if not d:
   d=next((v for k,v in domainprefix.items() if t.startswith(k)),None)
  if d:ds.add(d)
 # avoid assigning ownership on mixed tables without explicit review
 if len(ds)==1 and all(exact.get(t) or any(t.startswith(k) for k in domainprefix) for t in s['tables']):return next(iter(ds)),'provisional SQL table evidence; human review required'
 return 'UNKNOWN','ambiguous or insufficient direct SQL table evidence'
byname=collections.defaultdict(list)
for s in prod:byname[s['name']].append(s)
def key(s):return f"{s['file']}:{s['line']}:{s['receiver']}.{s['name']}"
# Direct lexical name matches are candidate edges, not type-resolved calls.
edges=[]; incoming=collections.defaultdict(list); testrefs=collections.defaultdict(list)
for s in syms:
 for c in s['calls']:
  name=c['name'].split('.')[-1]
  for target in byname.get(name,[]):
   if s['file'].startswith('internal/store/') or '.' in c['name']:
    edge={'caller':key(s),'call_line':c['line'],'call_expression':c['name'],'candidate_target':key(target),'confidence':'lexical candidate; receiver/type unresolved'}
    edges.append(edge); incoming[key(target)].append(edge)
    if s['test']:testrefs[key(target)].append(edge)
# Reachability as a conservative overapproximation; ambiguity is recorded.
legacy={'loadState','replaceRows','updateStateWithLocks'}; reachable={key(s) for s in prod if s['name'] in legacy}; changed=True
while changed:
 changed=False
 for e in edges:
  if e['candidate_target'] in reachable and e['caller'] not in reachable and e['caller'] in {key(x) for x in prod}:
   reachable.add(e['caller']);changed=True
with (P/'symbols.jsonl').open('w') as f:
 for s in prod:
  out={k:v for k,v in s.items() if k!='strings'};out['id']=key(s);out['issue_146_file']=s['file'] in touched;f.write(json.dumps(out,ensure_ascii=False)+'\n')
with (P/'candidate-call-edges.jsonl').open('w') as f:
 for e in edges:f.write(json.dumps(e,ensure_ascii=False)+'\n')
with (P/'store-ownership.csv').open('w',newline='') as f:
 w=csv.writer(f);w.writerow(['symbol','file','line','receiver','public','owner_domain','owner_evidence','sql_tables_literal_matches','candidate_callers','candidate_test_calls','legacy_writer_reachability','issue_146_file'])
 for s in methods:
  k=key(s);d,why=domain(s);w.writerow([s['name'],s['file'],s['line'],s['receiver'],s['name'][0].isupper(),d,why,';'.join(s['tables']),';'.join(sorted({e['caller'] for e in incoming[k]})),';'.join(sorted({e['caller'] for e in testrefs[k]})),'POSSIBLE lexical path; verify edges' if k in reachable else 'NOT_OBSERVED static lexical scan',s['file'] in touched])
with (P/'tables.csv').open('w',newline='') as f:
 w=csv.writer(f);w.writerow(['table','migration_mentions','store_symbol_string_mentions','store_receiver_methods']);
 for t,paths in sorted(tables.items()):
  use=[s for s in prod if t in s['tables']];w.writerow([t,';'.join(sorted(paths)),';'.join(key(s) for s in use),';'.join(key(s) for s in use if s['receiver'].lstrip('*')=='Store')])
files=sorted(str(p.relative_to(root)) for p in (root/'internal/store').glob('*.go') if not p.name.endswith('_test.go'));
with (P/'production-test-map.csv').open('w',newline='') as f:
 w=csv.writer(f);w.writerow(['production_file','production_symbols','candidate_tests_by_call_name','characterization_status','issue_146_file'])
 for file in files:
  ss=[s for s in prod if s['file']==file]; tt=sorted({e['caller'] for s in ss for e in testrefs[key(s)]});w.writerow([file,len(ss),';'.join(tt),'UNDECIDED: candidate references do not prove characterization coverage',file in touched])
with (P/'legacy-paths.jsonl').open('w') as f:
 for e in edges:
  if e['caller'] in reachable and e['candidate_target'] in reachable:f.write(json.dumps(e,ensure_ascii=False)+'\n')
# Compact file-level graph; callers in all repo to candidate store targets.
fileedges=collections.Counter((e['caller'].split(':')[0],e['candidate_target'].split(':')[0]) for e in edges)
with (P/'dependency-graph.dot').open('w') as f:
 f.write('digraph candidate_dependencies {\n  // Lexical call candidates; not type-resolved.\n')
 for (a,b),n in sorted(fileedges.items()):f.write('  '+json.dumps(a)+' -> '+json.dumps(b)+f' [label="{n}"];\n')
 f.write('}\n')
report={'source_sha':json.loads((P/'source-info.json').read_text())['source_sha'],'issue_146_commit':json.loads((P/'source-info.json').read_text())['issue_146_commit'],'migration_files':len(mig),'migration_first':mig[0].name,'migration_last':mig[-1].name,'migration_table_names':len(tables),'store_production_go_files':len(files),'store_production_functions_and_methods':len(prod),'store_receiver_methods':len(methods),'owner_counts':dict(collections.Counter(domain(s)[0] for s in methods)),'receiver_methods_possible_legacy':sum(key(s) in reachable for s in methods),'candidate_call_edges':len(edges),'store_files_without_candidate_test_calls':sum(not any(testrefs[key(s)] for s in prod if s['file']==file) for file in files),'production_type_const_var_declarations':len(json.loads((P/'declarations.json').read_text())), 'parse_errors':data['parse_errors']}
(P/'summary.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2))
