#!/usr/bin/env python3
# File metadata (manifest mirror):
# Purpose: Loads fixed target-base gate controllers and dependencies from Git blobs while binding their execution paths to a clean subject checkout and preserving controller exit results.
# Author: Bai Junhao <baijunhao@cq.chinamobile.com>
# Contact: +86 13617696321
# Created: 2026-11-03
# Updated: 2026-11-03
# Revision: r2
# Copyright: See LICENSE
# End file metadata.

"""Run a fixed BASE gate controller against a clean, fixed subject checkout.

This protects controller selection against accidental candidate changes. It is
not a security boundary against a modified workflow or a compromised host.
"""
import argparse
import os
from pathlib import Path
import re
import subprocess
import sys
import types

CONTROLLERS = {
    'fast': ('scripts/check_fast.py', ('go_test_scope',)),
    'quality': ('scripts/verify.py', ('source_identity',)),
    'architecture': ('scripts/architecture/check_architecture.py', ()),
}


def git(repo, *args):
    result = subprocess.run(['git', '-C', str(repo), *args], stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, check=False)
    if result.returncode:
        raise RuntimeError(f"git {' '.join(args)}: {result.stderr.decode(errors='replace').strip()}")
    return result.stdout


def require_clean(repo, subject):
    head = git(repo, 'rev-parse', '--verify', 'HEAD^{commit}').decode().strip()
    if head != subject:
        raise RuntimeError(f'HEAD {head} does not match subject {subject}')
    if git(repo, 'status', '--porcelain=v1', '-z', '--untracked-files=normal'):
        raise RuntimeError('subject working tree is dirty')


def read_blob(repo, base, name):
    entries = git(repo, 'ls-tree', '-z', base, '--', name).split(b'\0')
    entries = [entry for entry in entries if entry]
    if len(entries) != 1:
        raise RuntimeError(f'BASE missing required controller or dependency: {name}')
    metadata, filename = entries[0].split(b'\t', 1)
    mode, kind, oid = metadata.decode().split()
    if filename.decode() != name or mode not in ('100644', '100755') or kind != 'blob':
        raise RuntimeError(f'BASE contains invalid controller or dependency: {name}')
    return git(repo, 'cat-file', 'blob', oid)


def execute(repo, base, subject, mode, arguments):
    controller, dependencies = CONTROLLERS[mode]
    # Load every required BASE blob before executing any controller code.
    source = read_blob(repo, base, controller)
    blobs = {name: read_blob(repo, base, f'scripts/{name}.py') for name in dependencies}
    for name, blob in blobs.items():
        module = types.ModuleType(name)
        module.__file__ = str(repo / 'scripts' / (name + '.py'))
        sys.modules[name] = module
        exec(compile(blob, module.__file__, 'exec'), module.__dict__)
    target = str(repo / controller)
    os.chdir(repo)
    os.environ['CAW_TRUSTED_GATE_RUNNER'] = str(Path(__file__).resolve())
    os.environ['CAW_TRUSTED_GATE_BASE'] = base
    os.environ['CAW_TRUSTED_GATE_SUBJECT'] = subject
    if arguments and arguments[0] == '--':
        arguments = arguments[1:]
    reserved = {'--base', '--root'} if mode == 'fast' else ({'--base', '--repo', '--fixture'} if mode == 'architecture' else set())
    if any(arg.split('=', 1)[0] in reserved for arg in arguments):
        raise RuntimeError('controller identity arguments are reserved by the runner')
    if mode == 'fast':
        arguments = ['--base', base, *arguments]
    if mode == 'architecture':
        arguments = ['--repo', str(repo), '--base', base, *arguments]
    sys.argv = [target, *arguments]
    namespace = {'__name__': '__main__', '__file__': target, '__package__': None}
    exec(compile(source, target, 'exec'), namespace)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=CONTROLLERS)
    parser.add_argument('--repo', required=True)
    parser.add_argument('--base', required=True)
    parser.add_argument('--subject', required=True)
    args, remaining = parser.parse_known_args()
    for label, sha in [('base', args.base), ('subject', args.subject)]:
        if not re.fullmatch(r'[0-9a-f]{40}', sha):
            raise RuntimeError(f'{label} must be a full lowercase SHA-1 commit ID')
    repo = Path(args.repo).resolve(strict=True)
    for sha in (args.base, args.subject):
        actual = git(repo, 'rev-parse', '--verify', sha + '^{commit}').decode().strip()
        if actual != sha:
            raise RuntimeError('commit identity mismatch')
    require_clean(repo, args.subject)
    try:
        execute(repo, args.base, args.subject, args.mode, remaining)
    except SystemExit as exc:
        code = exc.code
    else:
        code = 0
    require_clean(repo, args.subject)
    return code


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (RuntimeError, OSError, ValueError) as exc:
        print(f'trusted gate BLOCKED: {exc}', file=sys.stderr)
        raise SystemExit(2)
