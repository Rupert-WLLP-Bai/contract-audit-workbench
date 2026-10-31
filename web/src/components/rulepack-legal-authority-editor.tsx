import { Alert, Button, Input, Select } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { getGovernedKnowledge } from "../api/governed-knowledge";
import {
  searchAdminLegalKnowledge,
  type LegalHit,
} from "../api/legal-knowledge";
import type { RulePackLegalAuthority } from "../api/rulepacks";
import { errorMessage } from "../api/http";

const fieldOptions = [
  { value: "buyer_kind", label: "采购方主体类型" },
  { value: "seller_kind", label: "供应方主体类型" },
  { value: "buyer_enterprise_size", label: "采购方企业规模" },
  { value: "seller_enterprise_size", label: "供应方企业规模" },
  { value: "funding_kind", label: "资金性质" },
  { value: "procurement_scope", label: "采购范围" },
  { value: "project_kind", label: "项目类型" },
  { value: "contract_clause_kind", label: "合同条款类型" },
  { value: "transaction_kind", label: "交易性质" },
] as const;

const valuesByField: Record<string, { value: string; label: string }[]> = {
  buyer_kind: [{ value: "state_organ", label: "国家机关" }, { value: "institution", label: "事业单位" }, { value: "social_organization", label: "社会团体" }, { value: "enterprise", label: "企业" }, { value: "natural_person", label: "自然人" }],
  seller_kind: [{ value: "state_organ", label: "国家机关" }, { value: "institution", label: "事业单位" }, { value: "social_organization", label: "社会团体" }, { value: "enterprise", label: "企业" }, { value: "natural_person", label: "自然人" }],
  buyer_enterprise_size: [{ value: "large", label: "大型企业" }, { value: "sme", label: "中小企业" }, { value: "not_enterprise", label: "非企业主体" }],
  seller_enterprise_size: [{ value: "large", label: "大型企业" }, { value: "sme", label: "中小企业" }, { value: "not_enterprise", label: "非企业主体" }],
  funding_kind: [{ value: "fiscal", label: "财政性资金" }, { value: "mixed_with_fiscal", label: "含财政性资金" }, { value: "state_owned_non_fiscal", label: "非财政性国有资金" }, { value: "private", label: "非国有及非财政资金" }],
  procurement_scope: [{ value: "centralized_catalog", label: "集中采购目录内" }, { value: "above_limit", label: "达到采购限额" }, { value: "catalog_and_above_limit", label: "目录内且达到限额" }, { value: "outside_catalog_and_below_limit", label: "目录外且未达限额" }],
  project_kind: [{ value: "construction", label: "建设工程" }, { value: "construction_related_goods", label: "工程相关货物" }, { value: "construction_related_services", label: "工程相关服务" }, { value: "standalone_goods", label: "独立货物采购" }, { value: "standalone_services", label: "独立服务采购" }],
  contract_clause_kind: [{ value: "standard_form", label: "格式条款" }, { value: "negotiated", label: "协商条款" }],
  transaction_kind: [{ value: "civil_contract", label: "民事合同交易" }, { value: "non_civil", label: "非民事交易" }],
};

interface ApplicabilityDraft {
  field: string;
  one_of: string[];
}

function refKey(hit: LegalHit): string {
  return `${hit.ref.knowledge_id}:${hit.ref.version_id}`;
}

function canBeRuleAuthority(hit: LegalHit): boolean {
  return hit.governance_status === "published" && hit.active &&
    hit.legal_metadata.legal_review_status === "NOT_RUN" &&
    hit.legal_metadata.reviewer === null &&
    hit.legal_metadata.source_provenance === "verified_official_capture" &&
    hit.legal_metadata.official_status === "effective" &&
    ["law", "administrative_regulation", "judicial_interpretation", "normative_notice"].includes(hit.legal_metadata.document_type);
}

