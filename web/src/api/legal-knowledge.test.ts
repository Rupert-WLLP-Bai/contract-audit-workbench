import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getCompanyPolicySourceStatus, getLegalKnowledgeForCase, getLegalSourceCaptureForCase, importLegalSource, parseCompanySourceStatusList, parseLegalKnowledgeVersion, parseLegalSearchPage, parseLegalSourceCapture, searchAdminLegalKnowledge, searchLegalKnowledgeForCase, type LegalCaptureInput } from "./legal-knowledge";
import { setSessionIdentity } from "./http";

function fixture(name: string): unknown {
  return JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/contracts/legal", name), "utf8")) as unknown;
}
function json(value: unknown, status = 200): Response { return new Response(JSON.stringify(value), {status,headers:{"Content-Type":"application/json"}}); }
afterEach(() => { vi.unstubAllGlobals(); setSessionIdentity(null); });

describe("legal knowledge API", () => {
  it("parses the Go wire fixtures and keeps legal knowledge review explicitly unrun", () => {
    const admin = parseLegalSearchPage(fixture("admin-search.json"));
    const client = parseLegalSearchPage(fixture("case-search.json"));
    const version = parseLegalKnowledgeVersion(fixture("legal-version.json"));
    const company = parseCompanySourceStatusList(fixture("company-status.json"));
    const capture = parseLegalSourceCapture(fixture("source-capture.json"));
    expect(admin.items[0]?.legal_metadata).toMatchObject({ source_snapshot_id:"lsc_wire_fixture_1", legal_review_status:"NOT_RUN", reviewer:null });
    expect(client.items[0]?.applicability).toBe("unknown");
    expect(version.content).toMatchObject({kind:"legal",binding:{pack_id:"",rule_ids:null,source_spans:null}});
    expect(version.content.legal_metadata?.source_sha256).toHaveLength(64);
    expect(company.items[0]).toMatchObject({has_body:false,has_published_version:false,has_content_hash:false,knowledge_ref:null,can_be_cited:false});
    expect(admin.items[0]?.legal_metadata.legal_review_status).toBe("NOT_RUN");
    expect(capture).toMatchObject({capture_snapshot_id:"lsc_wire_fixture_1",raw_sha256:"4".repeat(64),status_response_sha256:"5".repeat(64)});
    expect(() => parseCompanySourceStatusList({items:[{...company.items[0],can_be_cited:true}]})).toThrow("待提供状态包含了可引用资料");
    expect(() => parseLegalKnowledgeVersion({...fixture("legal-version.json") as object,ref:{knowledge_id:"other",version_id:"bad",content_sha256:"bad"}})).toThrow("法律版本引用与正文摘要不一致");
  });

  it("uses case context, sends exact article and business-time filters, and loads the company status read-only", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(json(fixture("case-search.json"))).mockResolvedValueOnce(json(fixture("company-status.json")));
    vi.stubGlobal("fetch",fetchMock);
    const result = await searchLegalKnowledgeForCase("case/one",{q:"合同",article_number:1260,business_at:"2026-10-31T08:00:00+08:00"});
    await getCompanyPolicySourceStatus("case/one");
    expect(result.items).toHaveLength(1);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/cases/case%2Fone/legal-knowledge?q=%E5%90%88%E5%90%8C&article_number=1260&business_at=2026-10-31T08%3A00%3A00%2B08%3A00&limit=50");
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/cases/case%2Fone/company-policy-sources");
  });

  it("dispatches admin queries through existing governance with kind=legal and imports with stable idempotency headers", async () => {
    const capture: LegalCaptureInput = { source_id:"source-1",source_snapshot_id:"snapshot-1",version_id:"v1",title:"测试法规",official_url:"https://example.invalid",content_type:"text/html",raw_bytes:"PGh0bWw+PC9odG1sPg==",raw_sha256:"a".repeat(64),source_captured_at:"2026-10-31T00:00:00Z",promulgated_on:"2020-01-01",effective_from:"2020-02-01",currency_status:"effective",status_evidence_id:"status-1",status_observed_at:"2026-10-31T00:00:00Z",status_evidence_raw:"e30=",status_evidence_sha256:"c".repeat(64),articles:[] };
    const result = { capture_snapshot_id:"lsc-1",source_id:"source-1",norm_version:"v1",raw_sha256:"a".repeat(64),extracted_full_text_sha256:"b".repeat(64),status_evidence_sha256:"c".repeat(64),provenance:"verified_official_capture",articles:[{knowledge_id:"legal-1",version_id:"d".repeat(64),content_sha256:"b".repeat(64)}] };
    const fetchMock = vi.fn().mockResolvedValueOnce(json(fixture("admin-search.json"))).mockResolvedValueOnce(json(result,201));
    vi.stubGlobal("fetch",fetchMock);
    await searchAdminLegalKnowledge({q:"1260"});
    await importLegalSource({capture,articles:[{article_number:1,knowledge_id:"legal-1",display_version:"v1"}]},"stable-import-key");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/admin/governed-knowledge?q=1260&limit=50&kind=legal");
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get("Idempotency-Key")).toBe("stable-import-key");
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toMatchObject({capture,articles:[{article_number:1,knowledge_id:"legal-1",display_version:"v1"}],idempotency_key:"stable-import-key"});
  });

  it("reads an exact published version and its pinned raw source capture in case context", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(json(fixture("legal-version.json")))
      .mockResolvedValueOnce(json(fixture("source-capture.json")));
    vi.stubGlobal("fetch", fetchMock);
    const version = await getLegalKnowledgeForCase("case/one", "legal-one", "version/one");
    const capture = await getLegalSourceCaptureForCase("case/one", "lsc_wire_fixture_1");
    expect(version.content.kind).toBe("legal");
    expect(capture.raw_response).toBe("PGh0bWw+Zml4dHVyZS1vbmx5PC9odG1sPg==");
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/cases/case%2Fone/legal-knowledge/legal-one/versions/version%2Fone");
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/cases/case%2Fone/legal-sources/snapshots/lsc_wire_fixture_1");
  });
});
