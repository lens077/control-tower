#!/usr/bin/env python3
"""把 deploy/chart/control-tower 渲染成 deploy/<env>/ 下的原始清单。

    scripts/deploy-render.py            # 渲染 dev 与 pre
    scripts/deploy-render.py --check    # 渲染到临时目录并与已提交的 deploy/<env>/ 比对，不一致非零退出

deploy/{dev,pre}/ 是生成物：守门测试（deploy/*_test.go）读它，kubectl apply 的手工兜底也用它，
但改动一律发生在 chart 与 values-<env>.yaml，然后重新渲染。CI 的 check-deploy 保证两边一致。

对 helm template 输出做两处整理，其余原样：
  1. 去掉每个文档头上的「# Source: …」行；
  2. helm 把模板文件开头、第一个 --- 之前的注释块当成独立文档输出，这里把它并回紧随其后的文档。
helm 会按 kind 顺序重排同一文件里的多个文档（PodDisruptionBudget 在 Deployment 前），
不在这里纠正：读这些文件的代码必须按 kind 找对象，而不是假定第一个文档是什么。
"""
from __future__ import annotations

import argparse
import filecmp
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHART = ROOT / "deploy" / "chart" / "control-tower"
ENVS = ("dev", "pre")
HEADER = (
    "# 由 deploy/chart/control-tower 渲染生成（make deploy-render），不要手改：\n"
    "# 改模板或 values-{env}.yaml 后重新渲染；CI 的 check-deploy 会比对。\n"
)


def helm_render(env: str, out: Path) -> Path:
    cmd = [
        "helm", "template", "control-tower", str(CHART),
        "-f", str(CHART / "values.yaml"),
        "-f", str(CHART / f"values-{env}.yaml"),
        "--output-dir", str(out),
    ]
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL)
    return out / "control-tower" / "templates"


def is_comment_only(doc: str) -> bool:
    return all(line.strip() == "" or line.lstrip().startswith("#") for line in doc.splitlines())


def tidy(text: str, env: str) -> str:
    docs = []
    for raw in text.split("\n---\n" if not text.startswith("---\n") else "---\n"):
        lines = [l for l in raw.splitlines() if not l.startswith("# Source: ")]
        doc = "\n".join(lines).strip("\n")
        if doc:
            docs.append(doc)
    merged: list[str] = []
    pending = ""
    for doc in docs:
        if is_comment_only(doc):
            pending += doc + "\n"
            continue
        merged.append(pending + doc)
        pending = ""
    if pending:
        merged.append(pending.rstrip("\n"))
    return HEADER.format(env=env) + "\n---\n".join(merged) + "\n"


def render(env: str, dest: Path) -> None:
    with tempfile.TemporaryDirectory() as tmp:
        src = helm_render(env, Path(tmp))
        for component in ("config", "gateway"):
            target = dest / component
            if target.exists():
                shutil.rmtree(target)
            target.mkdir(parents=True)
            for path in sorted((src / component).iterdir()):
                (target / path.name).write_text(tidy(path.read_text(), env))


def check() -> int:
    bad = 0
    with tempfile.TemporaryDirectory() as tmp:
        for env in ENVS:
            got = Path(tmp) / env
            render(env, got)
            want = ROOT / "deploy" / env
            for component in ("config", "gateway"):
                cmp = filecmp.dircmp(want / component, got / component)
                for name in cmp.left_only:
                    print(f"deploy/{env}/{component}/{name}: 已提交但 chart 不再渲染", file=sys.stderr)
                    bad += 1
                for name in cmp.right_only:
                    print(f"deploy/{env}/{component}/{name}: chart 会渲染但未提交", file=sys.stderr)
                    bad += 1
                for name in cmp.diff_files:
                    print(f"deploy/{env}/{component}/{name}: 与 chart 渲染结果不一致", file=sys.stderr)
                    subprocess.run(["diff", "-u", str(want / component / name), str(got / component / name)])
                    bad += 1
    if bad:
        print(f"\n{bad} 处不一致：运行 make deploy-render 并提交结果", file=sys.stderr)
        return 1
    print("deploy/{dev,pre} 与 chart 渲染结果一致")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--check", action="store_true", help="只比对，不写入")
    args = parser.parse_args()
    if shutil.which("helm") is None:
        print("缺少 helm", file=sys.stderr)
        return 2
    if args.check:
        return check()
    for env in ENVS:
        render(env, ROOT / "deploy" / env)
        print(f"已渲染 deploy/{env}/")
    return 0


if __name__ == "__main__":
    os.chdir(ROOT)
    sys.exit(main())
