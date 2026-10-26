import { Tabs, Table } from "antd";
import { Flex } from "antd";
import { Select } from "antd";
import { Input, Button } from "antd";
import { useState, type FormEvent } from "react";
import { useParams } from "react-router";
import { errorMessage } from "../api/http";
import { paths } from "../api/paths";
import {
  useCreateKnowledge,
  useCreateWorkbenchTask,
  useSession,
  useUpdateWorkbenchTask,
  useWorkbench,
} from "../api/queries";
import type {
  KnowledgeInput,
  WorkbenchSnapshot,
  WorkbenchTask,
} from "../api/types";
import { PageStatus } from "../components/page-status";
import { StatusText } from "../components/status-text";
import { reportPagePath } from "../domain/report";
import { formatDateTime } from "../domain/review";
import { SettingsPage } from "./settings-page";
import "./workbench-module-page.css";

export const WORKBENCH_MODULES = {
  tasks: {
    title: "审查任务",
    description: "查看实际分派任务、责任人和截止时间。",
    tabs: ["我的待办", "我发起的", "已完成"],
    details: ["任务详情", "处理记录", "关联材料", "协作成员"],
  },
  remediation: {
    title: "整改中心",
    description: "跟踪真实整改事项、材料和复核状态。",
    tabs: ["待整改", "待复核", "已关闭"],
    details: ["整改详情", "材料与版本", "复核记录", "操作历史"],
  },
  reports: {
    title: "报告中心",
    description: "查看服务端冻结的报告版本与摘要。",
    tabs: ["审查报告", "版本对比", "导出记录"],
    details: ["报告预览", "发现与决定", "版本与来源", "导出记录"],
  },
  evidence: {
    title: "证据中心",
    description: "查看原件登记、解析状态与材料摘要。",
    tabs: ["证据材料", "引用片段", "完整性校验"],
    details: ["原文预览", "引用片段", "来源与身份", "使用记录"],
  },
  executions: {
    title: "执行轨迹",
    description: "查看实际运行和已报告的执行用量。",
    tabs: ["运行记录", "模型调用", "规则执行"],
    details: ["运行概览", "调用时间轴", "输入与输出", "执行日志"],
  },
  knowledge: {
    title: "知识库",
    description: "检索和维护已持久化的制度与审查依据。",
    tabs: ["文档库", "知识条目", "检索测试"],
    details: ["文档内容", "条目与引用", "适用范围", "版本记录"],
  },
  organization: {
    title: "组织与权限",
    description: "查看当前身份可见的公开成员身份信息。",
    tabs: ["成员管理", "组织架构", "角色权限"],
    details: ["成员信息", "角色与权限", "案件授权", "活动记录"],
  },
  templates: {
    title: "模板管理",
    description: "查看服务端当前模板版本及结构化字段。",
    tabs: ["模板库", "字段配置", "生成记录"],
    details: ["模板预览", "字段定义", "使用记录", "版本记录"],
  },
  models: {
    title: "模型服务",
    description: "查看真实模型配置与快照报告状态。",
    tabs: ["模型服务", "调用记录", "预算配置"],
    details: ["服务配置", "调用统计", "能力边界", "变更记录"],
  },
  tools: {
    title: "工具网关",
    description: "查看实际注册工具及快照记录的调用状态。",
    tabs: ["工具目录", "调用记录", "权限策略"],
    details: ["工具信息", "输入与输出", "权限与预算", "调用记录"],
  },

  audit: {
    title: "审计日志",
    description: "查看工作台快照内返回的业务审计事件。",
    tabs: ["业务审计", "操作日志", "访问记录"],
    details: ["事件详情", "变更内容", "来源上下文", "关联事件"],
  },
} as const;

export type WorkbenchModuleKey = keyof typeof WORKBENCH_MODULES;

export function isWorkbenchModuleKey(
  value: string,
): value is WorkbenchModuleKey {
  return Object.hasOwn(WORKBENCH_MODULES, value);
}

type DetailField = { label: string; value: string };
type DetailPanel = {
  fields: DetailField[];
  body: string | null;
  links: { label: string; href: string }[];
};
type ModuleRecord = {
  id: string;
  title: string;
  subtitle: string;
  status: string;
  searchText: string;
  updatedAt: string | null;
  caseId: string | null;
  runId: string | null;
  href: string | null;
  panels: Record<string, DetailPanel>;
  task: WorkbenchTask | null;
};

function field(label: string, value: string | number | null): DetailField {
  return {
    label,
    value: value === null || value === "" ? "未报告" : String(value),
  };
}

function panel(
  fields: DetailField[],
  body: string | null = null,
  links: { label: string; href: string }[] = [],
): DetailPanel {
  return { fields, body, links };
}

function caseUrl(caseId: string, runId: string | null): string {
  return runId
    ? `/cases/${encodeURIComponent(caseId)}/reviews/${encodeURIComponent(runId)}`
    : `/cases/${encodeURIComponent(caseId)}`;
}

function emptyPanel(note: string): DetailPanel {
  return panel([], note);
}

