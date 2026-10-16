#!/usr/bin/env python3
"""方言编译的对照测试：每个样例段 × 每个模型的编译结果必须和 tests/expected 下的文件逐字相同。
柚子后端的 Go 实现（backend/internal/drama）读同一组样例和期望结果，保证两边编译一致。
  python3 yoozee-skills/tests/test_compile.py            对照
  python3 yoozee-skills/tests/test_compile.py --update   重新生成期望结果（改了编译规则时用，改完要人看一遍 diff）"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT / "bindings" / "local" / "tools"))
import compile as P  # noqa: E402

MODELS = {m["id"]: m for m in json.loads((ROOT / "kb" / "kb-dialects" / "models.json").read_text(encoding="utf-8"))["models"]}
CASES = [("segment-dialogue", "seedance-2.0"), ("segment-dialogue", "seedance-2.5"), ("segment-dialogue", "minimax-h3"),
         ("segment-long", "seedance-2.5"), ("segment-long", "seedance-2.0"), ("segment-long", "minimax-h3"),
         ("segment-firstframe", "seedance-2.0"), ("segment-firstframe", "minimax-h3")]


def render(result):
    issues = "\n".join(f"{i['level']}: {i['message']}" for i in result["issues"])
    files = "\n".join(f"{f['tag']}\t{f['file']}\t{f['role']}" for f in result["files"])
    return f"model: {result['model']}\nduration: {result['duration']}\n--- prompt\n{result['prompt']}\n--- files\n{files}\n--- issues\n{issues}\n"


def main():
    update = "--update" in sys.argv
    failed = 0
    for fixture, model in CASES:
        seg = json.loads((HERE / "fixtures" / f"{fixture}.json").read_text(encoding="utf-8"))
        got = render(P.compile_segment(seg, MODELS[model]))
        path = HERE / "expected" / f"{fixture}.{model}.txt"
        if update:
            path.write_text(got, encoding="utf-8")
            continue
        want = path.read_text(encoding="utf-8")
        if got != want:
            failed += 1
            print(f"✗ {fixture} × {model} 和期望不同")
        else:
            print(f"✓ {fixture} × {model}")
    if update:
        print(f"已更新 {len(CASES)} 个期望结果")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
