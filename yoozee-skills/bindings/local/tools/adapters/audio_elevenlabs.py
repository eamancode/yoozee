#!/usr/bin/env python3
"""音乐与音效适配样例：ElevenLabs。music 单条配乐、sfx 音效和环境元素、score 整条分段作曲（composition_plan）。2026-10 实测，详见项目 工具档案/ElevenLabs.md。
分段作曲：python3 … score --plan 剪辑/配乐计划.json --out 剪辑/配乐/score_a.mp3 [--adherence high|medium]
  plan.json = {"positive":[…], "negative":[…], "chunks":[{"name":"intro","ms":16000,"text":"[Intro]\n{…每行不超过 200 字符…}"}, …]}

音乐： python3 {{TOOLS}}/adapters/audio_elevenlabs.py music --prompt "..." --ms 60000 --out 剪辑/配乐/theme.mp3 [--model music_v2_5] [--vocals]
音效： python3 {{TOOLS}}/adapters/audio_elevenlabs.py sfx --text "..." --seconds 4 --out 剪辑/音效/door.mp3 [--influence 0.4] [--loop]
批量： python3 {{TOOLS}}/adapters/audio_elevenlabs.py batch --jobs 剪辑/audio_jobs.json
       jobs.json = [{"id":"M01","kind":"music","prompt":"...","ms":60000,"out":"..."},
                    {"id":"X01","kind":"sfx","text":"...","seconds":3,"out":"..."}]
"""
import argparse
import json
import os
import pathlib
import re
import subprocess
import sys
import time

import requests

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C  # noqa: E402

TAG = "audio_elevenlabs"
BASE = "https://api.elevenlabs.io"


def _key():
    return C.need("ELEVENLABS_API_KEY")


def _post(path, body, params=None, timeout=600):
    err = None
    for attempt in range(5):
        try:
            r = requests.post(BASE + path, headers={"xi-api-key": _key(), "Content-Type": "application/json"},
                              params=params, data=json.dumps(body), timeout=timeout)
        except requests.RequestException as e:
            err = str(e)
            time.sleep(5)
            continue
        if r.status_code == 429 or r.status_code >= 500:
            err = f"HTTP {r.status_code} {r.text[:200]}"
            time.sleep(min(60, 10 * (attempt + 1)))
            continue
        if r.status_code != 200:
            raise RuntimeError(f"{path} 失败：HTTP {r.status_code} {r.text[:400]}")
        return r.content
    raise RuntimeError(f"{path} 失败：{err}")


def _stt_request(path):
    with open(C.abspath(path), "rb") as f:
        r = requests.post(BASE + "/v1/speech-to-text", headers={"xi-api-key": _key()}, data={"model_id": "scribe_v1", "tag_audio_events": "true"},
                          files={"file": (os.path.basename(path), f, "audio/mpeg")}, timeout=300)
    if not r.ok:
        raise RuntimeError(f"听写失败 HTTP {r.status_code}: {r.text[:300]}")
    return r.json()


def check_vocals(out):
    """配乐出完自动听写一遍（默认做法）：听写存成 <文件>.stt.txt；听出字就打 ⚠ 并返回 False。描述会被当歌词唱出来（2026-10-10 实测），这一步拦它。听写没跑成只记日志，不拦生成。"""
    try:
        d = _stt_request(out)
    except Exception as e:
        C.log(TAG, f"配乐听写没跑成，手动用 stt 适配听一遍：{out}（{e}）"); return None
    text = d.get("text", "").strip()
    words = re.findall(r"[\u4e00-\u9fff]|[A-Za-z]{2,}", re.sub(r"\[[^\]]*\]|\([^)]*\)", " ", text))
    pathlib.Path(C.abspath(out) + ".stt.txt").write_text(("⚠ 听出字：" if words else "没有人声：") + text + "\n", encoding="utf-8")
    if words:
        msg = f"⚠ {os.path.basename(out)} 里听出人声或歌词，不能用：{text[:120]}"; print(msg); C.log(TAG, msg); return False
    return True


def _save(data, out):
    out = pathlib.Path(C.abspath(out))
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_bytes(data)
    return str(out)


def music(prompt, ms, out, model="music_v2_5", instrumental=True, fmt="mp3_44100_128"):
    body = {"prompt": prompt, "music_length_ms": int(ms), "model_id": model, "force_instrumental": bool(instrumental)}
    try:
        data = _post("/v1/music", body, params={"output_format": fmt})
    except RuntimeError as e:
        if "model" in str(e).lower() and model != "music_v2":
            C.log(TAG, f"{model} 不可用，退回 music_v2：{e}")
            body["model_id"] = "music_v2"
            data = _post("/v1/music", body, params={"output_format": fmt})
        else:
            raise
    out = _save(data, out); check_vocals(out)
    return out


