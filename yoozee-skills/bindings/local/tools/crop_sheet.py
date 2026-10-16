#!/usr/bin/env python3
"""三视图裁单图（默认做法，可改）：灰底三视图（特写 | 全身正面 | 全身侧面）切成三张，存成 名字_特写 / 名字_全身正面 / 名字_全身侧面。
python3 {{TOOLS}}/crop_sheet.py 素材库/人物/林书白_三视图.png [--out-dir 素材库/人物] [--labels 特写,全身正面,全身侧面]
找法：每行用最右 3% 的像素估背景色（灰底常有上下渐变），逐列算"非背景"占比；在宽度 25%～58% 和 58%～88% 两段里各取内容最少的一列下刀；
每块再收紧到有内容的列、左右留 3% 灰边。切口处内容占比 >8% 就报"要人看一眼"。"""
import argparse, os
import numpy as np
from PIL import Image

ap = argparse.ArgumentParser()
ap.add_argument("sheet"); ap.add_argument("--out-dir"); ap.add_argument("--labels", default="特写,全身正面,全身侧面")
ap.add_argument("--thr", type=float, default=60)
ap.add_argument("--min-ratio", type=float, default=0.5625, help="宽/高低于它就左右补背景灰（H3 参考图要求宽高比 0.4～2.5）")
a = ap.parse_args()
im = Image.open(a.sheet).convert("RGB"); x = np.asarray(im).astype(np.float32); H, W, _ = x.shape
bg_row = np.median(x[:, int(W * 0.97):], axis=1)                       # 每行的背景色
content = np.abs(x - bg_row[:, None, :]).sum(axis=2) > a.thr
col = np.convolve(content.mean(axis=0), np.ones(15) / 15, mode="same")


def valley(lo, hi):
    i0, i1 = int(W * lo), int(W * hi)
    return i0 + int(np.argmin(col[i0:i1]))


c1, c2 = valley(0.25, 0.58), valley(0.58, 0.88)
warn = [f"x={c} 处内容占比 {col[c]:.2f}" for c in (c1, c2) if col[c] > 0.08]
stem = os.path.basename(a.sheet).rsplit("_三视图", 1)[0]
out_dir = a.out_dir or os.path.dirname(a.sheet)
for (x0, x1), lab in zip([(0, c1), (c1, c2), (c2, W)], a.labels.split(",")):
    cols = np.where(col[x0:x1] > 0.03)[0]
    pad = int(0.03 * W)
    c0, cE = (max(x0, x0 + cols[0] - pad), min(x1, x0 + cols[-1] + pad)) if len(cols) else (x0, x1)
    piece = im.crop((c0, 0, cE, H))
    if piece.width / H < a.min_ratio:                       # 太窄的竖条左右补灰，补到 min_ratio
        newW = int(round(H * a.min_ratio)); canvas = Image.new("RGB", (newW, H), tuple(int(v) for v in np.median(bg_row, axis=0)))
        canvas.paste(piece, ((newW - piece.width) // 2, 0)); piece = canvas
    p = os.path.join(out_dir, f"{stem}_{lab}.png"); piece.save(p)
    print(f"{p}  {piece.width}x{H}  宽高比 {piece.width / H:.2f}")
print(f"切口 x={c1}、x={c2}" + ("；要人看一眼：" + "；".join(warn) if warn else ""))
