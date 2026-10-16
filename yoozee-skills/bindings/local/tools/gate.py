#!/usr/bin/env python3
"""门禁：成片交付前的硬检查。不过就退出码 2。只量结果，不管做法。
python3 {{TOOLS}}/gate.py final/片名.mp4 [--sync 剪辑/sync.json] [--prev final/_旧版_v2/x.mp4] [--final-dir final] [--allow 80-83] [--target -14]
查：同步差 ≤1 帧，入点、出点不在片内切点的 2 帧以内（都来自 assemble.py 写的 sync.json）；无连续 2 秒低于 -45 dBFS 的无声洞（--allow 列出刻意的）；无单秒高于 -6 dBFS；
    整合响度 目标±1（目标读 sync.json 里剪辑表写的 TARGET_I，或 --target；都没有就不过）、真峰值 ≤-1.5；2～8kHz 占比不高于上一版；final 里只有一个成片、没有 .env、文件名无密钥。"""
import argparse, glob, json, os, re, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import measure as M
import common as C

ap = argparse.ArgumentParser()
ap.add_argument("film"); ap.add_argument("--sync", default=str(C.ROOT / "剪辑" / "sync.json")); ap.add_argument("--prev"); ap.add_argument("--final-dir", default=os.path.dirname(os.path.abspath(sys.argv[1])) if len(sys.argv) > 1 else "final")
ap.add_argument("--allow", default="", help="刻意无声的秒段，逗号分隔，如 80-83,120-121"); ap.add_argument("--target", type=float, help="整合响度目标；不给就读 sync.json 里剪辑表写的 TARGET_I")
a = ap.parse_args()
ok = True; target = a.target
def check(cond, msg):
    global ok
    print(("✓ " if cond else "✗ ") + msg); ok = ok and cond

nf, fps, dur = M.frames_and_duration(a.film)
x = M.decode(a.film); adur = len(x) / M.SR
check(abs(nf / fps - adur) <= 1.5 / fps, f"画面 {nf / fps:.3f}s 与声音 {adur:.3f}s 长度一致")
if a.sync and os.path.exists(a.sync):
    s = json.load(open(a.sync)); diff = abs(s.get("actual", 0) - s.get("nominal", 0))
    check(diff <= 1.0 / fps + 1e-6, f"剪辑表名义 {s.get('nominal'):.3f}s 与实际 {s.get('actual'):.3f}s 差 {diff:.3f}s（≤1 帧）")
    if target is None:
        target = s.get("target_i")
    cw = s.get("cut_warnings", [])
    check(not cw, "入点、出点都不在片内切点的 2 帧以内" + (f"：{len(cw)} 处会闪别的镜头，" + "；".join(cw) if cw else ""))
else:
    print("· 没有 sync.json，跳过剪辑表同步和切点检查（assemble.py 会写它）")
vals = M.per_second_db(x); holes, peaks = M.holes_and_peaks(vals)
allow = []
for seg in filter(None, a.allow.split(",")):
    p, q = seg.split("-"); allow.append((int(p), int(q)))
bad_holes = [h for h in holes if not any(h[0] >= p and h[1] <= q for p, q in allow)]
check(not bad_holes, f"无声洞（连续 2 秒 < -45 dBFS）：{bad_holes or '无'}" + (f"；刻意的 {holes and [h for h in holes if h not in bad_holes]}" if allow else ""))
check(not peaks, f"爆音（单秒 > -6 dBFS）：{peaks or '无'}")
L = M.loudness(a.film)
if target is None:
    check(False, f"整合响度 {L['lufs']} LUFS，但没有目标：在剪辑表顶上填 TARGET_I（按需求文档的平台定），或传 --target")
else:
    check(abs(L["lufs"] - target) <= 1.0, f"整合响度 {L['lufs']} LUFS（目标 {target}±1）")
check(L["tp"] <= -1.5, f"真峰值 {L['tp']} dBFS（≤ -1.5）")
h = M.band_ratio_db(x, 2000, 8000)
if a.prev and os.path.exists(a.prev):
    hp = M.band_ratio_db(M.decode(a.prev), 2000, 8000)
    print(("✓ " if h <= hp + 0.3 else "⚠ ") + f"2～8kHz 占比 {h:.1f} dB（上一版 {hp:.1f}；升高只是警告，刻意要更亮的声音时在需求文档里说明）")
else:
    print(f"· 2～8kHz 占比 {h:.1f} dB（越低越柔；没有上一版可比）")
fd = a.final_dir
mp4s = [p for p in glob.glob(os.path.join(fd, "*.mp4"))]
check(len(mp4s) == 1, f"{fd}/ 里成片只有一个：{[os.path.basename(p) for p in mp4s]}")
check(not glob.glob(os.path.join(fd, ".env*")), f"{fd}/ 里没有 .env")
check(not re.search(r"sk-|AKIA|ghp_", " ".join(os.listdir(fd))), f"{fd}/ 文件名里没有密钥样的字符串")
print("门禁通过" if ok else "门禁未过，不许交付")
sys.exit(0 if ok else 2)
