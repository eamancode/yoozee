#!/usr/bin/env python3
"""参考视频拆解的本机帮手（yz-analyze）：抽帧给模型看，并写出契约 analysis 的骨架，模型填完后用 check.py analysis 校验。
  python3 {{TOOLS}}/analyze.py 参考/打斗.mp4 [--fps 3] [--key 4.2-5.0 --key 9.8-10.6] [--out 参考/拆解/打斗.json]
产出：
  参考/拆解/<名>_sheet.jpg            低密度抽帧拼图（默认 3 帧/秒，按时间顺序带时间码）
  参考/拆解/<名>_key_<from>-<to>.jpg   关键动作段逐帧拼图（原始帧率，启动前 / 接触 / 回收都在里面）
  参考/拆解/<名>.json                   analysis 骨架：source 已填（时长、帧率、是否有音轨、精度、能力限制），其余留空由模型填
拆解纪律见 kb-action › cards/参考视频逐帧拆解模板.md：只写看得见听得见的；命中需两项证据；遮挡标不确定；摄影机运动与人物位移分开。"""
import argparse
import json
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402


def frames(src, out_dir, fps=None, start=None, end=None):
    os.makedirs(out_dir, exist_ok=True)
    cmd = ["ffmpeg", "-v", "error", "-y"]
    if start is not None:
        cmd += ["-ss", str(start)]
    cmd += ["-i", src]
    if end is not None and start is not None:
        cmd += ["-t", str(end - start)]
    if fps:
        cmd += ["-vf", f"fps={fps}"]
    cmd += [os.path.join(out_dir, "f_%05d.jpg")]
    subprocess.run(cmd, check=True)
    return sorted(os.path.join(out_dir, f) for f in os.listdir(out_dir) if f.endswith(".jpg"))


def sheet(files, out, cols, t0, dt, label):
    from PIL import Image, ImageDraw
    if not files:
        return None
    thumbs = [Image.open(f) for f in files]
    w, h = thumbs[0].size
    scale = 320 / w
    tw, th = int(w * scale), int(h * scale)
    rows = (len(thumbs) + cols - 1) // cols
    canvas = Image.new("RGB", (cols * tw, rows * (th + 18)), "black")
    d = ImageDraw.Draw(canvas)
    for i, im in enumerate(thumbs):
        x, y = (i % cols) * tw, (i // cols) * (th + 18)
        canvas.paste(im.resize((tw, th)), (x, y + 18))
        d.text((x + 4, y + 2), f"{label} {t0 + i * dt:6.2f}s", fill="white")
    canvas.save(out, quality=85)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("video")
    ap.add_argument("--fps", type=float, default=3)
    ap.add_argument("--key", action="append", default=[], help="关键动作段 from-to（秒），按原始帧率逐帧")
    ap.add_argument("--out")
    a = ap.parse_args()
    src = C.abspath(a.video)
    stem = os.path.splitext(os.path.basename(src))[0]
    out_dir = C.abspath("参考/拆解")
    os.makedirs(out_dir, exist_ok=True)
    info = C.ffprobe(src)
    if not info:
        sys.exit("ffprobe 读不到这个文件：确认 ffmpeg 已安装、文件可读")
    fps_txt = info.get("fps") or "0/1"
    try:
        num, den = fps_txt.split("/")
        fps = float(num) / float(den)
    except Exception:
        fps = 0.0
    with tempfile.TemporaryDirectory() as tmp:
        fs = frames(src, os.path.join(tmp, "low"), fps=a.fps)
        sheet(fs, os.path.join(out_dir, f"{stem}_sheet.jpg"), 6, 0.0, 1.0 / a.fps, "t=")
        for rng in a.key:
            f0, f1 = (float(x) for x in rng.split("-"))
            kf = frames(src, os.path.join(tmp, f"key_{f0}_{f1}"), start=f0, end=f1)
            sheet(kf, os.path.join(out_dir, f"{stem}_key_{f0}-{f1}.jpg"), 8, f0, 1.0 / fps if fps else 0.04, "f")
    skeleton = {
        "source": {"file": a.video, "duration": info.get("duration"), "fps": round(fps, 3), "variable_fps": False, "has_cuts": False, "has_audio": bool(info.get("has_audio")),
                   "precision": f"低密度 {a.fps} 帧/秒；关键段逐帧（{round(fps, 1)} fps）" if a.key else f"低密度 {a.fps} 帧/秒（关键段未逐帧，判断命中时置信度不得为 high）",
                   "limits": "" if a.key else "未指定关键段：加 --key from-to 后重跑可逐帧"},
        "actors": [], "space": {}, "chain": "", "phases": [], "key_actions": [], "stats": {}, "audio_timeline": [], "camera": [], "narrative": "", "uncertain": [], "verdict": ""}
    out = C.abspath(a.out) if a.out else os.path.join(out_dir, f"{stem}.json")
    if not os.path.exists(out):
        C.write_json(out, skeleton)
        print(f"✓ 骨架 {os.path.relpath(out, C.ROOT)}（模型按模板填 actors / phases / key_actions …，再 check.py analysis）")
    else:
        print(f"· 已有 {os.path.relpath(out, C.ROOT)}，不覆盖")
    print(f"✓ 拼图 {os.path.relpath(os.path.join(out_dir, stem + '_sheet.jpg'), C.ROOT)}" + (f" + {len(a.key)} 段关键帧" if a.key else ""))


if __name__ == "__main__":
    main()
