// File metadata (manifest mirror):
// Purpose: Renders the read-only Qichacha MCP integration catalog so demo screenshots can show recognized servers and skills without executing external queries or implying business authorization.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-08
// Updated: 2026-11-08
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Tag } from "antd";
import { qccMcpServers } from "../domain/qcc-mcp-catalog";
import "./qcc-mcp-panel.css";

const serverLabels: Record<string, string> = {
  company: "企业数据", risk: "风险大脑", ipr: "知识产权", operation: "经营罗盘",
  history: "历史存档", executive: "董监高画像", regulation: "法律法规",
  case: "司法案例", tender: "招标标讯", document: "智能文档",
};
const serverLabel = (server: string) => serverLabels[server] ?? server;
const recognized = qccMcpServers.filter((server) => server.status === "RECOGNIZED");
const skillTotal = recognized.reduce((count, server) => count + server.skillCount, 0);

export function QccMcpPanel() {
  return (
    <section className="qcc-mcp-panel" aria-label="企查查 MCP 集成目录">
      <header className="qcc-mcp-head">
        <div>
          <p className="qcc-mcp-eyebrow">外部智能体工具 · Eino ToolsNode</p>
          <h2>企查查 MCP · 智能体数据平台</h2>
          <p>
            已识别 {recognized.length} 个 MCP Server、{skillTotal} 个 Skill。以下为 2026-11-08
            已保存的 <code>tools/list</code> 与 Skill 调用演示结果。默认不开启实时连接，也不执行查询。
          </p>
        </div>
        <div className="qcc-mcp-tags">
          <Tag color="blue">演示数据</Tag>
          <Tag>实时连接：关闭（默认）</Tag>
        </div>
      </header>
      <dl className="qcc-mcp-summary" aria-label="企查查 MCP 识别摘要">
        <div><dt>MCP Server</dt><dd>{qccMcpServers.length}</dd></div>
        <div><dt>已识别 Server</dt><dd>{recognized.length}</dd></div>
        <div><dt>已识别 Skill</dt><dd>{skillTotal}</dd></div>
        <div><dt>未授权 Server</dt><dd>{qccMcpServers.filter((server) => server.status !== "RECOGNIZED").length}</dd></div>
        <div><dt>实时连接</dt><dd className="qcc-mcp-switch">关闭</dd></div>
      </dl>
      <div className="qcc-mcp-grid">
        {qccMcpServers.map((server) => (
          <article key={server.server} className="qcc-mcp-server" aria-label={`${serverLabel(server.server)} MCP Server`}>
            <header>
              <div>
                <h3>{serverLabel(server.server)}</h3>
                <code>qcc-{server.server}</code>
              </div>
              <Tag color={server.status === "RECOGNIZED" ? "green" : "orange"}>
                {server.status === "RECOGNIZED" ? `${server.skillCount} Skills` : "未授权"}
              </Tag>
            </header>
            {server.status === "RECOGNIZED" ? (
              <>
                <div className="qcc-mcp-skills" aria-label={`${serverLabel(server.server)} Skill 列表`}>
                  {server.skills.slice(0, 6).map((skill) => <span key={skill}>{skill}</span>)}
                  {server.skills.length > 6 ? <span>+{server.skills.length - 6}</span> : null}
                </div>
                {server.skills.length > 6 ? (
                  <details>
                    <summary>查看全部 {server.skills.length} 个 Skill</summary>
                    <p>{server.skills.join(" · ")}</p>
                  </details>
                ) : null}
              </>
            ) : (
              <p className="qcc-mcp-error">服务端未授权此服务；未展示或推断其 Skill。</p>
            )}
          </article>
        ))}
      </div>
      <details className="qcc-mcp-sample">
        <summary>查看已保存的工商信息示例（Eino 已执行 get_company_registration_info）</summary>
        <dl>
          <div><dt>企业名称</dt><dd>企查查科技股份有限公司</dd></div>
          <div><dt>统一社会信用代码</dt><dd>91320594088140947F</dd></div>
          <div><dt>法定代表人</dt><dd>陈德强</dd></div>
          <div><dt>登记状态</dt><dd>在业</dd></div>
        </dl>
        <p>完整原始返回和股东、风险扫描、法规检索示例见交付包 <code>ui2/qcc-mcp/</code>。</p>
      </details>
      <p className="qcc-mcp-note">
        Eino 侧通过 <code>ToolsNode</code> 调用 MCP Skill。实时访问由服务端开关
        <code>QCC_MCP_ENABLED</code> 控制，默认关闭；Bearer Token 只从环境变量读取。
        已留存示例：企业工商信息、股东信息、企业风险扫描、法规检索。
      </p>
    </section>
  );
}
