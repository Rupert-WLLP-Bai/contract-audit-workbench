# 文档站预览

此包只负责把仓库根目录的 `docs/` 手册渲染为本地阅读站点；请直接编辑 `docs/`，不要在 `docs-site/` 复制或维护第二份手册。

## 本地预览

环境要求 Node.js 20 或更新版本，以及 Bun。依赖和锁文件只属于本目录：

```sh
cd docs-site
bun install --frozen-lockfile
bun run start
```

正式静态构建：

```sh
bun run build
```

当前只提供本地/CI 构建预览，不配置或发布 GitHub Pages。站点未启用 blog、文档版本归档或外部搜索。根首页来自 `docs/index.md`；导航和 Mermaid 渲染由本站配置生成。