def sfx(text, seconds, out, influence=0.3, loop=False, fmt="mp3_44100_128"):
    body = {"text": text, "prompt_influence": float(influence), "model_id": "eleven_text_to_sound_v2"}
    if seconds:
        body["duration_seconds"] = float(seconds)
    if loop:
        body["loop"] = True
    data = _post("/v1/sound-generation", body, params={"output_format": fmt})
    return _save(data, out)


def score(plan_path, out, adherence="high"):
    """注意（2026-10-10）：每段 text 会被当歌词唱出来，要纯器乐别把描述写进 text，改用 music()（force_instrumental）。出完必须听写。
    整条分段作曲：同一动机、同一套乐器贯穿；出完按各段 ms 切成 out_<name>.mp3。text 每行 ≤200 字符，否则 422。"""
    plan = C.read_json(C.abspath(plan_path))
    chunks = [{"text": c["text"], "duration_ms": int(c["ms"]), "positive_styles": plan.get("positive", []), "negative_styles": plan.get("negative", []), "context_adherence": adherence} for c in plan["chunks"]]
    data = _post("/v1/music", {"composition_plan": {"chunks": chunks}, "model_id": plan.get("model", "music_v2_5")}, params={"output_format": "mp3_44100_192"}, timeout=900)
    out = _save(data, out); dur = C.ffprobe(out).get("duration") or 0
    total = sum(c["duration_ms"] for c in chunks) / 1000; k = dur / total if total else 1; t = 0.0
    for c in plan["chunks"]:
        d = c["ms"] / 1000 * k; cue = out.replace(".mp3", f"_{c['name']}.mp3")
        subprocess.run(["ffmpeg", "-v", "error", "-y", "-ss", f"{t:.3f}", "-t", f"{d:.3f}", "-i", out, "-c:a", "libmp3lame", "-q:a", "2", cue], check=True)
        C.log(TAG, f"  {c['name']}: {t:.1f}s + {d:.1f}s → {os.path.basename(cue)}"); t += d
    check_vocals(out)
    return out


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    sc = sub.add_parser("score"); sc.add_argument("--plan", required=True); sc.add_argument("--out", required=True); sc.add_argument("--adherence", default="high")
    m = sub.add_parser("music")
    m.add_argument("--prompt", required=True)
    m.add_argument("--ms", type=int, required=True)
    m.add_argument("--out", required=True)
    m.add_argument("--model", default="music_v2_5")
    m.add_argument("--vocals", action="store_true")
    s = sub.add_parser("sfx")
    s.add_argument("--text", required=True)
    s.add_argument("--seconds", type=float)
    s.add_argument("--out", required=True)
    s.add_argument("--influence", type=float, default=0.3)
    s.add_argument("--loop", action="store_true")
    b = sub.add_parser("batch")
    b.add_argument("--jobs", required=True)
    a = ap.parse_args()
    if a.cmd == "score":
        p = score(a.plan, a.out, a.adherence)
        C.log(TAG, f"✓ score {C.ffprobe(p).get('duration')}s → {p}")
    elif a.cmd == "music":
        p = music(a.prompt, a.ms, a.out, a.model, not a.vocals)
        C.log(TAG, f"✓ music {C.ffprobe(p).get('duration')}s → {p}")
    elif a.cmd == "sfx":
        p = sfx(a.text, a.seconds, a.out, a.influence, a.loop)
        C.log(TAG, f"✓ sfx {C.ffprobe(p).get('duration')}s → {p}")
    else:
        jobs = C.read_json(C.abspath(a.jobs))
        res = []
        for j in jobs:
            if not j.get("force") and os.path.exists(C.abspath(j["out"])):
                C.log(TAG, f"{j['id']} 已存在，跳过")
                res.append({"id": j["id"], "status": "exists"})
                continue
            try:
                if j["kind"] == "music":
                    p = music(j["prompt"], j["ms"], j["out"], j.get("model", "music_v2_5"), not j.get("vocals", False))
                else:
                    p = sfx(j["text"], j.get("seconds"), j["out"], j.get("influence", 0.3), j.get("loop", False))
                d = C.ffprobe(p).get("duration")
                C.log(TAG, f"{j['id']} ✓ {d}s → {j['out']}")
                res.append({"id": j["id"], "status": "done", "duration": d})
            except Exception as e:
                C.log(TAG, f"{j['id']} ✗ {e}")
                res.append({"id": j["id"], "status": "failed", "error": str(e)})
        C.write_json(C.abspath(a.jobs) + ".result.json", res)


if __name__ == "__main__":
    main()
