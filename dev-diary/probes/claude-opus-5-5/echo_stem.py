#!/usr/bin/env python3
"""Echo of the host in the local user stem, on the episode clock.

The provider's right channel says when the host spoke. The user stem is placed on that
clock by the measured offset (align). For host-only spans, report the stem level against
the stem's own quiet floor, and how closely the stem's loudness envelope follows the host's.
  echo_stem.py <sess_id> <offset_ms>
"""
import json, sys
import numpy as np
import aai_sessions as a

sid, off_ms = sys.argv[1], float(sys.argv[2])
d = a.OUT / sid
tl = json.loads((d / "timeline.json").read_text())
t0 = tl["started_at_unix_ms"]
fr = 200.0
x = a.pcm(d / "audio.ogg")
host = a.envelope(x[:, 1].astype(float))
user = a.envelope(a.load_stem(d / "stem-user.wav", None))
shift = int(round(-off_ms / 1000 * fr))  # stem index = provider index + shift

user_spans = []
for t in tl["turns"]:
    s, e = t.get("user_speech_started_at_ms"), t.get("user_speech_ended_at_ms")
    if s:
        user_spans.append(((s - t0) / 1000, ((e or s + 3000) - t0) / 1000))

def talking_user(ts):
    return any(p - 0.3 <= ts <= q + 0.3 for p, q in user_spans)

host_only, quiet = [], []
for i in range(len(host)):
    j = i + shift
    if j < 0 or j >= len(user):
        continue
    ts = i / fr
    if talking_user(ts):
        continue
    if host[i] > -35:
        host_only.append((i, j))
    elif host[i] < -55:
        quiet.append(j)

def lin(db):
    return 10 ** (np.asarray(db) / 20)

if not host_only or not quiet:
    sys.exit("not enough host-only or quiet frames")
hi = np.array([i for i, _ in host_only]); uj = np.array([j for _, j in host_only])
u_host = 20 * np.log10(np.sqrt(np.mean(lin(user[uj]) ** 2)))
u_quiet = 20 * np.log10(np.sqrt(np.mean(lin(user[np.array(quiet)]) ** 2)))
h_lvl = 20 * np.log10(np.sqrt(np.mean(lin(host[hi]) ** 2)))
# Envelope correlation, searching echo path delays of 0 to 300 ms.
best = (0.0, 0)
for lag in range(0, 61):
    uu = user[np.clip(uj + lag, 0, len(user) - 1)]
    c = np.corrcoef(uu, host[hi])[0, 1]
    if c > best[0]:
        best = (c, lag)
print(f"host-only frames {len(hi)/fr:.1f}s, quiet frames {len(quiet)/fr:.1f}s")
print(f"host level {h_lvl:.1f} dBFS | user stem during host-only {u_host:.1f} dBFS | user stem quiet floor {u_quiet:.1f} dBFS")
print(f"rise over floor while host talks: {u_host - u_quiet:+.1f} dB | "
      f"envelope corr {best[0]:.3f} at {best[1]*5} ms")