function buildRecords(
  snapshot: WorkbenchSnapshot,
  module: WorkbenchModuleKey,
): ModuleRecord[] {
  const caseById = new Map(snapshot.cases.map((item) => [item.id, item]));
  const memberById = new Map(
    snapshot.members.map((item) => [item.id, item.display_name]),
  );
  const auditByCase = new Map<string, typeof snapshot.audit>();
  const auditByActor = new Map<string, typeof snapshot.audit>();
  for (const item of snapshot.audit) {
    if (item.case_id !== null)
      auditByCase.set(item.case_id, [
        ...(auditByCase.get(item.case_id) ?? []),
        item,
      ]);
    auditByActor.set(item.actor_id, [
      ...(auditByActor.get(item.actor_id) ?? []),
      item,
    ]);
  }
  const make = (values: Omit<ModuleRecord, "searchText">): ModuleRecord => ({
    ...values,
    searchText:
      `${values.id} ${values.title} ${values.subtitle} ${values.status} ${Object.values(
        values.panels,
      )
        .flatMap((item) => [
          item.body ?? "",
          ...item.fields.map((entry) => `${entry.label} ${entry.value}`),
        ])
        .join(" ")}`.toLocaleLowerCase("zh-CN"),
  });

  if (module === "tasks")
    return snapshot.tasks.map((task) => {
      const relatedCase = caseById.get(task.case_id);
      const caseAudit = auditByCase.get(task.case_id) ?? [];
      const documents = snapshot.documents.filter(
        (item) => item.case_id === task.case_id,
      );
      const panels: Record<string, DetailPanel> = {
        任务详情: panel([
          field("任务 ID", task.id),
          field("任务类型", task.kind),
          field("优先级", task.priority),
          field("状态", task.status),
          field("责任人", memberById.get(task.assignee_id) ?? task.assignee_id),
          field("截止时间", task.due_at),
          field("版本", task.version),
        ]),
        处理记录: caseAudit.length
          ? panel(
              caseAudit
                .slice(0, 8)
                .map((item) =>
                  field(
                    `${item.actor_name} · ${item.action}`,
                    `${formatDateTime(item.at)} · ${item.summary}`,
                  ),
                ),
            )
          : emptyPanel("快照没有返回相关案件的业务审计事件。"),
        关联材料: documents.length
          ? panel(
              documents.map((item) =>
                field(
                  item.filename,
                  `${item.role} · ${item.parse_status} · ${item.id}`,
                ),
              ),
              null,
              documents.map((item) => ({
                label: `读取 ${item.filename}`,
                href: paths.documentOriginal(item.case_id, item.id),
              })),
            )
          : emptyPanel("案件快照没有返回材料。"),
        协作成员: panel([
          field(
            "当前责任人",
            memberById.get(task.assignee_id) ?? task.assignee_id,
          ),
          field(
            "案件负责人",
            relatedCase?.profile?.owner_id
              ? (memberById.get(relatedCase.profile.owner_id) ??
                  relatedCase.profile.owner_id)
              : null,
          ),
        ]),
      };
      return make({
        id: task.id,
        title: task.title,
        subtitle: relatedCase?.title ?? task.case_id,
        status: task.status,
        updatedAt: task.updated_at,
        caseId: task.case_id,
        runId: relatedCase?.latest_run?.id ?? null,
        href: caseUrl(task.case_id, relatedCase?.latest_run?.id ?? null),
        panels,
        task,
      });
    });

  if (module === "remediation")
    return snapshot.remediations.map((item) => {
      const documents = snapshot.documents.filter(
        (document) =>
          document.case_id === item.case_id &&
          item.document_ids.includes(document.id),
      );
      const caseAudit = auditByCase.get(item.case_id) ?? [];
      const panels: Record<string, DetailPanel> = {
        整改详情: panel([
          field("整改 ID", item.id),
          field("状态", item.status),
          field("责任人", memberById.get(item.assignee_id) ?? item.assignee_id),
          field("截止时间", item.due_at),
          field("整改措施", item.measure),
          field("最近理由", item.reason),
          field("版本", item.version),
        ]),
        材料与版本: documents.length
          ? panel(
              documents.map((document) =>
                field(
                  document.filename,
                  `${document.role} · ${document.parse_status} · ${document.digest}`,
                ),
              ),
              null,
              documents.map((document) => ({
                label: `读取 ${document.filename}`,
                href: paths.documentOriginal(document.case_id, document.id),
              })),
            )
          : emptyPanel("整改记录未关联快照中的材料。"),
        复核记录: panel(
          [
            field("复核运行", item.verification_run_id),
            field("复核尝试", item.verification_attempt_id),
            field("原始运行", item.run_id),
            field("原始尝试", item.attempt_id),
          ],
          null,
          [
            { label: "打开原始审查", href: caseUrl(item.case_id, item.run_id) },
            ...(item.verification_run_id
              ? [
                  {
                    label: "打开复核审查",
                    href: caseUrl(item.case_id, item.verification_run_id),
                  },
                ]
              : []),
          ],
        ),
        操作历史: caseAudit.length
          ? panel(
              caseAudit
                .slice(0, 8)
                .map((entry) =>
                  field(
                    `${entry.actor_name} · ${entry.action}`,
                    `${formatDateTime(entry.at)} · ${entry.summary}`,
                  ),
                ),
            )
          : emptyPanel("快照没有返回相关案件的审计历史。"),
      };
      return make({
        id: item.id,
        title: item.measure,
        subtitle: item.case_title,
        status: item.status,
        updatedAt: item.updated_at,
        caseId: item.case_id,
        runId: item.run_id,
        href: caseUrl(item.case_id, item.run_id),
        panels,
        task: null,
      });
    });

  if (module === "reports")
    return snapshot.reports.map((item) => {
      const links = [
        {
          label: "读取冻结 HTML",
          href: paths.report(item.case_id, item.id, "html"),
        },
        {
          label: "读取冻结 JSON",
          href: paths.report(item.case_id, item.id, "json"),
        },
      ];
      const panels: Record<string, DetailPanel> = {
        报告预览: panel(
          [
            field("报告 ID", item.id),
            field("案件", item.case_title),
            field("生成时间", item.created_at),
          ],
          "报告内容从服务端冻结快照读取。",
          links,
        ),
        发现与决定: panel(
          [field("运行 ID", item.run_id), field("尝试 ID", item.attempt_id)],
          "发现与决定以冻结报告正文为准；请读取冻结 HTML 或 JSON。",
          links,
        ),
        版本与来源: panel([
          field("报告版本", item.version),
          field("输入摘要", item.input_hash),
          field("输出摘要", item.output_hash),
          field("决定快照摘要", item.decision_snapshot_hash),
          field("报告摘要", item.digest),
        ]),
        导出记录: panel(
          [
            field("生成时间", item.created_at),
            field("运行 ID", item.run_id),
            field("尝试 ID", item.attempt_id),
          ],
          null,
          links,
        ),
      };
      return make({
        id: item.id,
        title: `${item.case_title} · 审查报告 v${item.version}`,
        subtitle: item.case_id,
        status: "已冻结",
        updatedAt: item.created_at,
        caseId: item.case_id,
        runId: item.run_id,
        href: reportPagePath(item.case_id, item.id),
        panels,
        task: null,
      });
    });

  if (module === "evidence")
    return snapshot.documents.map((item) => {
      const parsed = item.parsed_document;
      const integrity =
        parsed === null
          ? "未生成解析产物"
          : parsed.original_digest === item.digest
            ? "原件摘要与解析来源一致"
            : "原件摘要与解析来源不一致";
      const links = [
        {
          label: "读取原件",
          href: paths.documentOriginal(item.case_id, item.id),
        },
      ];
      const relatedCase = caseById.get(item.case_id);
      const panels: Record<string, DetailPanel> = {
        原文预览: panel(
          [
            field("文件名", item.filename),
            field("类型", item.media_type),
            field("字节数", item.size),
            field("解析状态", item.parse_status),
          ],
          "原件按案件权限实时读取；PDF 页数及未返回的页面信息不作估算。",
          links,
        ),
        引用片段: panel(
          [
            field("案件", item.case_title),
            field("解析产物", parsed?.id ?? null),
          ],
          "材料引用锚点随具体审查运行返回。打开实际审查工作区查看被引用原文。",
          relatedCase?.latest_run
            ? [
                {
                  label: "查看审查引用",
                  href: caseUrl(item.case_id, relatedCase.latest_run.id),
                },
              ]
            : links,
        ),
        来源与身份: panel([
          field("材料 ID", item.id),
          field("角色", item.role),
          field("原件摘要", item.digest),
          field("上传时间", item.created_at),
          field("解析器", parsed?.parser_id ?? null),
          field("解析版本", parsed?.parser_version ?? null),
        ]),
        使用记录: relatedCase?.latest_run
          ? panel(
              [
                field("最新运行", relatedCase.latest_run.id),
                field("最新状态", relatedCase.latest_run.state),
                field("更新时间", relatedCase.latest_run.updated_at),
              ],
              "完整历史引用需进入各次持久审查工作区。",
              [
                {
                  label: "打开最近审查",
                  href: caseUrl(item.case_id, relatedCase.latest_run.id),
                },
              ],
            )
          : emptyPanel("案件尚无快照所示的审查运行。"),
        完整性校验: panel(
          [
            field("材料 ID", item.id),
            field("解析产物 ID", parsed?.id ?? null),
            field("原件摘要", item.digest),
            field("解析来源原件摘要", parsed?.original_digest ?? null),
            field("归一化内容摘要", parsed?.normalized_digest ?? null),
            field("校验结果", integrity),
          ],
          "校验结果逐字比较同一材料记录的原件摘要与持久解析来源摘要；解析产物 ID 和归一化摘要来自已保存的 DocumentVersion 投影。未生成解析产物时保持未校验。",
        ),
      };
      return make({
        id: item.id,
        title: item.filename,
        subtitle: item.case_title,
        status: item.parse_status,
        updatedAt: item.created_at,
        caseId: item.case_id,
        runId: relatedCase?.latest_run?.id ?? null,
        href: links[0]?.href ?? null,
        panels,
        task: null,
      });
    });

  if (module === "executions")
    return snapshot.runs.map((item) => {
      const usage = item.usage;
      const panels: Record<string, DetailPanel> = {
        运行概览: panel(
          [
            field("运行 ID", item.id),
            field("案件", item.case_title),
            field("运行状态", item.state),
            field("尝试 ID", item.attempt_id),
            field("开始时间", item.started_at),
            field("结束时间", item.finished_at),
            field("创建时间", item.created_at),
            field("更新时间", item.updated_at),
          ],
          null,
          [
            {
              label: "打开完整执行工作区",
              href: caseUrl(item.case_id, item.id),
            },
          ],
        ),
        调用时间轴: panel(
          [
            field("实际调用数", usage?.calls ?? null),
            field("用量未知调用数", usage?.unknown_calls ?? null),
            field("已报告来源", usage?.sources.join("、") ?? null),
          ],
          "快照没有返回单次调用事件；详细持久事件在审查工作区的执行检查器中查看。",
          [{ label: "查看执行事件", href: caseUrl(item.case_id, item.id) }],
        ),
        输入与输出: panel(
          [
            field("输入 tokens", usage?.input_tokens ?? null),
            field("输出 tokens", usage?.output_tokens ?? null),
            field("总 tokens", usage?.total_tokens ?? null),
            field(
              "成本",
              usage?.cost === null || usage === null ? null : usage.cost,
            ),
          ],
          "未返回的用量保持未报告，不作推算。",
        ),
        执行日志: panel(
          [
            field("状态", item.state),
            field("开始时间", item.started_at),
            field("结束时间", item.finished_at),
          ],
          "打开审查工作区以读取该运行实际冻结的执行事件。",
          [{ label: "查看运行记录", href: caseUrl(item.case_id, item.id) }],
        ),
      };
      return make({
        id: item.id,
        title: item.case_title,
        subtitle: item.id,
        status: item.state,
        updatedAt: item.updated_at,
        caseId: item.case_id,
        runId: item.id,
        href: caseUrl(item.case_id, item.id),
        panels,
        task: null,
      });
    });

  if (module === "knowledge")
    return snapshot.knowledge.map((item) => {
      const panels: Record<string, DetailPanel> = {
        文档内容: panel(
          [
            field("知识条目", item.id),
            field("版本", item.version),
            field("来源", item.source_type),
            field("生效时间", item.effective_at),
            field("维护人", memberById.get(item.created_by) ?? item.created_by),
          ],
          item.body,
        ),
        条目与引用: panel(
          [
            field("条目 ID", item.id),
            field("版本", item.version),
            field("来源类型", item.source_type),
          ],
          item.body,
        ),
        适用范围: panel(
          [
            field("来源类型", item.source_type),
            field("生效时间", item.effective_at),
          ],
          "快照没有返回额外适用范围字段。",
        ),
        版本记录: panel(
          [
            field("当前版本", item.version),
            field("创建时间", item.created_at),
            field("创建者", memberById.get(item.created_by) ?? item.created_by),
          ],
          "同一知识条目的不可覆盖与版本引用由服务端维护。",
        ),
      };
      return make({
        id: item.id,
        title: item.title,
        subtitle: `${item.source_type} · ${item.version}`,
        status: item.source_type,
        updatedAt: item.created_at,
        caseId: null,
        runId: null,
        href: null,
        panels,
        task: null,
      });
    });

  if (module === "organization")
    return snapshot.members.map((item) => {
      const fields = [
        field("身份 ID", item.id),
        field("显示姓名", item.display_name),
        field("角色", item.role),
        field("租户 ID", item.tenant_id),
        field("组织 ID", item.org_id),
      ];
      const memberEvents = [...(auditByActor.get(item.id) ?? [])]
        .sort((left, right) => right.at.localeCompare(left.at))
        .slice(0, 8);
      const panels: Record<string, DetailPanel> = {
        成员信息: panel(fields),
        角色与权限: panel(
          [
            field("角色", item.role),
            field("租户", item.tenant_id),
            field("组织", item.org_id),
          ],
          "服务端只在公开成员目录中返回这些身份字段；案件资源仍按每次请求授权。",
        ),
        案件授权: panel(
          [
            field("身份 ID", item.id),
            field("角色", item.role),
            field("租户", item.tenant_id),
            field("组织", item.org_id),
          ],
          "成员快照不包含逐案件 ACL；不能从当前用户的可见案件推断此身份的授权范围。",
        ),
        活动记录: memberEvents.length
          ? panel(
              memberEvents.map((entry) =>
                field(
                  `${entry.action} · ${formatDateTime(entry.at)}`,
                  `${entry.case_title ?? "平台级事件"} · ${entry.summary}`,
                ),
              ),
              "以下仅是快照中关联到此 actor_id 的业务审计事件，不代表登录或 HTTP 访问日志。",
              [{ label: "查看审计事件", href: "/workbench/audit" }],
            )
          : emptyPanel("快照没有返回关联到此身份的业务审计事件。"),
      };
      return make({
        id: item.id,
        title: item.display_name,
        subtitle: `${item.role} · ${item.org_id}`,
        status: "当前身份可见",
        updatedAt: null,
        caseId: null,
        runId: null,
        href: null,
        panels,
        task: null,
      });
    });

  if (module === "templates")
    return snapshot.templates.map((item) => {
      const fieldRows = item.fields.map((entry) =>
        field(
          entry.label,
          `${entry.key} · ${entry.type} · ${entry.required ? "必填" : "选填"}`,
        ),
      );
      const panels: Record<string, DetailPanel> = {
        模板预览: panel(
          [
            field("模板", item.name),
            field("ID", item.id),
            field("版本", item.version),
            field("摘要", item.digest),
          ],
          "模板由服务端返回；此处不拼装虚构合同正文。",
        ),
        字段定义: panel(fieldRows),
        使用记录: panel(
          [field("当前身份可见案件", snapshot.cases.length)],
          "工作台快照不包含模板级生成日志；案件详情页展示各案件真实保存的生成记录。",
          [{ label: "打开案件中心查看生成记录", href: "/cases" }],
        ),
        版本记录: panel(
          [field("当前版本", item.version), field("内容摘要", item.digest)],
          "快照仅返回当前模板版本。",
        ),
      };
      return make({
        id: item.id,
        title: item.name,
        subtitle: `${item.id} · ${item.version}`,
        status: "状态未提供",
        updatedAt: null,
        caseId: null,
        runId: null,
        href: null,
        panels,
        task: null,
      });
    });

  if (module === "models") {
    const model = snapshot.model;
    const platformEvents = snapshot.audit
      .filter((item) => item.case_id === null)
      .slice(0, 8);
    const record = make({
      id: model.name,
      title: model.label || model.name,
      subtitle: model.name,
      status: model.status === "configured" ? "已配置" : "未配置",
      updatedAt: null,
      caseId: null,
      runId: null,
      href: null,
      panels: {
        服务配置: panel(
          [
            field("模型名", model.name),
            field("OCR 模型", model.ocr_name),
            field(
              "配置状态",
              model.status === "configured" ? "已配置" : "未配置",
            ),
            field("显示标签", model.label),
          ],
          "配置状态不代表端点连通；工作台快照不调用外部探测。",
        ),
        调用统计: panel(
          [
            field("快照运行记录数", snapshot.runs.length),
            field(
              "有用量汇总的运行",
              snapshot.runs.filter((item) => item.usage !== null).length,
            ),
            field(
              "已报告调用总数",
              snapshot.runs.some((item) => item.usage !== null)
                ? snapshot.runs.reduce(
                    (total, item) => total + (item.usage?.calls ?? 0),
                    0,
                  )
                : null,
            ),
          ],
          "快照没有返回费用或模型连接地址。费用保持未知。",
        ),
        能力边界: panel(
          [field("聊天模型", model.name), field("OCR 模型", model.ocr_name)],
          "此快照未声明额外能力或端点探测结果。",
        ),
        变更记录: panel(
          platformEvents.map((item) =>
            field(`${item.action} · ${formatDateTime(item.at)}`, item.summary),
          ),
          "这里只列工作台已返回的平台级业务审计，不将规则发布等事件解释为模型专属配置变更。",
          [{ label: "打开业务审计", href: "/workbench/audit" }],
        ),
      },
      task: null,
    });
    return [record];
  }

  if (module === "tools")
    return snapshot.tools.map((item) => {
      const panels: Record<string, DetailPanel> = {
        工具信息: panel([
          field("工具 ID", item.id),
          field("名称", item.name),
          field("说明", item.description),
          field("最近状态", item.last_status),
        ]),
        输入与输出: panel(
          [field("能力", item.capabilities.join("、") || null)],
          "快照未包含单次调用的输入输出正文。",
        ),
        权限与预算: panel(
          [field("登记能力", item.capabilities.join("、") || null)],
          "权限与预算不在公开工具快照中返回。",
        ),
        调用记录: panel(
          [
            field("已统计调用", item.calls),
            field("最近状态", item.last_status),
          ],
          "快照不含调用明细；进入相关审查运行查看已持久化执行事件。",
        ),
      };
      return make({
        id: item.id,
        title: item.name,
        subtitle: item.description,
        status: item.last_status ?? "未报告",
        updatedAt: null,
        caseId: null,
        runId: null,
        href: null,
        panels,
        task: null,
      });
    });

  return snapshot.audit.map((item) => {
    const caseLabel =
      item.case_id === null ? "平台级事件" : (item.case_title ?? item.case_id);
    const relatedAudit =
      item.case_id === null ? [] : (auditByCase.get(item.case_id) ?? []);
    const panels: Record<string, DetailPanel> = {
      事件详情: panel([
        field("事件 ID", item.id),
        field("资源范围", caseLabel),
        field("动作", item.action),
        field("操作人", item.actor_name),
        field("时间", item.at),
        field("摘要", item.summary),
      ]),
      变更内容: panel(
        [field("动作", item.action), field("摘要", item.summary)],
        "只呈现服务端保存的审计摘要。",
      ),
      来源上下文: panel([
        field("资源范围", caseLabel),
        field("操作人 ID", item.actor_id),
        field("动作", item.action),
      ]),
      关联事件:
        item.case_id === null
          ? emptyPanel("平台级事件没有案件关联。")
          : relatedAudit.length > 1
            ? panel(
                relatedAudit
                  .slice(0, 12)
                  .map((entry) =>
                    field(
                      `${entry.actor_name} · ${entry.action}`,
                      `${formatDateTime(entry.at)} · ${entry.summary}`,
                    ),
                  ),
              )
            : emptyPanel("快照没有返回该案件的其他审计事件。"),
    };
    return make({
      id: item.id,
      title: item.action,
      subtitle: caseLabel,
      status: "已记录",
      updatedAt: item.at,
      caseId: item.case_id,
      runId: null,
      href: item.case_id === null ? null : caseUrl(item.case_id, null),
      panels,
      task: null,
    });
  });
}

