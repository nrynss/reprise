#!/usr/bin/env python3
"""Read AssemblyAI voice sessions for the live-sessions runs.

The key is read from the repository .env file and never printed or exported.

  aai_sessions.py list [N]          newest N sessions (default 10)
  aai_sessions.py fetch <sess_id>   download artifacts into ./runs/<sess_id>/ and measure
  aai_sessions.py align <sess_id> <stem> [rate] [left|right]
                                    drift of the local user stem against the provider's
                                    left channel. A raw stem is s16le mono. With no rate,
                                    24000, 48000 and 44100 are tried and the best kept.
"""
import json
import pathlib
import subprocess
import sys
import urllib.request

ROOT = pathlib.Path("/home/nryn/work/reprise")
BASE = "https://agents.assemblyai.com/v1/sessions"
OUT = pathlib.Path(__file__).resolve().parent / "runs"


def key():
    for line in (ROOT / ".env").read_text().splitlines():
        if line.startswith("ASSEMBLYAI_API_KEY="):
            return line.split("=", 1)[1].strip().strip('"')
    sys.exit("ASSEMBLYAI_API_KEY missing from .env")


def get(url, auth=True):
    req = urllib.request.Request(url)
    if auth:
        req.add_header("Authorization", key())
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def cmd_list(n):
    body = json.loads(get(f"{BASE}?limit={n}"))
    rows = body.get("sessions", body.get("data", body)) if isinstance(body, dict) else body
    for s in rows[:n]:
        print(f"{s.get('id')}  {s.get('status'):<10} {str(s.get('duration_seconds')):<12} "
              f"{s.get('public_close_reason')!s:<14} {s.get('created_at')}  {s.get('ended_at')}")


def ffmpeg_stats(path, channel):
    """Integrated loudness, true peak and RMS of one channel."""
    pan = "pan=mono|c0=c0" if channel == "left" else "pan=mono|c0=c1"
    r = subprocess.run(
        ["ffmpeg", "-hide_banner", "-nostats", "-i", str(path), "-af",
         f"{pan},ebur128=peak=true:framelog=quiet,astats=metadata=0:reset=0", "-f", "null", "-"],
        capture_output=True, text=True)
    keep = [l.strip() for l in r.stderr.splitlines()
            if any(k in l for k in ("I:", "LRA:", "Peak:", "RMS level dB", "Noise floor dB"))]
    return keep


def pcm(path):
    import numpy as np
    raw = subprocess.run(["ffmpeg", "-v", "error", "-i", str(path), "-ac", "2", "-ar", "24000",
                          "-f", "f32le", "-"], capture_output=True, check=True).stdout
    return np.frombuffer(raw, dtype=np.float32).reshape(-1, 2)


def db(x):
    import numpy as np
    return 20 * np.log10(max(float(np.sqrt(np.mean(x.astype(np.float64) ** 2))), 1e-9))


def echo_report(d):
    """Leak of the host (right) into the user channel (left) while only the host talks."""
    import numpy as np
    tl = json.loads((d / "timeline.json").read_text())
    t0 = tl["started_at_unix_ms"]
    x = pcm(d / "audio.ogg")
    sr = 24000
    user_spans = [(t["user_speech_started_at_ms"], t["user_speech_ended_at_ms"]) for t in tl.get("turns", [])
                  if t.get("user_speech_started_at_ms")]
    turns = tl.get("turns", [])
    print(f"turns={len(turns)} interrupted={sum(1 for t in turns if t.get('interrupted_at_ms'))}")
    for t in turns:
        print(f"  [{t.get('trigger')}] user={t.get('user_transcript')!r} "
              f"ttfa_ms={t.get('time_to_first_audio_ms')} interrupted={t.get('interrupted_at_ms') is not None}")
    for i, t in enumerate(turns):
        a, b = t.get("agent_reply_started_at_ms"), t.get("interrupted_at_ms") or t.get("agent_reply_ended_at_ms")
        if not a or not b:
            continue
        if any(us < b and (ue or us + 3000) > a for us, ue in user_spans):
            print(f"  reply {i}: overlaps user speech, skipped")
            continue
        s0, s1 = int((a - t0) / 1000 * sr), int((b - t0) / 1000 * sr)
        u, h = x[s0:s1, 0].astype(np.float64), x[s0:s1, 1].astype(np.float64)
        if len(h) < sr // 2:
            continue
        best, lag = 0.0, 0
        n = len(h)
        for L in range(0, int(0.5 * sr), 48):
            uu, hh = u[L:], h[:n - L]
            den = np.sqrt(np.dot(uu, uu) * np.dot(hh, hh))
            c = abs(np.dot(uu, hh)) / den if den > 0 else 0.0
            if c > best:
                best, lag = c, L
        print(f"  reply {i}: {len(h)/sr:.1f}s host {db(h):.1f} dBFS, user channel {db(u):.1f} dBFS, "
              f"leak {db(u)-db(h):.1f} dB, max xcorr {best:.3f} at {lag/sr*1000:.0f} ms")


