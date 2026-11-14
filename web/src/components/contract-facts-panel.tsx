// File metadata (manifest mirror):
// Purpose: Displays bounded advisory contract facts with explicit uncertainty and actions that locate verified frozen source text without creating risk conclusions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Alert, Button, Card, Tag, Typography } from "antd";
import type { ContractFactsCitation, ContractFactsProjection } from "../api/contract-facts";

const labels: Record<ContractFactsProjection["fields"][number]["field"], string> = {
  parties: "合同主体",
  contract_amount: "合同金额",
  tax_treatment: "税额与税口径",
  business_dates: "业务日期",
  term: "合同期限",
  payment_schedule: "付款安排",
  subject_matter: "标的与范围",
  exceptions: "例外约定",
};

function statusLabel(status: ContractFactsProjection["fields"][number]["status"]): string {
  switch (status) {
    case "known":
      return "有引用的建议";
    case "conflicting":
      return "待核对的差异";
    default:
      return "未知";
  }
}

function statusColor(
  status: ContractFactsProjection["fields"][number]["status"],
): "default" | "processing" | "warning" {
  if (status === "known") return "processing";
  return status === "conflicting" ? "warning" : "default";
}

export function ContractFactsPanel({
  facts,
  onLocate,
}: {
  facts: ContractFactsProjection | null | undefined;
  onLocate?: (citation: ContractFactsCitation, key: string) => void;
}) {
  if (!facts) return null;
  const structuredOutputUnavailable = facts.structured_output_status !== "valid";
  return (
    <Card size="small" title="合同事实建议" variant="outlined">
      <Alert
        type={structuredOutputUnavailable ? "warning" : "info"}
        showIcon
        message="模型建议需人工核对；它不构成风险结论或法律判断。"
        description={
          structuredOutputUnavailable
            ? "请求没有形成可用的结构化事实输出，当前字段只表示未知；请核对冻结原文。"
            : facts.input_truncated
              ? "正文按上限截断，未提供部分保持未知；本次结果为不完整建议。"
              : facts.status === "partial"
                ? "本次没有达到完整覆盖；未知项不表示合同没有约定，仍需核对冻结原文。"
                : "引用可定位到本次冻结原文，引用存在不代表事实归纳正确。"
        }
      />
      <ul>
        {facts.fields.map((field) => (
          <li key={field.field}>
            <Typography.Text strong>{labels[field.field]}</Typography.Text>{" "}
            <Tag color={statusColor(field.status)}>{statusLabel(field.status)}</Tag>
            <Typography.Paragraph>
              {field.status === "known"
                ? field.value
                : field.status === "conflicting"
                  ? "模型提示不同表述，请结合冻结原文核对。"
                  : "没有足够依据，保持未知。"}
            </Typography.Paragraph>
            {field.citations.length > 0 ? (
              <ul>
                {field.citations.map((citation, index) => {
                  const key = `${field.field}-${index}`;
                  return (
                    <li key={key}>
                      <Typography.Text>{citation.quote}</Typography.Text>
                      {onLocate ? (
                        <Button type="link" size="small" onClick={() => onLocate(citation, key)}>
                          定位冻结原文
                        </Button>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </li>
        ))}
      </ul>
    </Card>
  );
}