function tabNotice(module: WorkbenchModuleKey, tab: string): string | null {
  if (module === "tasks" && tab === "我发起的")
    return "按任务的服务端创建人 ID 匹配当前身份；历史任务未记录创建人时不会出现在此列表。";
  if (module === "evidence" && tab === "引用片段")
    return "引用锚点需要结合指定 run/attempt 读取，请从材料详情进入真实审查工作区。";
  if (module === "executions" && tab === "模型调用")
    return "本页展示运行级统计；单次调用时间轴与输入输出需打开具体审查工作区。";
  if (module === "executions" && tab === "规则执行")
    return "规则执行结果记录在具体运行中，请打开审查工作区查看实际执行记录。";
  if (module === "organization" && tab === "组织架构")
    return "目录按真实 org_id 展示成员；快照没有上下级组织关系。";
  if (module === "organization" && tab === "角色权限")
    return "显示服务端公开角色与组织字段；资源授权仍由服务端逐次判断。";
  if (module === "audit" && tab === "操作日志")
    return "按当前身份 actor_id 筛选工作台返回的业务审计事件；不是 HTTP 访问日志。";
  if (module === "audit" && tab === "访问记录")
    return "HTTP 访问日志不在工作台业务审计快照中。";

  if (module === "knowledge" && tab === "检索测试")
    return "输入检索词后匹配工作台快照中的持久标题与全文；这是本地快照子串检索，不调用模型。";
  return null;
}

