#!/usr/bin/env python3
"""2.1 情绪强化与即梦格式的夹具：情绪弧展开的缩放规则 + 样例项目即梦格式分镜脚本黄金对照。
  python3 yoozee-skills/tests/test_arc.py            对照
  python3 yoozee-skills/tests/test_arc.py --update   重新生成分镜脚本期望（改了渲染规则时用，改完要人看 diff）"""
import os
import pathlib
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT / "bindings" / "local" / "tools"))
import common as C  # noqa: E402
import pack as PK  # noqa: E402

PROF = C.profile("live-realism")
ARCS = C.vocab_by_id("kb-performance", "vocab/emotion-arcs.jsonl")


def shot(arc, est=None, lines=False, **kw):
    s = {"id": "T-01", "emotion_arc": arc}
    if lines:
        s["lines"] = [{"line_id": "X"}]
    s.update(kw)
    return s


def main():
    update = "--update" in sys.argv
    fails = []
    assert len(ARCS) == 32 and len(C.vocab("kb-performance", "vocab/expressions.jsonl")) == 101, "词库数目不对"
    # 1 等比缩放：6 秒弧进 3.5 秒镜，末段结束在 3.5
    txt, used, fam = PK.expand_arc(shot({"id": "arc-sadness-001"}), 3.5, PROF)
    if not (txt.endswith("眼神空洞") or "3.5秒" in txt) or used != [1, 2, 3, 4] or fam != "sadness":
        fails.append(f"缩放：{txt} {used} {fam}")
    # 2 est<3 只取前两段
    txt, used, _ = PK.expand_arc(shot({"id": "arc-sadness-001"}), 2.5, PROF)
    if used != [1, 2] or "2.5秒" not in txt:
        fails.append(f"短镜前两段：{txt} {used}")
    # 3 phase_from/to 截取
    txt, used, _ = PK.expand_arc(shot({"id": "arc-cold-001", "phase_from": 3, "phase_to": 5}), 6, PROF)
    if used != [3, 4, 5]:
        fails.append(f"截段：{used}")
    # 4 continues 从上一镜停下的段接着
    txt, used, _ = PK.expand_arc(shot({"id": "arc-shock-002", "continues": "P"}), 3, PROF, prev_phase_end=2)
    if used != [3, 4]:
        fails.append(f"续接：{used}")
    # 5 hidden7 删大幅度信号
    txt, _, _ = PK.expand_arc(shot({"id": "arc-cold-001", "intensity": "hidden7"}), 8, PROF)
    if "掩面" in txt or "藏七露三" not in txt:
        fails.append(f"hidden7：{txt}")
    # 6 台词落段标注
    txt, _, _ = PK.expand_arc(shot({"id": "arc-sadness-001"}, lines=True), 6, PROF)
    if "台词落在这一段" not in txt:
        fails.append(f"台词落段：{txt}")
    # 7 短段并入前一段（0.5 秒弧段进 2 秒镜会被合并）
    txt, used, _ = PK.expand_arc(shot({"id": "arc-love-001"}), 2.0, PROF)
    if txt.count("秒：") > 2:
        fails.append(f"短段合并：{txt}")
    # 8 动作模式与禁止项
    pat, forbid = PK.action_pattern_text({"action_pattern": {"id": "act-turn", "variant": "被叫住后回头：肩膀先停，再缓缓侧脸"}})
    if "动作模式·转身回头" not in pat or "头发穿模" not in forbid:
        fails.append(f"动作模式：{pat} {forbid}")
    for f in fails:
        print("✗", f)
    print("✓ 情绪弧展开 8 项" if not fails else f"✗ 情绪弧展开 {len(fails)} 项失败")
    # 9 样例项目即梦格式分镜脚本黄金
    env = dict(os.environ, YOOZEE_ROOT=str(HERE / "sample"), PYTHONIOENCODING="utf-8")
    out = HERE / "sample" / "剧本" / "分镜脚本_E01.md"
    subprocess.run([sys.executable, str(ROOT / "bindings/local/tools/storyboard_md.py"), "--out", "剧本/分镜脚本_E01.md"], env=env, check=True, capture_output=True)
    got = out.read_text(encoding="utf-8")
    exp = HERE / "expected" / "storyboard-sample-E01.md"
    if update:
        exp.write_text(got, encoding="utf-8")
        print("已更新 storyboard-sample-E01.md")
    elif not exp.exists() or exp.read_text(encoding="utf-8") != got:
        fails.append("即梦格式分镜脚本与期望不同")
        print("✗ 即梦格式分镜脚本与期望不同")
    else:
        print("✓ 即梦格式分镜脚本黄金一致")
    for must in ("表情：0–", "音色：", "灯光与光影：主光：", "衔接：上一镜→本镜", "自检摘要（check.py 真算）", "动作模式·"):
        if must not in got:
            fails.append(f"分镜脚本缺「{must}」")
            print(f"✗ 分镜脚本缺「{must}」")
    sys.exit(1 if fails else 0)


if __name__ == "__main__":
    main()
