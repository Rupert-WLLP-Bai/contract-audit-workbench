// File metadata (manifest mirror):
// Purpose: Checks the web behavior for “case legal knowledge”; representative assertions cover the reader shell default tab, URL-backed tab switching and deep links, the empty reader/search states, searches within the selected case, loading the exact article version in the applicability reader, keeping company sources read-only, restoring URL-backed draft values on browser history, and submitting the admin legal search from the URL-backed draft instead of querying each keystroke.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router";
import { setSessionIdentity } from "../api/http";
import { renderWithAntd } from "../test/antd";
import { LegalKnowledgeAdminList, LegalKnowledgePage } from "./legal-knowledge-page";

vi.mock("../components/case-legal-applicability-panel", () => ({
  CaseLegalApplicabilityPanel: ({ caseId }: { caseId: string }) => <div data-testid="legal-applicability-panel">法律适用事实 · {caseId}</div>,
}));

function fixture(name:string): unknown { return JSON.parse(readFileSync(resolve(process.cwd(),"../testdata/contracts/legal",name),"utf8")) as unknown; }
function json(value:unknown):Response { return new Response(JSON.stringify(value),{status:200,headers:{"Content-Type":"application/json"}}); }
const cases={items:[{id:"case-1",title:"已授权专线案件",document_count:0,pending_decisions:0,latest_run:null,archived_at:null}]};
afterEach(()=>{cleanup();setSessionIdentity(null);vi.unstubAllGlobals();});
function HistoryControls(){const navigate=useNavigate();return <><button onClick={()=>navigate(-1)}>Go back</button><button onClick={()=>navigate(1)}>Go forward</button></>;}
function LocationProbe(){const location=useLocation();return <output data-testid="location">{location.pathname}{location.search}</output>;}
function queryClient(){return new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}});}
function pageFetch(extra:(path:string)=>Response|undefined){
  const requests:string[]=[];
  vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
    const path=String(input); requests.push(path);
    if(path==="/api/cases") return json(cases);
    if(path==="/api/cases/case-1/company-policy-sources") return json(fixture("company-status.json"));
    return extra(path) ?? json({error:{code:"not_found",message:"没有找到"}});
  }));
  return requests;
}