function tabMatches(
  module: WorkbenchModuleKey,
  tab: string,
  record: ModuleRecord,
  actorId: string | undefined,
  snapshot: WorkbenchSnapshot,
): boolean {
  if (module === "tasks") {
    if (tab === "我的待办")
      return (
        record.task !== null &&
        record.task.assignee_id === actorId &&
        record.task.status !== "completed"
      );
    if (tab === "已完成") return record.task?.status === "completed";
    if (tab === "我发起的") return record.task?.created_by === actorId;
  }
  if (module === "remediation") {
    if (tab === "待整改")
      return record.status === "open" || record.status === "reopened";
    if (tab === "待复核") return record.status === "submitted";
    if (tab === "已关闭") return record.status === "closed";
  }
  if (module === "reports" && tab === "版本对比") {
    const reportCount = snapshot.reports.filter(
      (item) => item.case_id === record.caseId,
    ).length;
    return reportCount > 1;
  }
  if (module === "audit" && tab === "操作日志") {
    const actorField = record.panels["来源上下文"]?.fields.find(
      (item) => item.label === "操作人 ID",
    );
    return actorField?.value === actorId;
  }

  if (module === "audit" && tab === "访问记录") return false;
  return true;
}

function statusTone(status: string): "good" | "attention" | "bad" | "neutral" {
  if (status === "ready" || status === "closed" || status === "completed")
    return "good";
  if (status === "unavailable" || status === "failed" || status === "high")
    return "bad";
  if (
    status === "unknown" ||
    status === "submitted" ||
    status === "open" ||
    status === "reopened"
  )
    return "attention";
  return "neutral";
}

