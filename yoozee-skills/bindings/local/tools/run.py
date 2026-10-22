#!/usr/bin/env python3
"""一次到底总控：从需求到资产图、分镜脚本、投递包，代码能做的全自动，缺模型写的产物就停下来告诉它写什么。
  python3 {{TOOLS}}/run.py [--episode 1] [--submit batch|manual] [--skip-images] [--parallel 2] [--target 15-25]
退出码：0 全部完成；10 缺一份由模型写的产物（输出里写明按哪个技能写哪个文件）；2 某道门禁有 error（输出里有 check 的结果）。
每次运行把进度写到 生成/run.json，人读的在 生成/run清单.md。能续跑：已有且过检的产物跳过，已存在的图不重出。
一次到底 = 不问人：warn 记进清单，error 才停；出图直接跑（.env 没有 IMAGE_API_KEY 时跳过出图并记一条）；
视频不在这里生成——--submit batch 把本集全部段打成投递包（manifest + 粘贴版），--submit manual 出逐段提交清单，由人或柚子逐个提交。"""
import argparse
import datetime
import glob
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import common as C  # noqa: E402

TOOLS = os.path.dirname(os.path.abspath(__file__))
PY = sys.executable


class Run:
    def __init__(self, episode, submit, skip_images, parallel, target):
        self.ep = episode
        self.submit = submit
        self.skip_images = skip_images
        self.parallel = parallel
        self.target = target
        self.log = []
        self.state = {"started": datetime.datetime.now().isoformat(timespec="seconds"), "episode": episode, "steps": []}

    def say(self, line):
        print(line)
        self.log.append(line)

    def step(self, name, status, detail=""):
        self.state["steps"].append({"step": name, "status": status, "detail": detail})
        self.say(f"[{status}] {name}" + (f"：{detail}" if detail else ""))

    def sh(self, args, capture=True):
        env = dict(os.environ, YOOZEE_ROOT=str(C.ROOT), PYTHONIOENCODING="utf-8")
        r = subprocess.run([PY, *args], cwd=str(C.ROOT), env=env, capture_output=capture, text=True, encoding="utf-8", errors="replace")
        return r.returncode, (r.stdout or "") + (r.stderr or "")

    def check(self, kind, *extra):
        code, out = self.sh([os.path.join(TOOLS, "check.py"), kind, *extra])
        warns = [l for l in out.splitlines() if l.startswith("⚠")]
        errs = [l for l in out.splitlines() if l.startswith("✗")]
        return code, errs, warns

    def need_model(self, path, skill, what):
        self.step(path, "缺", f"由模型按 {skill} 写：{what}")
        self.finish(10)

    def finish(self, code):
        self.state["finished"] = datetime.datetime.now().isoformat(timespec="seconds")
        self.state["exit"] = code
        os.makedirs(C.abspath("生成"), exist_ok=True)
        C.write_json(C.abspath("生成/run.json"), self.state)
        with open(C.abspath("生成/run清单.md"), "w", encoding="utf-8") as f:
            f.write(f"# 一次到底运行清单（第 {self.ep} 集）\n\n" + "\n".join(f"- {l}" for l in self.log) + "\n")
        sys.exit(code)

    def gate(self, kind, *extra):
        code, errs, warns = self.check(kind, *extra)
        if errs:
            self.step(f"门禁 {kind}", "未过", "；".join(errs[:6]))
            self.finish(2)
        self.step(f"门禁 {kind}", "过", f"{len(warns)} 条 warn" + ("：" + "；".join(w[:80] for w in warns[:4]) if warns else ""))

    # ------------------------------------------------------------ steps
    def run(self):
        ep = f"E{self.ep:02d}"
        if not os.path.exists(C.abspath("需求.json")):
            self.need_model("需求.json", "yz-brief", "需求契约（script_mode / style_profile / video_model / platform / episodes / 一次到底 auth_mode=full）")
        self.gate("brief")
        brief = C.project_brief() or {}
        mode = brief.get("script_mode") or "original"
        if mode in ("original", "adapt") and not os.path.exists(C.abspath("剧本/剧本.json")):
            if not os.path.exists(C.abspath("故事/故事.json")):
                self.need_model("故事/故事.json", "yz-story", "故事契约（duanju_fast 五项 / 完整法六步）")
            self.gate("story")
        if not os.path.exists(C.abspath("剧本/剧本.json")):
            self.need_model("剧本/剧本.json", "yz-script", "剧本契约：全部集数，节拍带 id，单句 ≤15 字")
        self.gate("script")
        if not os.path.exists(C.abspath("剧本/资产圣经.json")):
            self.need_model("剧本/资产圣经.json", "yz-extract", "资产圣经骨架：人物（锚点 / 锁定卡 / voice_profile）、道具、场景（space_type / 大白话 / 锚点 / 光源）、伏笔、两人以上场的空间图（approved）")
        self.gate("bible", "--stage", "shots")
        self.assets()
        shots_path = C.abspath("剧本/镜头表.json")
        if not os.path.exists(shots_path):
            self.need_model("剧本/镜头表.json", "yz-direct（动作场 yz-fight）", f"第 {self.ep} 集镜头表：坐标机位、站位锚点、情绪弧、灯光七项、台词三元、构图、衔接")
        sl = C.read_json(shots_path)
        if int(sl.get("episode") or 1) != self.ep:
            self.need_model("剧本/镜头表.json", "yz-direct", f"现有镜头表是第 {sl.get('episode')} 集的；第 {self.ep} 集请先把它另存为 剧本/镜头表_E{sl.get('episode'):02d}.json 再写新的")
        self.gate("shots")
        code, out = self.sh([os.path.join(TOOLS, "pack.py"), "--target", self.target])
        if code != 0:
            self.step("装段", "未过", out.strip()[-400:])
            self.finish(2)
        self.step("装段", "过", out.strip().splitlines()[-1] if out.strip() else "")
        self.gate("segments")
        code, out = self.sh([os.path.join(TOOLS, "compile.py"), "all"])
        if code != 0:
            self.step("编译", "未过", out.strip()[-400:])
            self.finish(2)
        segs = [s for s in C.read_json(C.abspath("剧本/分段.json")) if str(s.get("scene", "")).startswith(ep)]
        os.makedirs(C.abspath("生成"), exist_ok=True)
        for s in segs:
            self.sh([os.path.join(TOOLS, "compile.py"), s["id"], "--out", f"生成/{s['id']}_prompt.json"])
            c2, paste = self.sh([os.path.join(TOOLS, "compile.py"), s["id"], "--paste"])
            with open(C.abspath(f"生成/{s['id']}_粘贴版.txt"), "w", encoding="utf-8") as f:
                f.write(paste)
        self.step("编译", "过", f"{len(segs)} 段 → 生成/S0x_prompt.json + 粘贴版")
        self.sh([os.path.join(TOOLS, "views.py"), "all"])
        code, out = self.sh([os.path.join(TOOLS, "storyboard_md.py"), "--episode", str(self.ep)])
        self.step("分镜脚本", "过" if code == 0 else "未过", out.strip().splitlines()[-1] if out.strip() else "")
        self.deliver(segs)
        self.step("完成", "过", f"第 {self.ep} 集：资产图、分镜脚本、投递包齐")
        self.finish(0)

    def assets(self):
        bible_path = C.abspath("剧本/资产圣经.json")
        code, out = self.sh([os.path.join(TOOLS, "assets_jobs.py"), "--episodes", str(self.ep), "--out", "素材库/jobs.json"])
        self.step("出图任务", "过" if code == 0 else "未过", out.strip().splitlines()[-1] if out.strip() else "")
        jobs = C.read_json(C.abspath("素材库/jobs.json")) if os.path.exists(C.abspath("素材库/jobs.json")) else []
        if jobs and not self.skip_images:
            if not (C.ROOT / ".env").exists() or "IMAGE_API_KEY" not in (C.ROOT / ".env").read_text(encoding="utf-8"):
                self.step("出图", "跳过", f"{len(jobs)} 张待出：项目 .env 里没有 IMAGE_API_KEY（写好后重跑 run.py 会续）")
            else:
                code, out = self.sh([os.path.join(TOOLS, "adapters", "image_openai_compat.py"), "--jobs", "素材库/jobs.json", "--parallel", str(self.parallel)])
                ok = out.count("✓")
                bad = [l for l in out.splitlines() if "✗" in l]
                self.step("出图", "过" if not bad else "部分", f"成功 {ok}，失败 {len(bad)}" + ("；" + "；".join(b[-80:] for b in bad[:3]) if bad else ""))
        elif jobs:
            self.step("出图", "跳过", f"--skip-images，{len(jobs)} 张未出")
        # 裁三视图 + 回填 checked
        bible = C.read_json(bible_path)
        cropped = 0
        for ch in bible.get("characters", []):
            for v in ch.get("variants", []):
                sheet = v.get("sheet")
                if sheet and os.path.exists(C.abspath(sheet)):
                    crops = v.get("crops") or []
                    if crops and not all(os.path.exists(C.abspath(c)) for c in crops):
                        self.sh([os.path.join(TOOLS, "crop_sheet.py"), sheet])
                        cropped += 1
                    if crops and all(os.path.exists(C.abspath(c)) for c in crops):
                        v["checked"] = True
        for lo in bible.get("locations", []):
            files = [lo.get(k) for k in ("plate", "side_a", "side_b") if lo.get(k)]
            if files and all(os.path.exists(C.abspath(f)) for f in files):
                lo["checked"] = True
                lo.setdefault("no_people_checked", True)
        for pr in bible.get("props", []):
            if pr.get("image") and os.path.exists(C.abspath(pr["image"])):
                pr["checked"] = True
        C.write_json(bible_path, bible)
        code, errs, warns = self.check("bible", "--stage", "assets")
        voice_errs = [e for e in errs if "音色" in e]
        other = [e for e in errs if "音色" not in e]
        self.step("资产门禁", "过" if not other else "有缺", f"裁图 {cropped} 张；缺音色 {len(voice_errs)} 项（没有 TTS 接口时只影响台词四元的音色文字）" + ("；其余：" + "；".join(o[:80] for o in other[:4]) if other else ""))

    def deliver(self, segs):
        ep = f"E{self.ep:02d}"
        out_dir = C.abspath(f"生成/投递/{ep}")
        os.makedirs(out_dir, exist_ok=True)
        manifest = []
        for s in segs:
            pj = C.read_json(C.abspath(f"生成/{s['id']}_prompt.json"))
            files = [dict(f, exists=os.path.exists(C.abspath(f["file"]))) for f in pj["files"]]
            manifest.append({"segment": s["id"], "scene": s["scene"], "duration": pj["duration"], "chars": pj["chars"], "prompt": pj["prompt"],
                             "files": files, "missing_files": [f["file"] for f in files if not f["exists"]], "paste_file": f"生成/{s['id']}_粘贴版.txt",
                             "warnings": pj["warnings"], "issues": pj["issues"], "bridge": (s.get("continuity_bridge") or {}).get("prev_segment")})
        C.write_json(os.path.join(out_dir, "manifest.json"), {"episode": self.ep, "model": C.project_model(), "mode": self.submit, "segments": manifest})
        if self.submit == "batch":
            with open(os.path.join(out_dir, "全部粘贴版.txt"), "w", encoding="utf-8") as f:
                for m in manifest:
                    f.write(open(C.abspath(m["paste_file"]), encoding="utf-8").read().rstrip() + "\n\n" + "=" * 60 + "\n\n")
            miss = sum(len(m["missing_files"]) for m in manifest)
            self.step("投递包", "过" if not miss else "缺图", f"批量：生成/投递/{ep}/manifest.json + 全部粘贴版.txt（{len(manifest)} 段；柚子批量提交读 manifest）" + (f"；参考图缺 {miss} 处，manifest.missing_files 列着" if miss else ""))
        else:
            lines = [f"# 第 {self.ep} 集 逐个提交清单", "", "按顺序提交；同场带桥的段要等前段选中候选、补了末帧再提交。", "", "| 顺序 | 段 | 秒 | 字符 | 上传 | 桥 | 状态 |", "|---|---|---|---|---|---|---|"]
            for i, m in enumerate(manifest, 1):
                ups = "；".join(f"{f['tag']}←{os.path.basename(f['file'])}" + ("" if f["exists"] else "（缺）") for f in m["files"]) or "无参考（人物由声明兜底）"
                lines.append(f"| {i} | {m['segment']} | {m['duration']} | {m['chars']} | {ups} | {'← ' + m['bridge'] if m['bridge'] else ''} | {'缺图 ' + str(len(m['missing_files'])) + ' 张' if m['missing_files'] else '待提交'} |")
            lines += ["", "每段的粘贴正文在 `生成/<段>_粘贴版.txt`；出好的片放回 `生成/<段>_v1.mp4` 后跑 `review_seg.py <段>` 量证据。"]
            with open(os.path.join(out_dir, "逐个提交.md"), "w", encoding="utf-8") as f:
                f.write("\n".join(lines) + "\n")
            self.step("投递包", "过", f"逐个：生成/投递/{ep}/逐个提交.md（{len(manifest)} 段）")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--episode", type=int, default=1)
    ap.add_argument("--submit", choices=["batch", "manual"], default="batch")
    ap.add_argument("--skip-images", action="store_true")
    ap.add_argument("--parallel", type=int, default=2)
    ap.add_argument("--target", default="15-25")
    ap.add_argument("--project")
    a = ap.parse_args()
    if a.project:
        import pathlib
        C.ROOT = pathlib.Path(a.project).resolve()
    Run(a.episode, a.submit, a.skip_images, a.parallel, a.target).run()


if __name__ == "__main__":
    main()
