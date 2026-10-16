#!/usr/bin/env python3
"""配音适配样例：MiniMax speech-2.8-hd + voice_design。2026-10 实测，详见 kb-dialects › minimax-h3/档案.md 的配音一节。
MiniMax 语音：音色设计 + 台词合成（speech-2.8-hd）。

设计音色： python3 {{TOOLS}}/adapters/tts_minimax.py design --name liu --prompt "..." --preview "试听文本" [--voice-id custom_id]
合成台词： python3 {{TOOLS}}/adapters/tts_minimax.py speak --voice-id ID --text "..." --out 素材库/声音/台词/L01.wav
            [--model speech-2.8-hd] [--speed 1.0] [--pitch 0] [--vol 1.0] [--emotion sad] [--lang Chinese] [--format wav|mp3]
批量：     python3 {{TOOLS}}/adapters/tts_minimax.py batch --jobs 素材库/声音/台词.json
            lines.json = [{"id":"L01","voice_id":"...","text":"...","out":"素材库/声音/台词/L01.wav","speed":1.0,"emotion":"calm"}]
系统音色： python3 {{TOOLS}}/adapters/tts_minimax.py voices [--type system|all]
设计好的音色登记在 素材库/声音/voices.json（name → voice_id），试听在 素材库/声音/试听/。
"""
import argparse
import json
import os
import pathlib
import sys
import time

import requests

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import common as C  # noqa: E402

TAG = "tts_minimax"
HOSTS = ["https://api.minimax.io", "https://api.minimaxi.com"]
REG = C.ROOT / "素材库" / "声音" / "voices.json"


class Speech:
    def __init__(self):
        self.key = C.need("MINIMAX_API_KEY")
        h = os.environ.get("MINIMAX_API_HOST", "").rstrip("/")
        self.hosts = [h] if h else HOSTS[:]
        self.host, self.locked = self.hosts[0], bool(h)
        self.group = os.environ.get("MINIMAX_GROUP_ID", "")

    def _url(self, path):
        u = self.host + path
        if self.group:
            u += ("&" if "?" in u else "?") + f"GroupId={self.group}"
        return u

    def _post(self, path, body):
        err = None
        for attempt in range(6):
            try:
                r = requests.post(self._url(path), headers={"Authorization": f"Bearer {self.key}", "Content-Type": "application/json"},
                                  data=json.dumps(body), timeout=300)
            except requests.RequestException as e:
                err = str(e)
                time.sleep(5)
                continue
            if r.status_code in (401, 403) and not self.locked and len(self.hosts) > 1:
                alt = [x for x in self.hosts if x != self.host][0]
                C.log(TAG, f"{self.host} 鉴权失败，改用 {alt}")
                self.host, self.locked = alt, True
                continue
            if r.status_code == 429 or r.status_code >= 500:
                err = f"HTTP {r.status_code} {r.text[:200]}"
                time.sleep(min(60, 10 * (attempt + 1)))
                continue
            try:
                d = r.json()
            except ValueError:
                raise RuntimeError(f"非 JSON 响应 HTTP {r.status_code}: {r.text[:300]}")
            br = d.get("base_resp") or {}
            if br.get("status_code", 0) != 0 or r.status_code != 200:
                if br.get("status_code") in (1004, 2049) and not self.locked and len(self.hosts) > 1:
                    alt = [x for x in self.hosts if x != self.host][0]
                    C.log(TAG, f"{self.host} 鉴权失败({br})，改用 {alt}")
                    self.host, self.locked = alt, True
                    continue
                raise RuntimeError(f"{path} 失败：HTTP {r.status_code} {json.dumps(d, ensure_ascii=False)[:400]}")
            return d
        raise RuntimeError(f"{path} 失败：{err}")

    def speak(self, text, voice_id, out, model="speech-2.8-hd", speed=1.0, pitch=0, vol=1.0,
              emotion=None, lang="Chinese", fmt="wav"):
        vs = {"voice_id": voice_id, "speed": float(speed), "vol": float(vol), "pitch": int(pitch)}
        if emotion:
            vs["emotion"] = emotion
        body = {"model": model, "text": text, "stream": False, "output_format": "hex",
                "voice_setting": vs,
                "audio_setting": {"sample_rate": 44100, "bitrate": 256000, "format": fmt, "channel": 1}}
        if lang:
            body["language_boost"] = lang
        d = self._post("/v1/t2a_v2", body)
        hexdata = (d.get("data") or {}).get("audio")
        if not hexdata:
            raise RuntimeError(f"无音频数据：{json.dumps(d, ensure_ascii=False)[:300]}")
        out = pathlib.Path(C.abspath(out))
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(bytes.fromhex(hexdata))
        extra = d.get("extra_info") or {}
        return str(out), extra

    def design(self, prompt, preview_text, voice_id=None):
        body = {"prompt": prompt, "preview_text": preview_text}
        if voice_id:
            body["voice_id"] = voice_id
        d = self._post("/v1/voice_design", body)
        return d.get("voice_id"), d.get("trial_audio"), d

    def voices(self, vtype="system"):
        return self._post("/v1/get_voice", {"voice_type": vtype})