function TaskCreateForm({
  snapshot,
  onCreated,
}: {
  snapshot: WorkbenchSnapshot;
  onCreated: () => void;
}) {
  const [caseId, setCaseId] = useState("");
  const [title, setTitle] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [dueDate, setDueDate] = useState("");
  const create = useCreateWorkbenchTask(caseId);
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    create.mutate(
      {
        title: title.trim(),
        kind: "general",
        assignee_id: assigneeId,
        due_at: dueDate ? `${dueDate}T23:59:59+08:00` : null,
        status: "open",
        priority: "medium",
        finding_id: null,
      },
      {
        onSuccess: () => {
          setTitle("");
          setDueDate("");
          onCreated();
        },
      },
    );
  }
  return (
    <form className="module-create-form" onSubmit={submit}>
      <h2>新建任务</h2>
      <label>
        关联案件
        <Select<string>
          aria-label={"关联案件"}
          value={String(caseId)}
          onChange={(selectedValue) => setCaseId(selectedValue)}
          options={[
            { value: "", label: "选择案件" },
            ...snapshot.cases.map((item) => ({
              value: String(item.id),
              label: String(item.title ?? ""),
            })),
          ]}
        />
      </label>
      <label>
        任务名称
        <Input
          required
          maxLength={240}
          value={title}
          onChange={(event) => setTitle(event.target.value)}
        />
      </label>
      <label>
        责任人
        <Select<string>
          aria-label={"责任人"}
          value={String(assigneeId)}
          onChange={(selectedValue) => setAssigneeId(selectedValue)}
          options={[
            { value: "", label: "选择责任人" },
            ...snapshot.members.map((item) => ({
              value: String(item.id),
              label: (
                <>
                  {item.display_name}· {item.role}
                </>
              ),
            })),
          ]}
        />
      </label>
      <label>
        截止日期
        <Input
          type="date"
          value={dueDate}
          onChange={(event) => setDueDate(event.target.value)}
        />
      </label>
      {create.isError ? <p role="alert">{errorMessage(create.error)}</p> : null}
      <Button
        aria-label={String(create.isPending ? "正在保存" : "保存任务")}
        type="primary"
        htmlType="submit"
        disabled={create.isPending || caseId === "" || assigneeId === ""}
      >
        {create.isPending ? "正在保存" : "保存任务"}
      </Button>
    </form>
  );
}

