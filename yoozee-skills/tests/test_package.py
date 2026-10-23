#!/usr/bin/env python3
"""流程技能包静态校验：tests/packages/cases.json 里每个包给出的规则编号，与后端 drama.CheckStepPackage 一致
（backend/internal/drama/step_package_test.go 跑同一份样例）；packages/ 下的包都能过校验，构建出的 zip 带清单和内容哈希。
  python3 yoozee-skills/tests/test_package.py"""
import json
import pathlib
import sys
import tempfile
import zipfile

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(ROOT))
import build as B  # noqa: E402


def expand(value):
    return value if isinstance(value, str) else value["repeat"] * value["times"]


def main():
    rules = B.package_rules()
    failed = 0
    cases = json.loads((HERE / "packages" / "cases.json").read_text(encoding="utf-8"))["cases"]
    for c in cases:
        files = {p: expand(v).encode("utf-8") for p, v in c["files"].items()}
        manifest = json.dumps(c["manifest"], ensure_ascii=False).encode("utf-8")
        _, problems = B.package_problems(manifest, files, rules)
        got = sorted(p["rule"] for p in problems)
        if got != sorted(c["expect"]):
            failed += 1
            print(f"✗ {c['name']}：规则 {got}，期望 {sorted(c['expect'])}：{problems}")
        else:
            print(f"✓ {c['name']}")
    problems = B.check_flow_packages()
    if problems:
        failed += 1
        print("✗ packages/ 校验没过：\n" + "\n".join(problems))
    with tempfile.TemporaryDirectory() as tmp:
        B.DIST = pathlib.Path(tmp)
        for d in B.flow_packages():
            out = B.build_package(d.name)
            with zipfile.ZipFile(out) as z:
                names = set(z.namelist())
                digest = json.loads(z.read(rules["hashFile"]))
                payload = {n: z.read(n) for n in names if n != rules["hashFile"]}
            want = set(json.loads((d / "package.json").read_text(encoding="utf-8"))["files"]) | {rules["manifestFile"], rules["hashFile"]}
            if names != want or digest["contentHash"] != B.content_hash(payload):
                failed += 1
                print(f"✗ {d.name} 的 zip 内容或哈希不对：{sorted(names)}")
    if failed:
        sys.exit(1)
    print(f"✓ {len(cases)} 个样例与后端规则一致")


if __name__ == "__main__":
    main()
