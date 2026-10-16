#!/usr/bin/env python3
"""一段候选的质检证据一键出（默认做法，可改）：每条候选出抽帧拼图（密度由 qa.py 按画面动静自动定）、听写（按分段表台词算字错率）、逐秒响度；带台词的再报台词量数（voice_qa.py：音高、起伏、死寂、人声高出环境多少，只报数不判合格）；最后出一张候选指标表。
python3 {{TOOLS}}/review_seg.py S03 [--fps N 固定帧率] [--model large-v3]
产出：生成/<段>_vK_sheet.jpg、生成/<段>_vK_stt.txt、生成/<段>_报数.txt。人脸表另用 qa.py faces（要看拼图定脸的位置）。"""
import argparse, glob, json, os, re, subprocess, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C
import voice_qa as V

ap = argparse.ArgumentParser(); ap.add_argument("seg"); ap.add_argument("--fps", type=float); ap.add_argument("--model", default="large-v3")
a = ap.parse_args()
T = os.path.dirname(os.path.abspath(__file__))
text = open(C.abspath("剧本/分段表.md"), encoding="utf-8").read()
seg = next(json.loads(b) for b in re.findall(r"```json\s*(\{.*?\})\s*```", text, re.S) if json.loads(b)["id"] == a.seg)
lines = "".join(ln["text"] for sh in seg.get("shots", []) for ln in sh.get("lines", []))
files = sorted(f for f in glob.glob(C.abspath(f"生成/{a.seg}_v*.mp4")) if re.search(r"_v\d+\.mp4$", f))
report = [f"{a.seg}  期望台词：{lines or '（无）'}  候选 {len(files)} 条"]
for f in files:
    stem = f[:-4]
    subprocess.run(["python3", os.path.join(T, "qa.py"), "sheet", f, "--cols", "4", "--out", stem + "_sheet.jpg"] + (["--fps", str(a.fps)] if a.fps else []), capture_output=True)
    cmd = ["python3", os.path.join(T, "qa.py"), "stt", f, "--model", a.model] + (["--expect", lines] if lines else [])
    stt = subprocess.run(cmd, capture_output=True, text=True).stdout.strip()
    open(stem + "_stt.txt", "w", encoding="utf-8").write(stt + "\n")
    lv = subprocess.run(["python3", os.path.join(T, "qa.py"), "levels", f], capture_output=True, text=True).stdout.strip().splitlines()
    info = C.ffprobe(f)
    report.append(f"\n== {os.path.basename(f)}  {info.get('width')}x{info.get('height')} {info.get('duration')}s\n听写：{stt or '（无）'}\n响度：{lv[-1] if lv else ''}")
    if lines:
        r = V.measure(f, text=lines)
        report.append(f"台词量数：音高 {r['f0']:.0f} Hz，起伏 {r['iqr']:.0f} Hz，语速 {r.get('rate', 0):.1f} 字/秒，死寂 {r['dead']} 格，人声高出环境 {r['gap']:.0f} dB" + ("；" + "；".join(r["notes"]) if r["notes"] else ""))
tk = subprocess.run(["python3", os.path.join(T, "pick.py"), "takes"] + files, capture_output=True, text=True).stdout.strip()
report.append("\n指标：\n" + tk)
open(C.abspath(f"生成/{a.seg}_报数.txt"), "w", encoding="utf-8").write("\n".join(report) + "\n")
print("\n".join(report))