function KnowledgeCreateForm({ onClose }: { onClose: () => void }) {
  const [id, setId] = useState("");
  const [title, setTitle] = useState("");
  const [version, setVersion] = useState("1.0.0");
  const [sourceType, setSourceType] =
    useState<KnowledgeInput["source_type"]>("synthetic_policy");
  const [effectiveAt, setEffectiveAt] = useState("");
  const [body, setBody] = useState("");
  const create = useCreateKnowledge();
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    create.mutate(
      {
        id: id.trim() || undefined,
        title: title.trim(),
        version: version.trim(),
        body: body.trim(),
        source_type: sourceType,
        effective_at: effectiveAt ? new Date(effectiveAt).toISOString() : null,
      },
      { onSuccess: onClose },
    );
  }
  return (
    <form className="module-create-form" onSubmit={submit}>
      <h2>新增知识条目</h2>
      <label>
        稳定 ID（可选）
        <Input value={id} onChange={(event) => setId(event.target.value)} />
      </label>
      <label>
        标题
        <Input
          required
          value={title}
          onChange={(event) => setTitle(event.target.value)}
        />
      </label>
      <label>
        版本
        <Input
          required
          value={version}
          onChange={(event) => setVersion(event.target.value)}
        />
      </label>
      <label>
        来源类型
        <Select<string>
          aria-label={"来源类型"}
          value={String(sourceType)}
          onChange={(selectedValue) =>
            setSourceType(
              selectedValue === "reference" ? "reference" : "synthetic_policy",
            )
          }
          options={[
            { value: "synthetic_policy", label: "合成制度" },
            { value: "reference", label: "参考资料" },
          ]}
        />
      </label>
      <label>
        生效时间
        <Input
          type="datetime-local"
          value={effectiveAt}
          onChange={(event) => setEffectiveAt(event.target.value)}
        />
      </label>
      <label className="module-form-wide">
        全文
        <Input.TextArea
          required
          rows={7}
          value={body}
          onChange={(event) => setBody(event.target.value)}
        />
      </label>
      {create.isError ? <p role="alert">{errorMessage(create.error)}</p> : null}
      <div className="module-form-actions">
        <Button aria-label={"取消"} htmlType="button" onClick={onClose}>
          取消
        </Button>
        <Button
          aria-label={String(create.isPending ? "正在保存" : "保存知识条目")}
          type="primary"
          htmlType="submit"
          disabled={
            create.isPending || title.trim() === "" || body.trim() === ""
          }
        >
          {create.isPending ? "正在保存" : "保存知识条目"}
        </Button>
      </div>
    </form>
  );
}

