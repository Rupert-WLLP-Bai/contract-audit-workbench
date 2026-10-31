import { jsonInit, requestJson } from "./http";
import { responseReader } from "./respond";
import { parseKnowledgeVersion, type KnowledgeVersion } from "./governed-knowledge";

export interface LegalRef { knowledge_id: string; version_id: string; content_sha256: string }
export interface LegalMetadata {
  source_id: string; norm_title: string; document_type: string; norm_version: string;
  article_number: number; article_label: string; issuer: string; publisher: string;
  official_url: string; promulgated_on: string; effective_from: string; effective_until: string | null;
  official_status_observation_id: string; official_status: string; status_registry_url: string;
  status_observed_at: string; status_evidence_sha256: string; status_response_status: number;
  status_content_type: string; source_snapshot_id: string; source_provenance: string;
  source_captured_at: string; source_response_status: number; source_content_type: string;
  source_sha256: string; norm_text_sha256: string; canonical_text_version: string;
  source_text_start: number; source_text_end: number; extractor_id: string; extractor_version: string;
  text_check_status: string; legal_review_status: string; reviewer: string | null;
}
export interface LegalHit { ref: LegalRef; title: string; body: string; snippet: string; summary: string; governance_status: string; active: boolean; legal_metadata: LegalMetadata; applicability: string }
export interface LegalSearchPage { items: LegalHit[]; next_cursor: string | null }
export interface CompanySourceStatus { catalog_key: string; display_label: string; state: string; requested_owner: string | null; has_body: boolean; has_published_version: boolean; has_content_hash: boolean; knowledge_ref: LegalRef | null; can_be_cited: boolean; satisfies_mandatory_dependencies: boolean }
export interface CompanySourceStatusList { items: CompanySourceStatus[] }
export interface LegalCaptureArticleRef { source_id: string; source_snapshot_id: string; version_id: string; article_number: number; article_label: string; full_text: string; text_sha256: string; citation_span?: { start: number; end: number; quote: string }; promulgated_on: string; effective_from: string; effective_until?: string; currency_status: string; status_evidence_id: string }
export interface LegalCaptureInput {
  source_id: string; source_snapshot_id: string; version_id: string; title: string; official_url: string; content_type: string;
  raw_bytes: string; raw_sha256: string; source_captured_at: string; promulgated_on: string; effective_from: string; effective_until?: string;
  currency_status: string; status_evidence_id: string; status_observed_at: string; status_evidence_raw: string;
  status_evidence_sha256: string; articles: LegalCaptureArticleRef[];
}
export interface LegalImportInput { capture: LegalCaptureInput; articles: { article_number: number; knowledge_id: string; display_version: string }[] }
export interface LegalImportResult { capture_snapshot_id: string; source_id: string; norm_version: string; raw_sha256: string; extracted_full_text_sha256: string; status_evidence_sha256: string; provenance: string; articles: LegalRef[] }
export interface LegalCaptureArticle {
  ref: {
    source_id: string; source_snapshot_id: string; version_id: string; article_number: number;
    article_label: string; full_text: string; text_sha256: string;
    citation_span: { start: number; end: number; quote: string };
    promulgated_on: string; effective_from: string; currency_status: string; status_evidence_id: string;
  };
  full_text: string; text_sha256: string; start: number; end: number;
}
export interface LegalSourceCapture {
  capture_snapshot_id: string; source_id: string; norm_version: string; norm_title: string;
  document_type: string; publisher: string; issuer: string; official_url: string; provenance: string;
  promulgated_on: string; effective_from: string; source_captured_at: string; response_status: number;
  media_type: string; raw_response: string; raw_sha256: string; extracted_full_text: string;
  extracted_full_text_sha256: string; extractor_id: string; extractor_version: string;
  canonicalization_version: string; status_observation_id: string; observed_status: string;
  status_registry_url: string; status_request_method: string; status_observed_at: string;
  status_response_status: number; status_media_type: string; status_response: string;
  status_response_sha256: string; status_mapping_evidence: Record<string, unknown>;
  articles: LegalCaptureArticle[]; imported_articles: LegalRef[]; request_digest: string;
  created_by: string; created_at: string;
}

