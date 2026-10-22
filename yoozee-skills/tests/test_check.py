#!/usr/bin/env python3
"""验证器测试：样例项目全部产物 0 error；坏样例命中标注的规则 id。
  python3 yoozee-skills/tests/test_check.py"""
import json
import os
import pathlib
import subprocess
import sys

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
TOOLS = ROOT / "bindings" / "local" / "tools"
SAMPLE = HERE / "sample"
ENV = {**os.environ, "YOOZEE_ROOT": str(SAMPLE), "PYTHONIOENCODING": "utf-8"}


def run(args):
    r = subprocess.run([sys.executable, str(TOOLS / "check.py"), *args, "--json"], capture_output=True, text=True, env=ENV, encoding="utf-8")
    out = r.stdout.strip()
    try:
        data = json.loads(out[out.index("["):])
    except Exception:
        print(r.stdout, r.stderr)
        raise
    return r.returncode, data


def main():
    failed = 0
    for kind, extra in (("brief", []), ("script", []), ("bible", ["--stage", "shots"]), ("shots", []), ("segments", [])):
        code, data = run([kind, *extra])
        errs = sum(d["errors"] for d in data)
        if code != 0 or errs:
            failed += 1
            print(f"✗ sample {kind}: {errs} error(s)")
            for d in data:
                for i in d["items"]:
                    if i["level"] == "error":
                        print("   ", i["rule"], i["message"])
        else:
            print(f"✓ sample {kind} 无 error")
    # 坏样例：每个文件声明 expect 的规则 id 必须命中
    for p in sorted((HERE / "fixtures").glob("check-bad-*.json")):
        spec = json.loads(p.read_text(encoding="utf-8"))
        code, data = run([spec["kind"], str(p.resolve().relative_to(SAMPLE)) if False else str(p), *spec.get("args", [])])
        hit = {i["rule"] for d in data for i in d["items"]}
        missing = [r for r in spec["expect"] if r not in hit]
        if missing:
            failed += 1
            print(f"✗ {p.name}: 没命中 {missing}（命中 {sorted(hit)}）")
        else:
            print(f"✓ {p.name} 命中 {spec['expect']}")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
