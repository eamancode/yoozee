#!/usr/bin/env python3
"""房间声床（默认做法，可改）：每场用两三个真实声音元素叠成 40 秒可循环的床，全部 60Hz 高通、3.5kHz 低通、压 2.8kHz，归一到 -30 LUFS。
python3 {{TOOLS}}/roomtone.py beds.json --out 素材库/声音床 [--dur 40] [--lufs -30]
beds.json = {"office_night": {"elements": [["剪辑/元素/城市车流.mp3", 0], ["剪辑/元素/空调出风.mp3", -8]], "lowpass": 3500, "cut_2800": -4}, ...}  后两个可省
教训：不要让模型出"room tone / ambience"并开 loop，出来几乎全是 150Hz 以下的隆隆声；要按具体声音（钟、车流、院子、隔墙电视、鸟）分别生成，12～15 秒，由这里循环并交叉淡化。也不要加粉噪、棕噪当"空气声"，听起来像耳鸣。"""
import argparse, json, os, re, subprocess, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import measure as M

ap = argparse.ArgumentParser(); ap.add_argument("beds"); ap.add_argument("--out", required=True); ap.add_argument("--dur", type=int, default=40); ap.add_argument("--lufs", type=float, default=-30.0)
a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
for name, cfg in json.load(open(a.beds)).items():
    els = [(p, g) for p, g in cfg["elements"] if os.path.exists(p)]
    if not els:
        print(name, "没有元素文件"); continue
    inputs, chains, labels = [], [], []
    for k, (p, g) in enumerate(els):
        inputs += ["-stream_loop", "-1", "-i", p]
        gain = (a.lufs - M.loudness(p)["lufs"]) + g
        chains.append(f"[{k}:a]atrim=0:{a.dur},asetpts=PTS-STARTPTS,highpass=f={cfg.get('highpass', 60)},lowpass=f={cfg.get('lowpass', 3500)},equalizer=f=2800:t=q:w=1.5:g={cfg.get('cut_2800', -4)},volume={gain:.1f}dB[e{k}]"); labels.append(f"[e{k}]")
    chains.append("".join(labels) + f"amix=inputs={len(labels)}:normalize=0,aformat=channel_layouts=stereo,afade=t=in:d=0.5,afade=t=out:st={a.dur - 0.5}:d=0.5[o]")
    out = os.path.join(a.out, f"{name}.wav")
    r = subprocess.run(["ffmpeg", "-v", "error", "-y"] + inputs + ["-filter_complex", ";".join(chains), "-map", "[o]", "-ar", "48000", "-t", str(a.dur), out], capture_output=True, text=True)
    if r.returncode:
        print(name, "失败", r.stderr[-200:]); continue
    g = a.lufs - M.loudness(out)["lufs"]; tmp = out + ".tmp.wav"
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-i", out, "-af", f"volume={g:.1f}dB", tmp], check=True); os.replace(tmp, out)
    x = M.decode(out); print(f"{name}: {M.loudness(out)['lufs']:.1f} LUFS，<150Hz 占比 {M.band_ratio_db(x, 20, 150):.1f} dB，{len(els)} 个元素 → {out}")
