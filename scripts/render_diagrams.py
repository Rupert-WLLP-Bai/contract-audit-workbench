#!/usr/bin/env python3
"""Render checked-in UML locally; never send architecture source to a web service."""
import argparse
import datetime as dt
import hashlib
import html
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
BASE = ROOT / "docs/diagrams"


def render(jar, fonts):
    sources = sorted((BASE / "src").glob("*.puml"))
    if not sources:
        raise ValueError("No PlantUML source found")
    java = [os.environ.get("JAVA", "java"), "-Djava.awt.headless=true"]
    if fonts:
        java.append(f"-Dsun.java2d.fontpath={fonts}")
    java += ["-jar", str(jar), "-charset", "UTF-8", "-failfast2", "-Playout=smetana"]
    env = dict(os.environ, PLANTUML_LIMIT_SIZE="8192")
    for fmt in ("svg", "png"):
        target = BASE / fmt
        target.mkdir(parents=True, exist_ok=True)
        # Clear corresponding outputs: a render failure must not reuse old images.
        for source in sources:
            (target / f"{source.stem}.{fmt}").unlink(missing_ok=True)
        subprocess.run(java + [f"-t{fmt}", "-o", str(target)] + [str(p) for p in sources],
                       cwd=ROOT, env=env, check=True, timeout=240)
    manifest = []
    for source in sources:
        svg, png = (BASE / "svg" / f"{source.stem}.svg", BASE / "png" / f"{source.stem}.png")
        data = svg.read_text(encoding="utf-8")
        if "Syntax Error" in data or "An error has occurred" in data:
            raise ValueError(f"Renderer error diagram: {source.name}")
        root = ET.fromstring(data)
        if not root.tag.endswith("svg") or png.read_bytes()[:8] != b"\x89PNG\r\n\x1a\n":
            raise ValueError(f"Invalid diagram output: {source.name}")
        body = source.read_text(encoding="utf-8")
        match = re.search(r"(?m)^title\s+(.+)$", body)
        title = match.group(1).replace("\\n", " · ") if match else source.stem
        manifest.append({"name": source.stem, "title": title,
                         "source_sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
                         "svg_sha256": hashlib.sha256(svg.read_bytes()).hexdigest(),
                         "png_sha256": hashlib.sha256(png.read_bytes()).hexdigest(),
                         "width": root.attrib.get("width"), "height": root.attrib.get("height")})
    report = {"generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
              "renderer": "PlantUML (local Java / Smetana)",
              "renderer_sha256": hashlib.sha256(jar.read_bytes()).hexdigest(),
              "font": "Noto Sans CJK SC (PNG rendered locally)",
              "source_count": len(sources), "diagrams": manifest}
    (BASE / "render-manifest.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    gallery(manifest)
    print(f"PASS: {len(sources)} UML sources rendered to SVG and PNG; offline gallery created")


def gallery(items):
    nav = "\n".join(f'<a href="#{x["name"]}">{html.escape(x["title"])}</a>' for x in items)
    figures = []
    for x in items:
        name, title = x["name"], html.escape(x["title"])
        figures.append(f'''<section id="{name}" class="diagram"><header><h2>{title}</h2>
<div class="links"><a href="svg/{name}.svg" target="_blank" rel="noopener">打开矢量图</a>
<a href="png/{name}.png" target="_blank" rel="noopener">打开高清 PNG</a>
<a href="src/{name}.puml" download>下载可编辑图源</a></div></header>
<a href="png/{name}.png" target="_blank" rel="noopener"><img loading="lazy" src="png/{name}.png" alt="{title}"></a></section>''')
    page = '''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>运营商合同与业务承诺审查 · UML 图册</title>
<style>
:root{color-scheme:light;font-family:system-ui,"Noto Sans CJK SC","Microsoft YaHei",sans-serif;color:#173044;background:#edf2f6}
*{box-sizing:border-box}body{margin:0}a{color:#136779;text-decoration:none}a:hover{text-decoration:underline}
.hero{background:#12384a;color:white;padding:42px max(28px,6vw)}.hero small{color:#a8d6dd;letter-spacing:.12em}.hero h1{font-size:32px;line-height:1.3;margin:12px 0}.hero p{max-width:960px;line-height:1.8;margin-bottom:0;color:#d1e2e8}
.layout{display:grid;grid-template-columns:270px minmax(0,1fr);gap:28px;padding:28px;max-width:1740px;margin:auto}
nav{position:sticky;top:20px;align-self:start;max-height:94vh;overflow:auto;background:white;border-radius:12px;padding:20px}nav a{display:block;font-size:13px;line-height:1.5;padding:9px 0;border-bottom:1px solid #edf2f6}nav h2{font-size:16px;margin-top:0}
.notice{background:#e5f0f2;border-left:4px solid #278494;padding:18px 22px;line-height:1.8;margin-bottom:24px;border-radius:5px}
.diagram{background:white;border:1px solid #d7e1e8;border-radius:12px;margin-bottom:28px;overflow:hidden;scroll-margin-top:20px}.diagram header{padding:22px 26px;border-bottom:1px solid #edf2f6}.diagram h2{font-size:20px;line-height:1.5;margin:0 0 12px}.links{display:flex;gap:20px;font-size:13px;flex-wrap:wrap}.diagram img{display:block;max-width:100%;height:auto;margin:24px auto;padding:0 22px;max-height:1250px;object-fit:contain}footer{padding:20px 30px 45px;color:#536c79;font-size:13px}
@media(max-width:1000px){.layout{grid-template-columns:1fr;padding:16px}nav{position:static;max-height:310px}.hero{padding:28px}.hero h1{font-size:25px}.diagram img{padding:8px}.diagram header{padding:18px}}
@media print{nav,.links{display:none}.layout{display:block;padding:0}.hero{background:white;color:#173044}.hero p{color:#173044}.diagram{break-before:page;border:0}.diagram img{max-height:85vh}.notice{background:white}}
</style></head><body>
<div class="hero"><small>CONTRACT AUDIT / OPERATOR DESIGN / 0.3</small>
<h1>运营商合同与业务承诺审查 · UML 图册</h1>
<p>政企 · 市场 · 网络 · 客服。图册覆盖用例、领域结构、业务流程、时序、状态、组件、部署与包依赖。每张图明确现状或目标，不把规划能力当作本轮已实现功能。</p></div>
<div class="layout"><nav><h2>图册目录</h2>NAV</nav><main>
<div class="notice">这是离线设计文档，不连接业务 API。PNG 保留已验证的中文排版；点击图片查看原尺寸，矢量图可无限缩放。
完整字段、异常流和验收标准分别见 <a href="../product/USE-CASES.md">42 个用例</a>、<a href="../product/ACCEPTANCE-MATRIX.md">验收矩阵</a>、<a href="../architecture/ARCHITECTURE.md">架构与技术选型</a>。图源使用 PlantUML，可本地重新渲染。</div>
FIGURES</main></div><footer>来源、规则版本及本次测试证据请查看 README 与 docs/VALIDATION.md。目标图不构成生产认证或业务效果承诺。</footer></body></html>'''
    (BASE / "index.html").write_text(page.replace("NAV", nav).replace("FIGURES", "\n".join(figures)), encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plantuml", default=os.environ.get("PLANTUML_JAR", ""), help="Path to local PlantUML jar")
    parser.add_argument("--fonts", default=os.environ.get("UML_FONT_DIR", ""), help="Optional directory of installed Chinese fonts")
    args = parser.parse_args()
    jar = Path(args.plantuml).expanduser().resolve() if args.plantuml else None
    if jar is None or not jar.is_file():
        parser.error("Set PLANTUML_JAR or --plantuml to a local jar; no remote rendering is used")
    try:
        render(jar, args.fonts)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(f"FAIL: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
