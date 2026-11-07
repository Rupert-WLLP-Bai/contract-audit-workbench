<!-- File metadata (manifest mirror): -->
<!-- Purpose: 说明“文档站预览”的操作或技术边界：此包只负责把仓库根目录的 `docs/` 手册渲染为本地阅读站点；请直接编辑 `docs/`，不要在 `docs-site/` 复制或维护第二份手册。 -->
<!-- Author: Bai Junhao <baijunhao@cq.chinamobile.com> -->
<!-- Contact: +86 13617696321 -->
<!-- Created: 2026-11-01 -->
<!-- Updated: 2026-11-07 -->
<!-- Revision: r2 -->
<!-- Copyright: See LICENSE -->
<!-- End file metadata. -->

# 文档站预览

此包只负责把仓库根目录的 `docs/` 手册渲染为阅读站点；请直接编辑 `docs/`，不要在 `docs-site/` 复制或维护第二份手册。

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

当前只提供本地/CI 构建预览，不配置或发布 GitHub Pages。站点未启用 blog、文档版本归档或外部搜索。导航和 Mermaid 渲染由本站配置生成。

## 页面与路由

- 根首页由 `src/pages/index.js` 生成，只提供导航入口，不复制手册正文；手册正文仍以 `docs/` 为唯一来源，挂在 `/handbook/` 下。
- 指向应用内 `/api-docs` 的连接使用普通 `<a>`，在任何部署 origin 上都按根路径解析，不参与文档站路由与断链检查。

## 随应用一起拉起

`deploy/app/Dockerfile` 的 `docs-build` 阶段以 `CAW_DOCS_BASE_URL=/docs/` 构建静态站点，并把产物复制到 client 镜像的 `/usr/share/nginx/html/docs/`；`deploy/app/nginx/client.conf` 用 `location ^~ /docs/` 提供服务。`make apps-up` 因此同时拉起工作台与手册，不新增端口或容器。

`CAW_DOCS_BASE_URL` 默认为 `/`（本地预览）；改变它会同时改变资源路径与站内链接。`CAW_DOCS_SITE_URL` 只影响 canonical URL。

离线镜像打包（`make app-images-offline`）走 `web/dist/*` 预构建产物，不执行本目录构建；该路径的 `/docs/` 接入尚未执行，记为 `NOT_RUN`。