def load_reg():
    return C.read_json(REG) if REG.exists() else {}


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    d = sub.add_parser("design")
    d.add_argument("--name", required=True)
    d.add_argument("--prompt", required=True)
    d.add_argument("--preview", required=True)
    d.add_argument("--voice-id")
    s = sub.add_parser("speak")
    s.add_argument("--voice-id", required=True)
    s.add_argument("--text", required=True)
    s.add_argument("--out", required=True)
    s.add_argument("--model", default="speech-2.8-hd")
    s.add_argument("--speed", type=float, default=1.0)
    s.add_argument("--pitch", type=int, default=0)
    s.add_argument("--vol", type=float, default=1.0)
    s.add_argument("--emotion")
    s.add_argument("--lang", default="Chinese")
    s.add_argument("--format", default="wav")
    b = sub.add_parser("batch")
    b.add_argument("--jobs", required=True)
    b.add_argument("--model", default="speech-2.8-hd")
    v = sub.add_parser("voices")
    v.add_argument("--type", default="system")
    a = ap.parse_args()
    sp = Speech()
    if a.cmd == "design":
        vid, trial, raw = sp.design(a.prompt, a.preview, a.voice_id)
        reg = load_reg()
        reg[a.name] = {"voice_id": vid, "prompt": a.prompt, "preview": a.preview}
        C.write_json(REG, reg)
        if trial:
            p = C.ROOT / "素材库" / "声音" / "试听" / f"{a.name}_试听.mp3"
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_bytes(bytes.fromhex(trial))
            C.log(TAG, f"音色 {a.name} = {vid}，试听 → {p}")
        else:
            C.log(TAG, f"音色 {a.name} = {vid}（无试听音频）{json.dumps(raw, ensure_ascii=False)[:200]}")
    elif a.cmd == "speak":
        path, extra = sp.speak(a.text, a.voice_id, a.out, a.model, a.speed, a.pitch, a.vol, a.emotion, a.lang, a.format)
        C.log(TAG, f"✓ {path} {extra.get('audio_length', '?')}ms")
    elif a.cmd == "batch":
        jobs = C.read_json(C.abspath(a.jobs))
        reg = load_reg()
        res = []
        for j in jobs:
            vid = j.get("voice_id") or reg.get(j.get("voice", ""), {}).get("voice_id")
            if not vid:
                C.log(TAG, f"{j['id']} 没有 voice_id（voice={j.get('voice')}）")
                res.append({"id": j["id"], "status": "no_voice"})
                continue
            try:
                path, extra = sp.speak(j["text"], vid, j["out"], j.get("model", a.model), j.get("speed", 1.0),
                                       j.get("pitch", 0), j.get("vol", 1.0), j.get("emotion"), j.get("lang", "Chinese"),
                                       j.get("format", "wav"))
                info = C.ffprobe(path)
                C.log(TAG, f"{j['id']} ✓ {info.get('duration')}s {j['text'][:20]}")
                res.append({"id": j["id"], "out": j["out"], "status": "done", "duration": info.get("duration")})
            except Exception as e:
                C.log(TAG, f"{j['id']} ✗ {e}")
                res.append({"id": j["id"], "status": "failed", "error": str(e)})
        C.write_json(C.abspath(a.jobs) + ".result.json", res)
    elif a.cmd == "voices":
        print(json.dumps(sp.voices(a.type), ensure_ascii=False, indent=2)[:20000])


if __name__ == "__main__":
    main()
