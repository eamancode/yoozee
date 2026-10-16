#!/usr/bin/env python3
"""台词"像不像人在现场说话"的尺子：只量、只报数，不判合格。挑候选和写自审时用。
python3 {{TOOLS}}/voice_qa.py FILE [--from s] [--to s] [--ref 声音样本.wav] [--text 台词]
量：
  音高中位、音高起伏（四分位距 Hz）；给了 --ref 就报相对样本的变化。喊、惊呼的音高应明显升高；起伏比闲聊样本还小，就是念得平
  语速（给了 --text，按字数 / 说话时长）
  死寂：说话区间内，每 0.1 秒音量低于 -70 dBFS 的格数。现场录音不会掉到数字静音；有就要在剪辑里铺声音床
  人声高出环境多少 dB：说话最响的三成对最静的三成的中位差。现场一般 20～35；偏高就是干、贴耳，靠声音床和空间感补
为什么不判合格：实测用户认可的补拍也会出现死寂和偏干，这两个数说明的是"剪辑要补什么"，不是"这条不能用"。
review_seg.py 调 measure() 把数写进候选报告。"""
import argparse, os, re, sys
import numpy as np
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import measure as M

DEAD_DB, GAP_HIGH = -70.0, 45.0   # 死寂的判定电平；人声高出环境超过这个数就提示"偏干"


def measure(file, frm=0.0, to=None, ref=None, text=None):
    """返回 dict：span（说话区间，秒）、f0、iqr、dead（格）、gap（dB）、notes（提示）；给了 ref 再有 rel_f0、rel_iqr（%），给了 text 再有 rate（字/秒）"""
    SR = M.SR
    x = M.decode(file, frm, (to - frm) if to else None)
    w = int(0.1 * SR)
    if len(x) < 2 * w:
        return {"span": (frm, frm), "f0": 0.0, "iqr": 0.0, "dead": 0, "gap": 0.0, "notes": ["没有声音"]}
    db = np.array([20 * np.log10(np.sqrt(np.mean(x[i:i + w] ** 2)) + 1e-12) for i in range(0, len(x) - w, w)])
    loud = np.where(db > db.max() - 30)[0]
    s0, s1 = (int(loud[0]), int(loud[-1]) + 1) if len(loud) else (0, len(db))
    span = db[s0:s1]
    f = M.f0_stats(x[s0 * w:s1 * w])
    dead = int(np.sum(span < DEAD_DB))
    srt = np.sort(span); k = max(1, len(srt) * 3 // 10)
    gap = float(np.median(srt[-k:]) - np.median(srt[:k]))
    r = {"span": (frm + s0 * 0.1, frm + s1 * 0.1), "f0": float(f.get("f0_median") or 0), "iqr": float(f.get("f0_iqr") or 0), "dead": dead, "gap": gap}
    notes = []
    if dead: notes.append(f"字缝死寂 {dead} 格（每格 0.1 秒），剪辑要铺声音床")
    if gap > GAP_HIGH: notes.append(f"人声高出环境 {gap:.0f} dB，偏干，靠声音床和空间感补")
    if ref:
        g = M.f0_stats(M.decode(ref))
        r["rel_f0"] = (r["f0"] / max(float(g.get("f0_median") or 1), 1) - 1) * 100
        r["rel_iqr"] = (r["iqr"] / max(float(g.get("f0_iqr") or 1), 1) - 1) * 100
        if r["rel_iqr"] < 0: notes.append(f"起伏比样本小 {-r['rel_iqr']:.0f}%，比闲聊还平")
    if text:
        n = len(re.sub(r"[\s，。、！？；：,.!?;:“”\"'…—]", "", text)); r["rate"] = n / max((s1 - s0) * 0.1, 0.1)
    r["notes"] = notes
    return r


if __name__ == "__main__":
    ap = argparse.ArgumentParser(); ap.add_argument("file"); ap.add_argument("--from", dest="frm", type=float, default=0.0); ap.add_argument("--to", type=float)
    ap.add_argument("--ref"); ap.add_argument("--text")
    a = ap.parse_args()
    r = measure(a.file, a.frm, a.to, a.ref, a.text)
    out = [f"说话区间 {r['span'][0]:.1f}～{r['span'][1]:.1f} 秒", f"音高 {r['f0']:.0f} Hz，起伏 {r['iqr']:.0f} Hz"]
    if "rel_f0" in r: out.append(f"相对样本：音高 {r['rel_f0']:+.0f}%，起伏 {r['rel_iqr']:+.0f}%")
    if "rate" in r: out.append(f"语速 {r['rate']:.1f} 字/秒")
    out.append(f"死寂 {r['dead']} 格"); out.append(f"人声高出环境 {r['gap']:.0f} dB")
    if r["notes"]: out.append("提示：" + "；".join(r["notes"]))
    print("；".join(out))