export function RulePackLegalAuthorityEditor({
  value,
  disabled,
  onAdd,
  onRemove,
}: {
  value: RulePackLegalAuthority[];
  disabled: boolean;
  onAdd: (authority: RulePackLegalAuthority) => void;
  onRemove: (index: number) => void;
}) {
  const [searchDraft, setSearchDraft] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const [selectedKey, setSelectedKey] = useState("");
  const [quoteSpan, setQuoteSpan] = useState<{ start: number; end: number; quote: string } | null>(null);
  const [conditions, setConditions] = useState<ApplicabilityDraft[]>([]);
  const search = useQuery({
    queryKey: ["admin", "governed-knowledge", "legal-authority", submittedQuery],
    queryFn: ({ signal }) => searchAdminLegalKnowledge({ q: submittedQuery }, signal),
    enabled: submittedQuery.trim() !== "",
    retry: false,
  });
  const selected = search.data?.items.find((item) => refKey(item) === selectedKey);
  const detail = useQuery({
    queryKey: ["admin", "governed-knowledge", "legal-authority-detail", selected?.ref.knowledge_id, selected?.ref.version_id],
    queryFn: ({ signal }) => getGovernedKnowledge(selected!.ref.knowledge_id, selected!.ref.version_id, signal),
    enabled: Boolean(selected && canBeRuleAuthority(selected)),
    retry: false,
  });

  useEffect(() => {
    setQuoteSpan(null);
    setConditions([]);
  }, [selectedKey]);

  const body = detail.data?.content.kind === "legal" ? detail.data.content.body : "";
  const metadata = detail.data?.content.kind === "legal" ? detail.data.content.legal_metadata : undefined;
  const canAdd = Boolean(
    detail.data && metadata && quoteSpan?.quote.trim() && quoteSpan.end > quoteSpan.start &&
    Array.from(quoteSpan.quote).length === quoteSpan.end - quoteSpan.start &&
    conditions.length > 0 && conditions.length <= 9 && conditions.every((item) => item.field && item.one_of.length > 0),
  );

  function submitSearch(event: FormEvent) {
    event.preventDefault();
    setSelectedKey("");
    setSubmittedQuery(searchDraft.trim());
  }

  function updateCondition(index: number, patch: Partial<ApplicabilityDraft>) {
    setConditions((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item));
  }

  function addSelectedAuthority() {
    if (!detail.data || !metadata || !quoteSpan || !canAdd) return;
    onAdd({
      authority_source_id: metadata.source_id,
      knowledge_id: detail.data.ref.knowledge_id,
      version_id: detail.data.ref.version_id,
      content_sha256: detail.data.ref.content_sha256,
      source_id: metadata.source_id,
      source_snapshot_id: metadata.source_snapshot_id,
      norm_title: metadata.norm_title,
      norm_version: metadata.norm_version,
      article_number: metadata.article_number,
      document_type: metadata.document_type,
      source_sha256: metadata.source_sha256,
      article_text_sha256: metadata.norm_text_sha256,
      effective_from: metadata.effective_from,
      effective_until: metadata.effective_until ?? "",
      quote_span: quoteSpan,
      applicability: { all_of: conditions.map((item) => ({ field: item.field, one_of: [...item.one_of] })) },
    });
    setSelectedKey("");
  }

  return (
    <section className="rulepack-editor-section" aria-label="法律条文依据">
      <h3>法律条文依据</h3>
      <p className="meta">选择已发布的官方法律版本、原文范围和适用条件。引用只记录依据，不自动形成法律结论；缺少案件事实时适用性仍由服务端判为未知。</p>
      {value.length > 0 ? (
        <ul className="document-list">
          {value.map((authority, index) => (
            <li key={`${authority.knowledge_id}:${authority.version_id}:${authority.quote_span.start}`}>
              <strong>{authority.norm_title} · 第{authority.article_number}条 · {authority.norm_version}</strong>
              <p>{authority.quote_span.quote}</p>
              <p className="meta">来源快照 {authority.source_snapshot_id} · {authority.applicability.all_of.length} 项适用条件</p>
              <Button htmlType="button" disabled={disabled} onClick={() => onRemove(index)}>移除此依据</Button>
            </li>
          ))}
        </ul>
      ) : <p className="meta">尚未添加法律条文依据。</p>}
      <form className="workflow-form" onSubmit={submitSearch}>
        <label className="field">查找已发布法律条文
          <Input aria-label="查找已发布法律条文" value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} disabled={disabled} placeholder="法规名称、条号或正文" />
        </label>
        <Button htmlType="submit" disabled={disabled || !searchDraft.trim()}>搜索条文</Button>
      </form>
      {search.isError ? <Alert type="error" showIcon title="法律条文检索失败" description={errorMessage(search.error)} action={<Button onClick={() => void search.refetch()}>重试</Button>} /> : null}
      {search.isLoading ? <p role="status">正在读取法律条文…</p> : null}
      {search.data ? (
        <label className="field">选择精确法律版本
          <Select
            aria-label="选择精确法律版本"
            value={selectedKey || undefined}
            disabled={disabled}
            placeholder="选择法规、条号和版本"
            options={search.data.items.map((item) => ({
              value: refKey(item),
              label: `${item.legal_metadata.norm_title} · ${item.legal_metadata.article_label} · ${item.legal_metadata.norm_version} · ${item.governance_status}${item.active ? "" : " · 非当前"}`,
              disabled: !canBeRuleAuthority(item),
            }))}
            onChange={(key: string) => setSelectedKey(key)}
          />
        </label>
      ) : null}
      {selected && !canBeRuleAuthority(selected) ? <Alert type="warning" showIcon title="此版本不能作为当前规则依据" description="仅可选择已发布、当前有效且有官方捕获记录的法律规范。历史版本仍可查看，但须另有明确的历史适用依据。" /> : null}
      {detail.isError ? <Alert type="error" showIcon title="精确法律版本没有加载" description={errorMessage(detail.error)} action={<Button onClick={() => void detail.refetch()}>重试</Button>} /> : null}
      {detail.isLoading ? <p role="status">正在读取法律原文…</p> : null}
      {detail.isSuccess && metadata ? (
        <div className="rulepack-legal-authority-detail">
          <p><strong>{metadata.norm_title} · {metadata.article_label} · {metadata.norm_version}</strong></p>
          <p>{metadata.issuer} · {metadata.official_status} · 施行于 {metadata.effective_from}{metadata.effective_until ? ` 至 ${metadata.effective_until}` : "起"}</p>
          <p className="meta">来源捕获 {metadata.source_snapshot_id} · SHA-256 {metadata.source_sha256} · 法律审阅未审核（NOT_RUN）</p>
          <label className="field">选择法律原文引用范围
            <Input.TextArea
              aria-label="选择法律原文引用范围"
              value={body}
              readOnly
              rows={8}
              disabled={disabled}
              onSelect={(event) => {
                const startUTF16 = event.currentTarget.selectionStart;
                const endUTF16 = event.currentTarget.selectionEnd;
                if (endUTF16 <= startUTF16) { setQuoteSpan(null); return; }
                setQuoteSpan({
                  start: Array.from(body.slice(0, startUTF16)).length,
                  end: Array.from(body.slice(0, endUTF16)).length,
                  quote: body.slice(startUTF16, endUTF16),
                });
              }}
            />
          </label>
          {quoteSpan ? <p role="status">已选择 {quoteSpan.end - quoteSpan.start} 个 Unicode 字符：{quoteSpan.quote}</p> : <p className="meta">在原文中拖选需要引用的文字。</p>}
          <fieldset disabled={disabled}>
            <legend>规则适用条件（全部条件同时成立）</legend>
            {conditions.map((condition, index) => (
              <div className="governance-filters" key={`${index}-${condition.field}`}>
                <label className="field">事实维度
                  <Select aria-label={`法律适用事实维度 ${index + 1}`} value={condition.field || undefined} options={fieldOptions.map((item) => ({ ...item }))} onChange={(field: string) => updateCondition(index, { field, one_of: [] })} />
                </label>
                <label className="field">允许值
                  <Select aria-label={`法律适用事实取值 ${index + 1}`} mode="multiple" value={condition.one_of} options={valuesByField[condition.field] ?? []} disabled={!condition.field} onChange={(one_of: string[]) => updateCondition(index, { one_of })} />
                </label>
                <Button htmlType="button" onClick={() => setConditions((current) => current.filter((_, itemIndex) => itemIndex !== index))}>移除条件</Button>
              </div>
            ))}
            <Button htmlType="button" disabled={conditions.length >= 9} onClick={() => setConditions((current) => [...current, { field: "", one_of: [] }])}>添加适用条件</Button>
          </fieldset>
          <p className="meta">引用偏移按 Unicode 字符计数，提交后由服务端对照固定条文原文校验。</p>
          <Button type="primary" htmlType="button" disabled={disabled || !canAdd} onClick={addSelectedAuthority}>添加此法律依据</Button>
        </div>
      ) : null}
    </section>
  );
}
