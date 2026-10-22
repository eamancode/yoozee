#!/usr/bin/env python3
"""2.0 编译器新能力的黄金对照（与 test_compile.py 分开：那组样例由柚子后端 Go 共用，不动）。
样例：seg25-scene（2.5 整场一段，live-realism 档案）、seg25-bridge（同场后段带一致性桥与 risk_tags）。
  python3 yoozee-skills/tests/test_compile25.py            对照
  python3 yoozee-skills/tests/test_compile25.py --update   重新生成期望结果（改了编译规则时用，改完要人看 diff）"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT / "bindings" / "local" / "tools"))
import compile as P  # noqa: E402

MODELS = {m["id"]: m for m in json.loads((ROOT / "kb" / "kb-dialects" / "models.json").read_text(encoding="utf-8"))["models"]}
CASES = [("seg25-scene", "seedance-2.5", "live-realism"), ("seg25-bridge", "seedance-2.5", "live-realism"), ("seg25-bridge", "minimax-h3", "live-realism"), ("seg25-bridge", "seedance-2.5", "duanju-shiga")]


def profile(pid):
    data = json.loads((ROOT / "profiles" / f"{pid}.json").read_text(encoding="utf-8"))
    if data.get("extends"):
        base = profile(data["extends"])
        base.update(data)
        return base
    return data


def render(result):
    issues = "\n".join(f"{i['level']}: {i['message']}" for i in result["issues"])
    files = "\n".join(f"{f['tag']}\t{f['file']}\t{f['role']}" for f in result["files"])
    return f"model: {result['model']}\nduration: {result['duration']}\n--- prompt\n{result['prompt']}\n--- paste\n{result['paste']}\n--- files\n{files}\n--- issues\n{issues}\n"


def main():
    update = "--update" in sys.argv
    failed = 0
    for fixture, model, pid in CASES:
        seg = json.loads((HERE / "fixtures" / f"{fixture}.json").read_text(encoding="utf-8"))
        res = P.compile_segment(seg, MODELS[model], profile=profile(pid))
        got = render(res)
        path = HERE / "expected" / f"{fixture}.{model}.{pid}.txt"
        if update:
            path.write_bytes(got.encode("utf-8"))
            continue
        if not path.exists() or got != path.read_text(encoding="utf-8"):
            failed += 1
            print(f"✗ {fixture} × {model} × {pid} 和期望不同")
        else:
            print(f"✓ {fixture} × {model} × {pid}")
        if fixture == "seg25-bridge":
            assert "承接上一段" in res["prompt"], "桥段应有再锚定"
            assert any("⚠️" in w for w in res["warnings"]), "risk_tags 应生成 ⚠️"
            assert not any(i["level"] == "error" for i in res["issues"]), res["issues"]
    if update:
        print(f"已更新 {len(CASES)} 个期望结果")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
