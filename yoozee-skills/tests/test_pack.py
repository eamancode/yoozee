#!/usr/bin/env python3
"""装段测试：样例项目的镜头表在 Seedance 2.5 上装出的段与 tests/sample/剧本/分段.json 逐字相同（确定性）；
2.5 同场 ≤30 秒一段、超过拆段带桥；2.0 上限 15 秒时段数更多且每段在范围内。
  python3 yoozee-skills/tests/test_pack.py            对照
  python3 yoozee-skills/tests/test_pack.py --update   重新生成 tests/sample/剧本/分段.json（改了装段规则时用，改完要人看 diff）"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT / "bindings" / "local" / "tools"))
import pack as PK  # noqa: E402

SAMPLE = HERE / "sample"
MODELS = {m["id"]: m for m in json.loads((ROOT / "kb" / "kb-dialects" / "models.json").read_text(encoding="utf-8"))["models"]}


def load(rel):
    return json.loads((SAMPLE / rel).read_text(encoding="utf-8"))


def main():
    update = "--update" in sys.argv
    shots, script, bible = load("剧本/镜头表.json"), load("剧本/剧本.json"), load("剧本/资产圣经.json")
    segs, problems = PK.pack(shots, script, bible, MODELS["seedance-2.5"], "live-realism")
    failed = 0
    assert not problems, problems
    expected_path = SAMPLE / "剧本" / "分段.json"
    if update:
        expected_path.write_text(json.dumps(segs, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print("已更新 tests/sample/剧本/分段.json")
    else:
        want = json.loads(expected_path.read_text(encoding="utf-8"))
        if json.dumps(segs, ensure_ascii=False, sort_keys=True) != json.dumps(want, ensure_ascii=False, sort_keys=True):
            failed += 1
            print("✗ 2.5 装段结果与 tests/sample/剧本/分段.json 不同")
        else:
            print("✓ 2.5 装段与样例一致")
    scenes = {}
    for s in segs:
        scenes.setdefault(s["scene"], []).append(s)
    for scene, group in scenes.items():
        total = sum(s["dur"] for s in group)
        if total <= 30 and len(group) > 1:
            failed += 1
            print(f"✗ 场 {scene} 合计 {total} 秒却拆成 {len(group)} 段")
        for prev, cur in zip(group, group[1:]):
            br = cur.get("continuity_bridge")
            if not br or br["prev_segment"] != prev["id"] or br["shared_refs"] != [r["file"] for r in prev.get("refs", []) if r["role"] != "motion"]:
                failed += 1
                print(f"✗ {cur['id']} 的一致性桥不对")
    if len(scenes.get("E01S02", [])) != 2:
        failed += 1
        print("✗ 第二场应拆成两段（>30 秒）")
    for s in segs:
        if not 4 <= s["dur"] <= 30:
            failed += 1
            print(f"✗ {s['id']} 时长 {s['dur']} 不在 2.5 范围")
    print(f"✓ 2.5：{len(segs)} 段，同场一段 / 拆段带桥 / 时长范围 均通过" if not failed else "")
    segs20, problems20 = PK.pack(shots, script, bible, MODELS["seedance-2.0"], "live-realism")
    if problems20:
        failed += 1
        print("✗ 2.0 装段报无合法切点：", problems20)
    if len(segs20) <= len(segs):
        failed += 1
        print(f"✗ 2.0（15 秒上限）应比 2.5 装出更多段：{len(segs20)} vs {len(segs)}")
    for s in segs20:
        if not 4 <= s["dur"] <= 15:
            failed += 1
            print(f"✗ 2.0 的 {s['id']} 时长 {s['dur']} 不在范围")
    print(f"✓ 2.0：{len(segs20)} 段，每段 4～15 秒" if not failed else "")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