def load_stem(path, rate):
    import numpy as np
    fmt = [] if rate is None else ["-f", "s16le", "-ar", str(rate), "-ac", "1"]
    raw = subprocess.run(["ffmpeg", "-v", "error", *fmt, "-i", str(path), "-ac", "1", "-ar", "24000",
                          "-f", "f32le", "-"], capture_output=True, check=True).stdout
    return np.frombuffer(raw, dtype=np.float32).astype(np.float64)


def locate(win, ref, guess, span):
    """Lag of win inside ref near guess, by normalised FFT cross-correlation."""
    import numpy as np
    lo, hi = max(0, guess - span), min(len(ref), guess + len(win) + span)
    seg = ref[lo:hi]
    if len(seg) < len(win):
        return None, 0.0
    n = 1 << int(np.ceil(np.log2(len(seg) + len(win))))
    c = np.fft.irfft(np.fft.rfft(seg, n) * np.conj(np.fft.rfft(win, n)), n)[:len(seg) - len(win) + 1]
    cs = np.concatenate(([0.0], np.cumsum(seg ** 2)))
    e = np.sqrt(np.maximum(cs[len(win):] - cs[:-len(win)], 0)) * np.sqrt(np.dot(win, win))
    r = np.abs(c) / np.maximum(e, 1e-12)
    k = int(np.argmax(r))
    return lo + k, float(r[k])


def envelope(x, hop=120):
    """RMS envelope at 200 Hz from 24 kHz samples, in dB, floored at -60."""
    import numpy as np
    n = len(x) // hop
    e = np.sqrt(np.mean(x[:n * hop].reshape(n, hop) ** 2, axis=1))
    return np.maximum(20 * np.log10(np.maximum(e, 1e-9)), -60.0)


def ncc(win, seg):
    """Normalised cross-correlation of win at every lag inside seg (mean removed per window)."""
    import numpy as np
    m = len(win)
    w = win - win.mean()
    wn = np.sqrt(np.dot(w, w))
    n = 1 << int(np.ceil(np.log2(len(seg) + m)))
    c = np.fft.irfft(np.fft.rfft(seg, n) * np.conj(np.fft.rfft(w, n)), n)[:len(seg) - m + 1]
    cs = np.concatenate(([0.0], np.cumsum(seg)))
    cs2 = np.concatenate(([0.0], np.cumsum(seg ** 2)))
    mu = (cs[m:] - cs[:-m]) / m
    var = np.maximum((cs2[m:] - cs2[:-m]) - m * mu ** 2, 1e-9)
    return c / (np.sqrt(var) * max(wn, 1e-9))


