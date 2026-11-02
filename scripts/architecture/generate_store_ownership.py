#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Builds a fixed Git source snapshot in temporary storage, runs ownership analysis and writes reproducible review artifacts to an explicitly external empty output directory.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-02
# Updated: 2026-11-02
# Revision: r1
# Copyright: See LICENSE
# End file metadata.

"""Reproduce the store ownership inventory from an immutable Git commit."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

ISSUE_146 = 'a68af8616865296e4b63ff5811c9ae2ab8702025'
HERE = Path(__file__).resolve().parent

def run(args, *, cwd=None, stdout=None):
    return subprocess.run(args, cwd=cwd, stdout=stdout, check=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', required=True, type=Path)
    parser.add_argument('--source-sha', required=True, help='full immutable commit SHA')
    parser.add_argument('--out', required=True, type=Path, help='evidence directory outside the source repository')
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9a-f]{40}', args.source_sha):
        parser.error('--source-sha must be a full lowercase commit SHA')
    repo = Path(subprocess.check_output(['git', '-C', str(args.repo), 'rev-parse', '--show-toplevel'], text=True).strip()).resolve()
    out = args.out.resolve()
    if out == repo or repo in out.parents:
        parser.error('--out must be outside the source repository')
    actual = subprocess.check_output(['git', '-C', str(repo), 'rev-parse', '--verify', args.source_sha + '^{commit}'], text=True).strip()
    if actual != args.source_sha:
        parser.error('--source-sha must identify the commit itself')
    run(['git', '-C', str(repo), 'merge-base', '--is-ancestor', ISSUE_146, actual])
    if out.exists() and any(out.iterdir()):
        parser.error('--out must be absent or an empty directory')
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='store-ownership-') as tmp:
        workspace = Path(tmp)
        source = workspace / 'source'
        source.mkdir()
        # Git writes an archive of the specified commit; no worktree files are read.
        archive = workspace / 'source.tar'
        with archive.open('wb') as stream:
            run(['git', '-C', str(repo), 'archive', '--format=tar', actual], stdout=stream)
        run(['tar', '-xf', str(archive), '-C', str(source)])
        (workspace / 'source-info.json').write_text(json.dumps({'source_sha': actual, 'issue_146_commit': ISSUE_146}, indent=2) + '\n')
        touched = subprocess.check_output(['git', '-C', str(repo), 'diff-tree', '--no-commit-id', '--name-only', '-r', '-z', ISSUE_146])
        (workspace / 'issue-146-files.txt').write_text(''.join(p + '\n' for p in touched.decode('utf-8').split('\0') if p))
        jobs = [('store-ownership-extractor', 'ast.json'), ('store-declarations', 'declarations.json')]
        for directory, name in jobs:
            with (workspace / name).open('wb') as stream:
                run(['go', 'run', str(HERE / directory / 'main.go'), str(source)], cwd=source, stdout=stream)
        with (workspace / 'go-deps.json').open('wb') as stream:
            run(['go', 'list', '-deps', '-export', '-json', './...'], cwd=source, stdout=stream)
        with (workspace / 'typed-calls.json').open('wb') as stream:
            run(['go', 'run', str(HERE / 'store-call-resolver' / 'main.go'), str(source), str(workspace / 'go-deps.json')], cwd=source, stdout=stream)
        ast = json.loads((workspace / 'ast.json').read_text())
        typed = json.loads((workspace / 'typed-calls.json').read_text())
        if ast['parse_errors'] or typed['errors']:
            raise RuntimeError('source parsing or type checking failed; refusing incomplete inventory')
        for script in ('build_inventory.py', 'refine.py'):
            run([sys.executable, str(HERE / script), '--workspace', str(workspace)], cwd=source)
        # Keep review artifacts outside the repository; candidate CSV can be copied after review.
        for path in workspace.iterdir():
            if path.is_file() and path.name != 'source.tar':
                shutil.copy2(path, out / path.name)
    print(f'Inventory for {actual}: {out}')

if __name__ == '__main__':
    main()
