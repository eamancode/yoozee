#!/usr/bin/env python3
"""候选的指标表（只报数，不替你选）。指标只用来筛掉不合格的；合格的候选谁更好，看抽帧、按表演和戏的需要定。加 --pick 才按内置打分自动选一条。
  takes FILES... [--pick]            镜头候选：每条的峰值响度和落点、喊声音高、画面动作幅度、时长
  cues  FILES... [--pick]            单条配乐候选：2～8kHz 占比、>5kHz 占比、峰均比、每 3 秒能量曲线
  score --dir D --cands a,b,c --sections intro,montage,home,ending [--rise montage] [--build home] [--audible intro] [--pick]
                                     整条分段配乐候选：每段的开场可闻、推进段起点和升幅、情绪段峰值位置和推进、整条高频占比；--pick 时拷成 D/sel_<段>.mp3 并写 selection.json"""
import argparse, json, os, shutil, subprocess, sys
import numpy as np
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import measure as M


def curve(x, step=3):
    n = M.SR * step
    return [20 * np.log10(np.sqrt(np.mean(x[i:i + n] ** 2)) + 1e-9) for i in range(0, len(x) - n // 2, n)]


def takes(a):
    rows = []
    for p in a.files:
        x = M.decode(p); win = int(0.3 * M.SR)
        rms = [20 * np.log10(np.sqrt(np.mean(x[i:i + win] ** 2)) + 1e-9) for i in range(0, len(x) - win, win // 2)]
        peak = max(rms); tp = int(np.argmax(rms)) * win // 2 / M.SR
        f0 = M.f0_stats(x[max(0, int((tp - 0.4) * M.SR)):int((tp + 0.6) * M.SR)])
        raw = subprocess.run(["ffmpeg", "-v", "error", "-i", p, "-vf", "fps=8,scale=160:-1,format=gray", "-f", "rawvideo", "-"], capture_output=True).stdout
        fr = np.frombuffer(raw, dtype=np.uint8); fw = 160 * 68
        motion = float(np.mean(np.abs(np.diff(fr[: len(fr) // fw * fw].reshape(-1, fw).astype(np.int16), axis=0)))) if len(fr) >= fw * 2 else 0
        rows.append((p, peak, tp, f0.get("f0_median"), motion, len(x) / M.SR))
        print(f"{os.path.basename(p):<24} 峰值 {peak:6.1f} dB @ {tp:4.1f}s  音高 {str(f0.get('f0_median')):>6}Hz  动作 {motion:4.1f}  时长 {len(x) / M.SR:4.1f}s")
    if a.pick:
        best = max(rows, key=lambda r: r[1] + 0.03 * (r[3] or 0) + r[4]); print("→ 自动选（最激烈）：", best[0])


def cues(a):
    rows = []
    for p in a.files:
        x = M.decode(p); h, vh = M.band_ratio_db(x, 2000, 8000), M.band_ratio_db(x, 5000, 8000)
        crest = 20 * np.log10(np.max(np.abs(x)) / (np.sqrt(np.mean(x ** 2)) + 1e-9))
        rows.append((p, h + 0.5 * vh))
        print(f"{os.path.basename(p):<24} 2-8k {h:6.1f} dB  >5k {vh:6.1f} dB  峰均比 {crest:4.1f}  每3秒 {' '.join(f'{v:4.0f}' for v in curve(x))}")
    if a.pick:
        print("→ 自动选（高频最低）：", min(rows, key=lambda r: r[1])[0])


def score(a):
    secs = a.sections.split(","); rows = []
    for c in a.cands.split(","):
        files = {s: os.path.join(a.dir, f"score_{c}_{s}.mp3") for s in secs}
        if not all(os.path.exists(f) for f in files.values()):
            continue
        whole = os.path.join(a.dir, f"score_{c}.mp3"); h = M.band_ratio_db(M.decode(whole if os.path.exists(whole) else files[secs[0]]), 2000, 8000)
        sc, notes, peak_t = -0.5 * h, [], None
        if a.audible and a.audible in files:
            lvl = float(np.mean(curve(M.decode(files[a.audible])) or [-99])); ok = lvl > -45; sc += 3 if ok else -4; notes.append(f"{a.audible} {'可闻' if ok else '静音 ✗'}")
        if a.rise and a.rise in files:
            cm = curve(M.decode(files[a.rise])); start = float(np.mean(cm[:2])); rise = float(np.mean(cm[-2:]) - start); sc += 0.6 * rise + (2 if start > -34 else -3); notes.append(f"{a.rise} 起 {start:.0f} 升 {rise:+.1f}")
        if a.build and a.build in files:
            ch = curve(M.decode(files[a.build])); i = int(np.argmax(ch)); peak_t = 3 * i; build = float(ch[i] - np.mean(ch[:2])); sc += 0.6 * build + (1.5 if peak_t >= 24 else 0); notes.append(f"{a.build} 峰 {peak_t}s 推进 {build:+.1f}")
        rows.append((sc, c, peak_t, files)); print(f"score_{c}: 2-8k {h:5.1f} | {' | '.join(notes)} | 打分 {sc:5.1f}")
    if a.pick and rows:
        sc, c, peak_t, files = max(rows, key=lambda r: r[0])
        for s, f in files.items():
            shutil.copy(f, os.path.join(a.dir, f"sel_{s}.mp3"))
        json.dump({"score": f"score_{c}", "build_peak_t": peak_t}, open(os.path.join(a.dir, "selection.json"), "w"), ensure_ascii=False, indent=1); print("→ 自动选：", f"score_{c}")


ap = argparse.ArgumentParser(); sp = ap.add_subparsers(dest="cmd", required=True)
p = sp.add_parser("takes"); p.add_argument("files", nargs="+"); p.add_argument("--pick", action="store_true"); p.set_defaults(f=takes)
p = sp.add_parser("cues"); p.add_argument("files", nargs="+"); p.add_argument("--pick", action="store_true"); p.set_defaults(f=cues)
p = sp.add_parser("score"); p.add_argument("--dir", required=True); p.add_argument("--cands", required=True); p.add_argument("--sections", required=True); p.add_argument("--rise"); p.add_argument("--build"); p.add_argument("--audible"); p.add_argument("--pick", action="store_true"); p.set_defaults(f=score)
a = ap.parse_args(); a.f(a)