def cmd_align(sid, stem, rate, channel="left"):
    """Offset of the local stem inside the provider channel, measured at many points.

    Opus, resampling and browser gain processing break sample level matching, so the
    match runs on 5 ms loudness envelopes and refines each peak by parabolic fit.
    """
    import numpy as np
    d = OUT / sid
    ref = envelope(pcm(d / "audio.ogg")[:, 0 if channel == "left" else 1].astype(np.float64))
    fr = 200.0
    raw = pathlib.Path(stem).suffix.lower() in (".pcm", ".raw", "") or bool(rate)
    loc = envelope(load_stem(stem, rate if raw else None))
    print(f"stem_seconds={len(loc)/fr:.2f} provider_seconds={len(ref)/fr:.2f}")
    # Coarse offset: the whole stem against the whole reference.
    full = ncc(loc, np.concatenate((np.full(len(loc), -60.0), ref, np.full(len(loc), -60.0))))
    coarse = int(np.argmax(full)) - len(loc)
    print(f"coarse offset {coarse/fr*1000:+.0f} ms (stem t=0 sits at provider t={coarse/fr:+.2f}s), "
          f"whole-take corr {full.max():.3f}")
    win, span = int(4 * fr), int(0.5 * fr)
    rows = []
    for h in range(0, len(loc) - win, int(2 * fr)):
        w = loc[h:h + win]
        if np.ptp(w) < 20:  # no speech onset in this window
            continue
        lo = h + coarse - span
        if lo < 0 or lo + win + 2 * span > len(ref):
            continue
        r = ncc(w, ref[lo:lo + win + 2 * span])
        k = int(np.argmax(r))
        frac = 0.0
        if 0 < k < len(r) - 1:
            a, b, c = r[k - 1], r[k], r[k + 1]
            den = a - 2 * b + c
            frac = 0.5 * (a - c) / den if den != 0 else 0.0
        off = (lo + k + frac - h) / fr * 1000
        rows.append((h / fr, off, float(r[k])))
    for t, off, r in rows:
        print(f"  stem t={t:7.2f}s  offset={off:9.1f} ms  corr={r:.3f}")
    good = [(t, off) for t, off, r in rows if r >= 0.7]
    if len(good) >= 2:
        t = np.array([g[0] for g in good])
        o = np.array([g[1] for g in good])
        slope = np.polyfit(t, o, 1)[0]
        print(f"drift over {np.ptp(t):.1f}s ({len(good)} windows, corr>=0.7): "
              f"{o[-1]-o[0]:+.1f} ms end to end, fit {slope*1000:+.0f} ppm, "
              f"median offset {np.median(o):.1f} ms, spread {np.ptp(o):.1f} ms")
    else:
        print("drift: fewer than two confident windows")


def cmd_fetch(sid):
    meta = json.loads(get(f"{BASE}/{sid}"))
    d = OUT / sid
    d.mkdir(parents=True, exist_ok=True)
    (d / "session.json").write_text(json.dumps(meta, indent=2))
    print(f"id={meta.get('id')} status={meta.get('status')} "
          f"duration_seconds={meta.get('duration_seconds')} close={meta.get('public_close_reason')}")
    print(f"created_at={meta.get('created_at')} ended_at={meta.get('ended_at')}")
    arts = meta.get("artifacts") or []
    if isinstance(arts, dict):
        arts = [dict(v, kind=k) if isinstance(v, dict) else {"kind": k, "url": v} for k, v in arts.items()]
    for a in arts:
        url = a.get("url")
        if not url:
            continue
        ctype = a.get("content_type", a.get("type", ""))
        name = ("audio.ogg" if "audio" in ctype or a.get("kind") == "audio"
                else f"{a.get('kind', 'artifact')}.json")
        if "timeline" in json.dumps(a):
            name = "timeline.json"
        elif "metadata" in json.dumps(a):
            name = "metadata.json"
        (d / name).write_bytes(get(url, auth=False))
        print(f"saved {name}")
    audio = d / "audio.ogg"
    if audio.exists():
        p = subprocess.run(["ffprobe", "-v", "error", "-show_entries",
                            "stream=codec_name,channels,sample_rate:format=duration",
                            "-of", "default=nw=1", str(audio)], capture_output=True, text=True)
        print(p.stdout.strip())
        for ch in ("left", "right"):
            print(f"[{ch} = {'user' if ch == 'left' else 'host'}]")
            for l in ffmpeg_stats(audio, ch):
                print("  " + l)
        if (d / "timeline.json").exists():
            echo_report(d)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    if sys.argv[1] == "list":
        cmd_list(int(sys.argv[2]) if len(sys.argv) > 2 else 10)
    elif sys.argv[1] == "fetch":
        cmd_fetch(sys.argv[2])
    elif sys.argv[1] == "align":
        cmd_align(sys.argv[2], sys.argv[3], int(sys.argv[4]) if len(sys.argv) > 4 and sys.argv[4] != "0" else None,
                  sys.argv[5] if len(sys.argv) > 5 else "left")
    else:
        sys.exit(__doc__)
