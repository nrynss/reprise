"""Review probe. Print every comment sentence over 30 words in one file.

Run from the worktree root:
python3 long_sentences.py web/src/routes/threads/render-clock.test.ts
It exits 1 when any sentence runs past 30 words.
"""
import re
import sys

blocks, cur = [], []
for line in open(sys.argv[1], encoding="utf-8"):
    m = re.match(r"^\s*//\s?(.*)", line)
    if m:
        cur.append(m.group(1))
        continue
    if cur:
        blocks.append(" ".join(cur))
        cur = []
if cur:
    blocks.append(" ".join(cur))
bad = 0
for block in blocks:
    for sentence in re.split(r"(?<=[.?!])\s+", block):
        n = len(sentence.split())
        if n > 30:
            print(n, sentence)
            bad += 1
sys.exit(1 if bad else 0)
