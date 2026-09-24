#!/usr/bin/env python3
# Prints every sentence over 30 words in a commit message and exits 1 if any exists.
import re, subprocess, sys
msg = subprocess.run(['git', '-C', sys.argv[1], 'log', '-1', '--format=%B', sys.argv[2]], capture_output=True, text=True, check=True).stdout
body = '\n'.join(l for l in msg.splitlines() if not l.startswith('Co-Authored-By'))
long = []
for para in re.split(r'\n\s*\n', body):
    for s in re.split(r'(?<=[.!?])\s+', ' '.join(para.split())):
        n = len(s.split())
        if n > 30:
            long.append((n, s))
for n, s in long:
    print(n, s)
sys.exit(1 if long else 0)