const { object, text, integer, bool, array, nullableText } = responseReader((path) => `法律知识响应中的 ${path} 无法识别`);
function date(value: unknown, path: string): string {
  const result = text(value, path);
  const parsed = new Date(`${result}T00:00:00Z`);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(result) || Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== result) throw new Error(`法律知识响应中的 ${path} 不是有效日期`);
  return result;
}
function instant(value: unknown, path: string): string {
  const result = text(value, path);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(result) || Number.isNaN(Date.parse(result))) throw new Error(`法律知识响应中的 ${path} 不是UTC时间`);
  date(result.slice(0, 10), path);
  return result;
}
function sha256(value: unknown, path: string): string {
  const result = text(value, path);
  if (!/^[a-f0-9]{64}$/i.test(result)) throw new Error(`法律知识响应中的 ${path} 不是SHA-256摘要`);
  return result;
}
function ref(value: unknown, path: string): LegalRef { const v = object(value,path); return { knowledge_id:text(v.knowledge_id,`${path}.knowledge_id`), version_id:text(v.version_id,`${path}.version_id`), content_sha256:text(v.content_sha256,`${path}.content_sha256`) }; }
export function parseLegalMetadata(value: unknown, path = "legal_metadata"): LegalMetadata {
  const v=object(value,path);
  const officialURL = text(v.official_url,`${path}.official_url`);
  const statusURL = text(v.status_registry_url,`${path}.status_registry_url`);
  if (!/^https?:\/\//.test(officialURL) || !/^https?:\/\//.test(statusURL)) throw new Error(`法律知识响应中的 ${path} 来源链接无效`);
  const from = date(v.effective_from,`${path}.effective_from`);
  const until = v.effective_until === undefined ? null : nullableText(v.effective_until,`${path}.effective_until`);
  if (until !== null) date(until,`${path}.effective_until`);
  const sourceTextStart=integer(v.source_text_start,`${path}.source_text_start`,0);
  const sourceTextEnd=integer(v.source_text_end,`${path}.source_text_end`,0);
  const legalReviewStatus=text(v.legal_review_status,`${path}.legal_review_status`);
  const reviewer=nullableText(v.reviewer,`${path}.reviewer`);
  if (sourceTextEnd < sourceTextStart) throw new Error(`法律知识响应中的 ${path} 正文跨度无效`);
  if (legalReviewStatus !== "NOT_RUN" || reviewer !== null) throw new Error(`法律知识响应中的 ${path} 法律审阅状态必须保持为 NOT_RUN 且审阅人为空`);
  return { source_id:text(v.source_id,`${path}.source_id`), norm_title:text(v.norm_title,`${path}.norm_title`), document_type:text(v.document_type,`${path}.document_type`), norm_version:text(v.norm_version,`${path}.norm_version`), article_number:integer(v.article_number,`${path}.article_number`,1), article_label:text(v.article_label,`${path}.article_label`), issuer:text(v.issuer,`${path}.issuer`), publisher:text(v.publisher,`${path}.publisher`), official_url:officialURL, promulgated_on:date(v.promulgated_on,`${path}.promulgated_on`), effective_from:from, effective_until:until, official_status_observation_id:text(v.official_status_observation_id,`${path}.official_status_observation_id`), official_status:text(v.official_status,`${path}.official_status`), status_registry_url:statusURL, status_observed_at:instant(v.status_observed_at,`${path}.status_observed_at`), status_evidence_sha256:sha256(v.status_evidence_sha256,`${path}.status_evidence_sha256`), status_response_status:integer(v.status_response_status,`${path}.status_response_status`,100), status_content_type:text(v.status_content_type,`${path}.status_content_type`), source_snapshot_id:text(v.source_snapshot_id,`${path}.source_snapshot_id`), source_provenance:text(v.source_provenance,`${path}.source_provenance`), source_captured_at:instant(v.source_captured_at,`${path}.source_captured_at`), source_response_status:integer(v.source_response_status,`${path}.source_response_status`,100), source_content_type:text(v.source_content_type,`${path}.source_content_type`), source_sha256:sha256(v.source_sha256,`${path}.source_sha256`), norm_text_sha256:sha256(v.norm_text_sha256,`${path}.norm_text_sha256`), canonical_text_version:text(v.canonical_text_version,`${path}.canonical_text_version`), source_text_start:sourceTextStart, source_text_end:sourceTextEnd, extractor_id:text(v.extractor_id,`${path}.extractor_id`), extractor_version:text(v.extractor_version,`${path}.extractor_version`), text_check_status:text(v.text_check_status,`${path}.text_check_status`), legal_review_status:legalReviewStatus, reviewer };
}
export function parseLegalKnowledgeVersion(value: unknown): KnowledgeVersion {
  const version = parseKnowledgeVersion(value);
  if (version.content.kind !== "legal" || !version.content.legal_metadata) throw new Error("服务返回的版本不是完整法律规范版本");
  if (version.ref.knowledge_id !== version.content.knowledge_id || version.ref.content_sha256 !== version.content.legal_metadata.norm_text_sha256) {
    throw new Error("法律版本引用与正文摘要不一致");
  }
  return version;
}
function hit(value: unknown, path: string): LegalHit { const v=object(value,path); return { ref:ref(v.ref,`${path}.ref`), title:text(v.title,`${path}.title`), body:text(v.body,`${path}.body`), snippet:v.snippet === undefined ? "" : text(v.snippet,`${path}.snippet`), summary:text(v.summary,`${path}.summary`), governance_status:text(v.governance_status,`${path}.governance_status`), active:bool(v.active,`${path}.active`), legal_metadata:parseLegalMetadata(v.legal_metadata,`${path}.legal_metadata`), applicability:text(v.applicability,`${path}.applicability`) }; }
export function parseLegalSearchPage(value: unknown): LegalSearchPage { const v=object(value,"page"); return { items:array(v.items,"page.items",hit), next_cursor:v.next_cursor === undefined ? null : nullableText(v.next_cursor,"page.next_cursor") }; }
function companyStatus(value: unknown, path: string): CompanySourceStatus {
  const v=object(value,path);
  const result: CompanySourceStatus={catalog_key:text(v.catalog_key,`${path}.catalog_key`),display_label:text(v.display_label,`${path}.display_label`),state:text(v.state,`${path}.state`),requested_owner:nullableText(v.requested_owner,`${path}.requested_owner`),has_body:bool(v.has_body,`${path}.has_body`),has_published_version:bool(v.has_published_version,`${path}.has_published_version`),has_content_hash:bool(v.has_content_hash,`${path}.has_content_hash`),knowledge_ref:v.knowledge_ref===null?null:ref(v.knowledge_ref,`${path}.knowledge_ref`),can_be_cited:bool(v.can_be_cited,`${path}.can_be_cited`),satisfies_mandatory_dependencies:bool(v.satisfies_mandatory_dependencies,`${path}.satisfies_mandatory_dependencies`)};
  if(result.state==="awaiting_source"&&(result.has_body||result.has_published_version||result.has_content_hash||result.knowledge_ref!==null||result.can_be_cited||result.satisfies_mandatory_dependencies)) throw new Error(`法律知识响应中的 ${path} 待提供状态包含了可引用资料`);
  if(result.can_be_cited&&(!result.has_body||!result.has_published_version||!result.has_content_hash||result.knowledge_ref===null)) throw new Error(`法律知识响应中的 ${path} 可引用状态缺少正文、版本、摘要或精确引用`);
  return result;
}
export function parseCompanySourceStatusList(value: unknown): CompanySourceStatusList { const v=object(value,"company_sources"); return { items:array(v.items,"company_sources.items",companyStatus) }; }
export function parseLegalImportResult(value: unknown): LegalImportResult { const v=object(value,"import"); return { capture_snapshot_id:text(v.capture_snapshot_id,"import.capture_snapshot_id"), source_id:text(v.source_id,"import.source_id"), norm_version:text(v.norm_version,"import.norm_version"), raw_sha256:text(v.raw_sha256,"import.raw_sha256"), extracted_full_text_sha256:text(v.extracted_full_text_sha256,"import.extracted_full_text_sha256"), status_evidence_sha256:text(v.status_evidence_sha256,"import.status_evidence_sha256"), provenance:text(v.provenance,"import.provenance"), articles:array(v.articles,"import.articles",(entry,p)=>ref(entry,p)) }; }
function captureArticle(value: unknown, path: string): LegalCaptureArticle {
  const v=object(value,path); const r=object(v.ref,`${path}.ref`); const span=object(r.citation_span,`${path}.ref.citation_span`);
  return { ref:{source_id:text(r.source_id,`${path}.ref.source_id`),source_snapshot_id:text(r.source_snapshot_id,`${path}.ref.source_snapshot_id`),version_id:text(r.version_id,`${path}.ref.version_id`),article_number:integer(r.article_number,`${path}.ref.article_number`,1),article_label:text(r.article_label,`${path}.ref.article_label`),full_text:text(r.full_text,`${path}.ref.full_text`),text_sha256:text(r.text_sha256,`${path}.ref.text_sha256`),citation_span:{start:integer(span.start,`${path}.ref.citation_span.start`,0),end:integer(span.end,`${path}.ref.citation_span.end`,0),quote:text(span.quote,`${path}.ref.citation_span.quote`)},promulgated_on:date(r.promulgated_on,`${path}.ref.promulgated_on`),effective_from:date(r.effective_from,`${path}.ref.effective_from`),currency_status:text(r.currency_status,`${path}.ref.currency_status`),status_evidence_id:text(r.status_evidence_id,`${path}.ref.status_evidence_id`)},full_text:text(v.full_text,`${path}.full_text`),text_sha256:sha256(v.text_sha256,`${path}.text_sha256`),start:integer(v.start,`${path}.start`,0),end:integer(v.end,`${path}.end`,0)};
}
export function parseLegalSourceCapture(value: unknown): LegalSourceCapture {
  const v=object(value,"capture");
  const mapping=object(v.status_mapping_evidence,"capture.status_mapping_evidence");
  return {capture_snapshot_id:text(v.capture_snapshot_id,"capture.capture_snapshot_id"),source_id:text(v.source_id,"capture.source_id"),norm_version:text(v.norm_version,"capture.norm_version"),norm_title:text(v.norm_title,"capture.norm_title"),document_type:text(v.document_type,"capture.document_type"),publisher:text(v.publisher,"capture.publisher"),issuer:text(v.issuer,"capture.issuer"),official_url:text(v.official_url,"capture.official_url"),provenance:text(v.provenance,"capture.provenance"),promulgated_on:date(v.promulgated_on,"capture.promulgated_on"),effective_from:date(v.effective_from,"capture.effective_from"),source_captured_at:instant(v.source_captured_at,"capture.source_captured_at"),response_status:integer(v.response_status,"capture.response_status",100),media_type:text(v.media_type,"capture.media_type"),raw_response:text(v.raw_response,"capture.raw_response"),raw_sha256:sha256(v.raw_sha256,"capture.raw_sha256"),extracted_full_text:text(v.extracted_full_text,"capture.extracted_full_text"),extracted_full_text_sha256:sha256(v.extracted_full_text_sha256,"capture.extracted_full_text_sha256"),extractor_id:text(v.extractor_id,"capture.extractor_id"),extractor_version:text(v.extractor_version,"capture.extractor_version"),canonicalization_version:text(v.canonicalization_version,"capture.canonicalization_version"),status_observation_id:text(v.status_observation_id,"capture.status_observation_id"),observed_status:text(v.observed_status,"capture.observed_status"),status_registry_url:text(v.status_registry_url,"capture.status_registry_url"),status_request_method:text(v.status_request_method,"capture.status_request_method"),status_observed_at:instant(v.status_observed_at,"capture.status_observed_at"),status_response_status:integer(v.status_response_status,"capture.status_response_status",100),status_media_type:text(v.status_media_type,"capture.status_media_type"),status_response:text(v.status_response,"capture.status_response"),status_response_sha256:sha256(v.status_response_sha256,"capture.status_response_sha256"),status_mapping_evidence:mapping,articles:array(v.articles,"capture.articles",captureArticle),imported_articles:array(v.imported_articles,"capture.imported_articles",(entry,path)=>ref(entry,path)),request_digest:sha256(v.request_digest,"capture.request_digest"),created_by:text(v.created_by,"capture.created_by"),created_at:instant(v.created_at,"capture.created_at")};
}

function legalSearchPath(base: string, query: { q?: string; article_number?: number; source_id?: string; business_at?: string; cursor?: string | null }): string {
  const params=new URLSearchParams();
  if(query.q) params.set("q",query.q);
  if(query.article_number!==undefined) params.set("article_number",String(query.article_number));
  if(query.source_id) params.set("source_id",query.source_id);
  if(query.business_at) params.set("business_at",query.business_at);
  if(query.cursor) params.set("cursor",query.cursor);
  params.set("limit","50");
  return `${base}?${params}`;
}
export function searchLegalKnowledgeForCase(caseId: string, query: Parameters<typeof legalSearchPath>[1], signal?: AbortSignal) { return requestJson(legalSearchPath(`/api/cases/${encodeURIComponent(caseId)}/legal-knowledge`,query),{method:"GET",signal},parseLegalSearchPage); }
export function getLegalKnowledgeForCase(caseId: string, knowledgeId: string, versionId: string, signal?: AbortSignal) { return requestJson(`/api/cases/${encodeURIComponent(caseId)}/legal-knowledge/${encodeURIComponent(knowledgeId)}/versions/${encodeURIComponent(versionId)}`,{method:"GET",signal},parseLegalKnowledgeVersion); }
export function getLegalSourceCaptureForCase(caseId: string, snapshotId: string, signal?: AbortSignal) { return requestJson(`/api/cases/${encodeURIComponent(caseId)}/legal-sources/snapshots/${encodeURIComponent(snapshotId)}`,{method:"GET",signal},parseLegalSourceCapture); }
export function getCompanyPolicySourceStatus(caseId: string, signal?: AbortSignal) { return requestJson(`/api/cases/${encodeURIComponent(caseId)}/company-policy-sources`,{method:"GET",signal},parseCompanySourceStatusList); }
export function searchAdminLegalKnowledge(query: Parameters<typeof legalSearchPath>[1], signal?: AbortSignal) { const path=legalSearchPath("/api/admin/governed-knowledge",query); return requestJson(`${path}&kind=legal`,{method:"GET",signal},parseLegalSearchPage); }
export function importLegalSource(input: LegalImportInput, idempotencyKey: string) { return requestJson("/api/admin/legal-sources/imports",jsonInit("POST",{...input,idempotency_key:idempotencyKey},{"Idempotency-Key":idempotencyKey}),parseLegalImportResult); }