describe("case legal knowledge",()=>{
  it("defaults to 适用依据, keeps tab choices in the URL and shows the empty reader and search states",async()=>{
    pageFetch(()=>undefined);
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge"]}><LocationProbe/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByRole("heading",{level:1,name:"案件适用依据"})).toBeInTheDocument();
    expect(screen.getByText("采购与履约依据核对")).toBeInTheDocument();
    expect(screen.getByRole("tab",{name:"适用依据"})).toHaveAttribute("aria-selected","true");
    expect(screen.getByText("尚未选择法律依据。请先在“引用条目”中检索并选择精确版本。")).toBeInTheDocument();
    expect(screen.queryByRole("button",{name:"查看来源详情"})).not.toBeInTheDocument();
    expect(screen.queryByTestId("legal-applicability-panel")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab",{name:"引用条目"}));
    await waitFor(()=>expect(screen.getByTestId("location")).toHaveTextContent("tab=references"));
    expect(screen.getByLabelText("名称或正文")).toBeInTheDocument();
    expect(screen.getByText("输入法规名称、正文关键词或条号后检索本案引用条目。")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab",{name:"适用事实"}));
    await waitFor(()=>expect(screen.getByTestId("location")).toHaveTextContent("tab=facts"));
    expect(screen.getByTestId("legal-applicability-panel")).toHaveTextContent("法律适用事实 · case-1");

    fireEvent.click(screen.getByRole("tab",{name:"适用依据"}));
    await waitFor(()=>expect(screen.getByTestId("location")).not.toHaveTextContent("tab="));
    client.clear();
  });

  it("honours a tab deep link before any interaction",async()=>{
    pageFetch(()=>undefined);
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?tab=facts"]}><LocationProbe/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByTestId("legal-applicability-panel")).toHaveTextContent("法律适用事实 · case-1");
    expect(screen.getByRole("tab",{name:"适用事实"})).toHaveAttribute("aria-selected","true");
    expect(screen.getByTestId("location")).toHaveTextContent("tab=facts");
    client.clear();
  });

  it("deep-links to an exact legal version in the applicability reader with source details",async()=>{
    const requests=pageFetch((path)=>{
      if(path.includes("/legal-knowledge/legal-wire-fixture-article-1/versions/")) return json(fixture("legal-version.json"));
      if(path==="/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1") return json(fixture("source-capture.json"));
      return undefined;
    });
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?tab=applicability&knowledge_id=legal-wire-fixture-article-1&version_id=1111111111111111111111111111111111111111111111111111111111111111"]}><LocationProbe/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("fixture-only body; not a statutory quotation")).toBeInTheDocument();
    expect(screen.getByRole("tab",{name:"适用依据"})).toHaveAttribute("aria-selected","true");
    expect(screen.getByTestId("location")).toHaveTextContent("tab=applicability");
    expect(await screen.findByRole("heading",{level:1,name:"案件适用依据"})).toBeInTheDocument();
    expect(screen.getByText("采购与履约依据核对")).toBeInTheDocument();
    expect(screen.getByRole("button",{name:"查看来源详情"})).toBeInTheDocument();
    expect(await screen.findByText("原始来源捕获")).toBeInTheDocument();
    expect(await screen.findByText("查看来源原始响应（Base64）")).toBeInTheDocument();
    expect(await screen.findByText(/未审核（NOT_RUN）/)).toBeInTheDocument();
    expect(requests.some((path)=>path.includes("/legal-knowledge/legal-wire-fixture-article-1/versions/"))).toBe(true);
    expect(requests).toContain("/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1");
    client.clear();
  });

  it("searches within the selected case, loads the exact article version and keeps company sources read-only",async()=>{
    vi.stubEnv("TZ", "America/Los_Angeles");
    const requests=pageFetch((path)=>{
      if(path.startsWith("/api/cases/case-1/legal-knowledge?") && path.includes("article_number=1")) return json(fixture("case-search.json"));
      if(path.includes("/legal-knowledge/legal-wire-fixture-article-1/versions/")) return json(fixture("legal-version.json"));
      if(path==="/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1") return json(fixture("source-capture.json"));
      return undefined;
    });
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?tab=references&q=fixture&article_number=1&business_at=2026-10-31T00%3A00%3A00Z"]}><LocationProbe/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("法律知识 wire fixture（非真实条文）")).toBeInTheDocument();
    expect(screen.getByText("公司制度资料待提供")).toBeInTheDocument();
    expect(screen.getByText(/当前不可引用/)).toBeInTheDocument();
    expect(screen.queryByText(/444444444444/)).not.toBeInTheDocument();
    expect(screen.getByLabelText("业务时间（北京时间）")).toHaveValue("2026-10-31T08:00");
    await waitFor(()=>expect(requests.some((path)=>path.includes("business_at=2026-10-31T00%3A00%3A00Z"))).toBe(true));
    expect(screen.getByRole("button",{name:"查看来源详情"})).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button",{name:"查看精确版本"}));
    expect(await screen.findByText("fixture-only body; not a statutory quotation")).toBeInTheDocument();
    expect(screen.getByRole("tab",{name:"适用依据"})).toHaveAttribute("aria-selected","true");
    await waitFor(()=>expect(screen.getByTestId("location")).not.toHaveTextContent("tab="));
    expect(screen.getByTestId("location")).toHaveTextContent("knowledge_id=legal-wire-fixture-article-1");
    expect(screen.getByText(/法律审阅/)).toBeInTheDocument();
    expect(await screen.findByText("查看来源原始响应（Base64）")).toBeInTheDocument();
    expect(screen.getByText("PGh0bWw+Zml4dHVyZS1vbmx5PC9odG1sPg==")).toBeInTheDocument();
    expect(await screen.findByText(/未审核（NOT_RUN）/)).toBeInTheDocument();
    expect(requests).toContain("/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1");
    expect(requests).toContain("/api/cases/case-1/company-policy-sources");
    client.clear();
  });

  it("keeps the URL as the committed query source and restores draft values on browser history", async () => {
    const requests:string[] = [];
    vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input); requests.push(path);
      if(path==="/api/cases") return json(cases);
      if(path==="/api/cases/case-1/company-policy-sources") return json(fixture("company-status.json"));
      if(path.startsWith("/api/cases/case-1/legal-knowledge?")) return json(fixture("case-search.json"));
      return json({error:{code:"not_found",message:"没有找到"}});
    }));
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?tab=references&q=fixture&article_number=1"]}><HistoryControls/><LocationProbe/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    await screen.findByText("法律知识 wire fixture（非真实条文）");
    const searchCount = requests.filter((path)=>path.includes("/legal-knowledge?")).length;
    fireEvent.change(screen.getByLabelText("名称或正文"),{target:{value:"draft only"}});
    await new Promise((resolve)=>setTimeout(resolve,20));
    expect(requests.filter((path)=>path.includes("/legal-knowledge?")).length).toBe(searchCount);
    fireEvent.click(screen.getByRole("button",{name:/检\s*索/}));
    await waitFor(()=>expect(requests.some((path)=>path.includes("q=draft+only"))).toBe(true));
    expect(screen.getByTestId("location")).toHaveTextContent("tab=references");
    fireEvent.click(screen.getByRole("button",{name:"Go back"}));
    await waitFor(()=>expect(screen.getByLabelText("名称或正文")).toHaveValue("fixture"));
    expect(requests.some((path)=>path.includes("q=fixture"))).toBe(true);
    client.clear();
  });

  it("shows an empty result state when the authorized search returns no legal citations", async () => {
    pageFetch((path)=>{
      if(path.startsWith("/api/cases/case-1/legal-knowledge?")) return json({items:[],next_cursor:null});
      return undefined;
    });
    const client=queryClient();
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?tab=references&q=missing"]}><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("没有匹配的法律条文")).toBeInTheDocument();
    expect(screen.getByText("检索到 0 条当前授权范围内的记录。")).toBeInTheDocument();
    expect(screen.queryByRole("button",{name:"查看来源详情"})).not.toBeInTheDocument();
    client.clear();
  });

  it("submits the admin legal search from the URL-backed draft instead of querying each keystroke", async () => {
    const requests: string[] = [];
    vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input); requests.push(path);
      if(path.startsWith("/api/admin/governed-knowledge")) return json(fixture("admin-search.json"));
      return json({error:{code:"not_found",message:"没有找到"}});
    }));
    const client=queryClient();
    renderWithAntd(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/admin/governed-knowledge?kind=legal"]}><LegalKnowledgeAdminList search="" onSearch={()=>{}} onSelectOtherKind={()=>{}} /></MemoryRouter></QueryClientProvider>);
    await screen.findByText("法律知识 wire fixture（非真实条文） · 条文 1（fixture）");
    const initialCount = requests.length;
    fireEvent.change(screen.getByLabelText("查找法律条文"),{target:{value:"合同法"}});
    await new Promise((resolve)=>setTimeout(resolve,20));
    expect(requests.length).toBe(initialCount);
    fireEvent.click(screen.getByRole("button",{name:/检\s*索/}));
    await waitFor(()=>expect(requests.some((path)=>path.includes("q=%E5%90%88%E5%90%8C%E6%B3%95"))).toBe(true));
    client.clear();
  });
});