export function WorkbenchModulePage() {
  const { module: routeModule = "" } = useParams();
  if (routeModule === "settings") return <SettingsPage audience="client" />;
  if (!isWorkbenchModuleKey(routeModule))
    return <PageStatus kind="empty" title="没有这个工作区页面" />;
  return <WorkbenchModule key={routeModule} module={routeModule} />;
}

function WorkbenchModule({ module }: { module: WorkbenchModuleKey }) {
  const [search, setSearch] = useState("");
  const [tab, setTab] = useState<string>(WORKBENCH_MODULES[module].tabs[0]);
  const [detailTab, setDetailTab] = useState<string>(
    WORKBENCH_MODULES[module].details[0],
  );
  const [selectedId, setSelectedId] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [updatedTaskId, setUpdatedTaskId] = useState("");
  const session = useSession();
  const query = useWorkbench("week");
  const updateTask = useUpdateWorkbenchTask();
  const refresh = () => void query.refetch();

  if (query.isLoading)
    return (
      <PageStatus
        kind="loading"
        title={`正在加载${WORKBENCH_MODULES[module].title}`}
      />
    );
  if (query.isError || !query.data)
    return (
      <PageStatus
        kind="error"
        title={`${WORKBENCH_MODULES[module].title}没有加载`}
        detail={errorMessage(query.error)}
        action={
          <Button
            aria-label={"重试"}
            type="primary"
            htmlType="button"
            onClick={refresh}
          >
            重试
          </Button>
        }
      />
    );

  const snapshot = query.data;
  const allRecords = buildRecords(snapshot, module);
  const queryText = search.trim().toLocaleLowerCase("zh-CN");
  const records = allRecords.filter(
    (record) =>
      (module !== "knowledge" || tab !== "检索测试" || queryText !== "") &&
      tabMatches(module, tab, record, session.data?.id, snapshot) &&
      record.searchText.includes(queryText),
  );
  const selected =
    records.find((record) => record.id === selectedId) ?? records[0] ?? null;
  const selectedTask = selected?.task ?? null;
  const currentPanel =
    module === "evidence" && tab === "完整性校验"
      ? (selected?.panels["完整性校验"] ?? null)
      : (selected?.panels[detailTab] ?? null);
  const config = WORKBENCH_MODULES[module];
  const notice = tabNotice(module, tab);
  const periodLabel =
    snapshot.period === "day"
      ? "本日"
      : snapshot.period === "week"
        ? "本周"
        : "本月";
  const canCreateTask = module === "tasks";
  const canCreateKnowledge = module === "knowledge";
  const emptyMessage =
    module === "knowledge" && tab === "检索测试" && queryText === ""
      ? "输入检索词，按当前快照搜索已保存的制度标题与正文。"
      : allRecords.length === 0
        ? "快照没有返回记录。"
        : "没有符合此分类或搜索词的记录。";

  function completeTask(task: WorkbenchTask) {
    setUpdatedTaskId(task.id);
    updateTask.mutate(
      {
        caseId: task.case_id,
        taskId: task.id,
        update: {
          title: task.title,
          kind: task.kind,
          assignee_id: task.assignee_id,
          due_at: task.due_at,
          status: "completed",
          priority: task.priority,
          finding_id: task.finding_id,
          expected_version: task.version,
        },
      },
      { onSettled: () => setUpdatedTaskId("") },
    );
  }

  let headerAction = (
    <Button
      aria-label={String(query.isFetching ? "正在更新" : "刷新快照")}
      type="text"
      htmlType="button"
      onClick={refresh}
      disabled={query.isFetching}
    >
      {query.isFetching ? "正在更新" : "刷新快照"}
    </Button>
  );
  if (canCreateTask)
    headerAction = (
      <Button
        aria-label={String(createOpen ? "关闭新建" : "分派任务")}
        type="primary"
        htmlType="button"
        onClick={() => setCreateOpen((value) => !value)}
      >
        {createOpen ? "关闭新建" : "分派任务"}
      </Button>
    );
  if (canCreateKnowledge)
    headerAction = (
      <Button
        aria-label={String(createOpen ? "关闭新建" : "新增知识")}
        type="primary"
        htmlType="button"
        onClick={() => setCreateOpen((value) => !value)}
      >
        {createOpen ? "关闭新建" : "新增知识"}
      </Button>
    );

  return (
    <section className="page module-page">
      <header className="page-head module-page-head">
        <div>
          <h1>
            {config.title}
            <span className="module-total">{allRecords.length}</span>
          </h1>
          <p>
            {config.description} 快照生成于{" "}
            {formatDateTime(snapshot.generated_at)}。
          </p>
        </div>
        <div className="module-actions">{headerAction}</div>
      </header>
      {createOpen && canCreateTask ? (
        <TaskCreateForm
          snapshot={snapshot}
          onCreated={() => setCreateOpen(false)}
        />
      ) : null}
      {createOpen && canCreateKnowledge ? (
        <KnowledgeCreateForm onClose={() => setCreateOpen(false)} />
      ) : null}
      {updateTask.isError ? (
        <p className="module-error" role="alert">
          {errorMessage(updateTask.error)}
        </p>
      ) : null}
      <div className="module-stats" aria-label="真实数据摘要">
        <div>
          <span>工作台集合</span>
          <strong>{allRecords.length}</strong>
        </div>
        <div>
          <span>匹配当前标签</span>
          <strong>{records.length}</strong>
        </div>
        <div>
          <span>数据周期</span>
          <strong>{periodLabel}</strong>
        </div>
        <div>
          <span>快照生成</span>
          <strong>
            <time dateTime={snapshot.generated_at}>
              {formatDateTime(snapshot.generated_at)}
            </time>
          </strong>
        </div>
      </div>
      <div className="module-toolbar">
        <Tabs
          aria-label={`${config.title}分类`}
          activeKey={tab}
          onChange={(key) => {
            setTab(key);
            setSelectedId("");
          }}
          items={config.tabs.map((label) => ({ key: label, label }))}
        />
        <label className="module-search">
          搜索当前快照
          <Input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={
              module === "knowledge"
                ? "检索标题或制度正文"
                : "搜索名称、编号、状态"
            }
          />
        </label>
      </div>
      {notice ? (
        <p className="module-note" role="status">
          {notice}
        </p>
      ) : null}
      <div className="module-workspace">
        <section
          className="module-list-panel"
          aria-label={`${config.title}记录`}
        >
          {records.length === 0 ? (
            <p className="module-empty">{emptyMessage}</p>
          ) : (
            <Table
              rowKey="id"
              dataSource={records}
              pagination={false}
              size="small"
              scroll={{ x: 440 }}
              rowSelection={{
                type: "radio",
                selectedRowKeys: selected ? [selected.id] : [],
                onChange: (keys) => setSelectedId(String(keys[0] ?? "")),
              }}
              columns={[
                {
                  title: "记录",
                  key: "title",
                  render: (_, record) => (
                    <Flex vertical gap={2}>
                      <Button
                        aria-label={String(record.title)}
                        type="link"
                        aria-pressed={selected?.id === record.id}
                        onClick={() => setSelectedId(record.id)}
                        styles={{
                          content: { whiteSpace: "normal", textAlign: "left" },
                        }}
                      >
                        {record.title}
                      </Button>
                      <span className="meta">{record.subtitle}</span>
                    </Flex>
                  ),
                },
                {
                  title: "状态",
                  key: "status",
                  render: (_, record) => (
                    <StatusText tone={statusTone(record.status)}>
                      {record.status}
                    </StatusText>
                  ),
                },
                {
                  title: "更新",
                  key: "updated",
                  render: (_, record) =>
                    record.updatedAt
                      ? formatDateTime(record.updatedAt)
                      : "时间未报告",
                },
              ]}
            />
          )}
        </section>
        <aside className="module-detail-panel" aria-label="所选记录详情">
          {selected ? (
            <>
              <header className="module-detail-head">
                <div>
                  <p>{selected.id}</p>
                  <h2>{selected.title}</h2>
                  <span>{selected.subtitle}</span>
                </div>
                {selected.href ? (
                  <Button
                    aria-label={String(
                      module === "reports"
                        ? "查看报告详情"
                        : module === "evidence"
                          ? "读取材料原件"
                          : "打开关联记录",
                    )}
                    type="text"
                    href={selected.href}
                    target={module === "evidence" ? "_blank" : undefined}
                    rel={module === "evidence" ? "noreferrer" : undefined}
                  >
                    {module === "reports"
                      ? "查看报告详情"
                      : module === "evidence"
                        ? "读取材料原件"
                        : "打开关联记录"}
                  </Button>
                ) : null}
              </header>
              {module === "evidence" && tab === "完整性校验" ? null : (
                <Tabs
                  aria-label="记录详情分类"
                  activeKey={detailTab}
                  onChange={setDetailTab}
                  items={config.details.map((label) => ({ key: label, label }))}
                />
              )}
              {currentPanel ? (
                <div className="module-detail-content">
                  {currentPanel.body !== null ? (
                    <p
                      className={
                        module === "knowledge"
                          ? "module-knowledge-body"
                          : "module-detail-note"
                      }
                    >
                      {currentPanel.body}
                    </p>
                  ) : null}
                  {currentPanel.fields.length > 0 ? (
                    <dl className="module-detail-fields">
                      {currentPanel.fields.map((item) => (
                        <div key={item.label}>
                          <dt>{item.label}</dt>
                          <dd>{item.value}</dd>
                        </div>
                      ))}
                    </dl>
                  ) : null}
                  {currentPanel.links.length > 0 ? (
                    <div className="module-detail-links">
                      {currentPanel.links.map((item) => (
                        <a
                          key={item.href}
                          href={item.href}
                          target={
                            module === "reports" || module === "evidence"
                              ? "_blank"
                              : undefined
                          }
                          rel={
                            module === "reports" || module === "evidence"
                              ? "noreferrer"
                              : undefined
                          }
                        >
                          {item.label}
                        </a>
                      ))}
                    </div>
                  ) : null}
                </div>
              ) : (
                <p className="module-empty">当前快照没有该分类的明细。</p>
              )}
              {selectedTask && selectedTask.status !== "completed" ? (
                <Button
                  aria-label={String(
                    updatedTaskId === selectedTask.id
                      ? "正在保存"
                      : "标记任务完成",
                  )}
                  type="primary"
                  className="module-complete-task"
                  htmlType="button"
                  disabled={updateTask.isPending}
                  onClick={() => completeTask(selectedTask)}
                >
                  {updatedTaskId === selectedTask.id
                    ? "正在保存"
                    : "标记任务完成"}
                </Button>
              ) : null}
              {selected.caseId && module === "remediation" ? (
                <Button
                  aria-label={"进入案件整改与复核"}
                  type="primary"
                  className="module-complete-task"
                  href={`/cases/${encodeURIComponent(selected.caseId)}`}
                >
                  进入案件整改与复核
                </Button>
              ) : null}
            </>
          ) : (
            <p className="module-empty">选择一条记录查看服务端返回的详情。</p>
          )}
        </aside>
      </div>
    </section>
  );
}
