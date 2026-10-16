#!/usr/bin/env python3
"""听写适配样例：ElevenLabs speech-to-text（scribe），逐词时间戳。第二把听写尺子：和本地 Whisper（qa.py stt）交叉核对台词，专有名词同音时以两把都听到为准；配乐和静音段上 Whisper 会幻听，以这把为准。
python3 {{TOOLS}}/adapters/stt_elevenlabs.py FILE [--from s] [--to s] [--model scribe_v1]"""
import argparse, json, os, subprocess, sys, tempfile
import requests
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C

ap = argparse.ArgumentParser(); ap.add_argument("file"); ap.add_argument("--from", dest="frm", type=float); ap.add_argument("--to", type=float); ap.add_argument("--model", default="scribe_v1")
a = ap.parse_args()
wav = os.path.join(tempfile.mkdtemp(), "a.mp3")
cmd = ["ffmpeg", "-v", "error", "-y"] + (["-ss", str(a.frm)] if a.frm else []) + (["-t", str(a.to - (a.frm or 0))] if a.to else []) + ["-i", a.file, "-vn", "-ac", "1", "-ar", "16000", wav]
subprocess.run(cmd, check=True)
with open(wav, "rb") as f:
    r = requests.post("https://api.elevenlabs.io/v1/speech-to-text", headers={"xi-api-key": C.need("ELEVENLABS_API_KEY")},
                      data={"model_id": a.model, "language_code": "zho", "timestamps_granularity": "word", "tag_audio_events": "true"},
                      files={"file": ("a.mp3", f, "audio/mpeg")}, timeout=300)
if not r.ok:
    sys.exit(f"听写失败 HTTP {r.status_code}: {r.text[:300]}")
d = r.json(); off = a.frm or 0
print("全文：", d.get("text", "").strip())
line, t0 = "", None
for w in d.get("words", []):
    if w.get("type") == "spacing":
        continue
    if t0 is None:
        t0 = w.get("start", 0)
    line += w.get("text", "")
    if line.endswith(("。", "！", "？", "，", ")", "]")) or len(line) > 24:
        print(f"{off + t0:7.2f}  {line}"); line, t0 = "", None
if line:
    print(f"{off + (t0 or 0):7.2f}  {line}")
