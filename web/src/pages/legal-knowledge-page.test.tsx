import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router";
import { setSessionIdentity } from "../api/http";
import { LegalKnowledgeAdminList, LegalKnowledgePage } from "./legal-knowledge-page";

function fixture(name:string): unknown { return JSON.parse(readFileSync(resolve(process.cwd(),"../testdata/contracts/legal",name),"utf8")) as unknown; }
function json(value:unknown):Response { return new Response(JSON.stringify(value),{status:200,headers:{"Content-Type":"application/json"}}); }
const cases={items:[{id:"case-1",title:"已授权专线案件",document_count:0,pending_decisions:0,latest_run:null,archived_at:null}]};
afterEach(()=>{cleanup();setSessionIdentity(null);vi.unstubAllGlobals();});
function HistoryControls(){const navigate=useNavigate();return <><button onClick={()=>navigate(-1)}>Go back</button><button onClick={()=>navigate(1)}>Go forward</button></>;}

describe("case legal knowledge",()=>{
  it("searches within the selected case, loads the exact article version and keeps company sources read-only",async()=>{
    vi.stubEnv("TZ", "America/Los_Angeles");
    const requests:string[]=[];
    vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input); requests.push(path);
      if(path==="/api/cases") return json(cases);
      if(path.startsWith("/api/cases/case-1/legal-knowledge?") && path.includes("article_number=1")) return json(fixture("case-search.json"));
      if(path==="/api/cases/case-1/company-policy-sources") return json(fixture("company-status.json"));
      if(path.includes("/legal-knowledge/legal-wire-fixture-article-1/versions/")) return json(fixture("legal-version.json"));
      if(path==="/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1") return json(fixture("source-capture.json"));
      return json({error:{code:"not_found",message:"没有找到"}});
    }));
    const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}});
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?q=fixture&article_number=1&business_at=2026-10-31T00%3A00%3A00Z"]}><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    expect(await screen.findByText("法律知识 wire fixture（非真实条文）")).toBeInTheDocument();
    expect(screen.getByText("公司制度资料待提供")).toBeInTheDocument();
    expect(screen.getByText(/当前不可引用/)).toBeInTheDocument();
    expect(screen.queryByText(/444444444444/)).not.toBeInTheDocument();
    expect(screen.getByLabelText("业务时间（北京时间）")).toHaveValue("2026-10-31T08:00");
    await waitFor(()=>expect(requests.some((path)=>path.includes("business_at=2026-10-31T00%3A00%3A00Z"))).toBe(true));
    fireEvent.click(screen.getByRole("button",{name:"查看精确版本"}));
    expect(await screen.findByText("fixture-only body; not a statutory quotation")).toBeInTheDocument();
    expect(screen.getByText(/法律审阅/)).toBeInTheDocument();
    expect(await screen.findByText("查看来源原始响应（Base64）")).toBeInTheDocument();
    expect(screen.getByText("PGh0bWw+Zml4dHVyZS1vbmx5PC9odG1sPg==")).toBeInTheDocument();
    expect(await screen.findByText(/未审核（NOT_RUN）/)).toBeInTheDocument();
    expect(requests).toContain("/api/cases/case-1/legal-sources/snapshots/lsc_wire_fixture_1");
    expect(requests).toContain("/api/cases/case-1/company-policy-sources");
    client.clear();
  });

  it("keeps the URL as the committed query source and restores draft values on browser history", async () => {
    const requests: string[] = [];
    vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input); requests.push(path);
      if(path==="/api/cases") return json(cases);
      if(path.startsWith("/api/cases/case-1/legal-knowledge?")) return json(fixture("case-search.json"));
      if(path==="/api/cases/case-1/company-policy-sources") return json(fixture("company-status.json"));
      return json({error:{code:"not_found",message:"没有找到"}});
    }));
    const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}});
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/cases/case-1/legal-knowledge?q=fixture&article_number=1"]}><HistoryControls/><Routes><Route path="/cases/:caseId/legal-knowledge" element={<LegalKnowledgePage/>}/></Routes></MemoryRouter></QueryClientProvider>);
    await screen.findByText("法律知识 wire fixture（非真实条文）");
    const searchCount = requests.filter((path)=>path.includes("/legal-knowledge?")).length;
    fireEvent.change(screen.getByLabelText("名称或正文"),{target:{value:"draft only"}});
    await new Promise((resolve)=>setTimeout(resolve,20));
    expect(requests.filter((path)=>path.includes("/legal-knowledge?")).length).toBe(searchCount);
    fireEvent.click(screen.getByRole("button",{name:/检\s*索/}));
    await waitFor(()=>expect(requests.some((path)=>path.includes("q=draft+only"))).toBe(true));
    fireEvent.click(screen.getByRole("button",{name:"Go back"}));
    await waitFor(()=>expect(screen.getByLabelText("名称或正文")).toHaveValue("fixture"));
    expect(requests.some((path)=>path.includes("q=fixture"))).toBe(true);
    client.clear();
  });

  it("submits the admin legal search from the URL-backed draft instead of querying each keystroke", async () => {
    const requests: string[] = [];
    vi.stubGlobal("fetch",vi.fn(async(input:RequestInfo|URL)=>{
      const path=String(input); requests.push(path);
      if(path.startsWith("/api/admin/governed-knowledge")) return json(fixture("admin-search.json"));
      return json({error:{code:"not_found",message:"没有找到"}});
    }));
    const client=new QueryClient({defaultOptions:{queries:{retry:false,gcTime:0}}});
    render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/admin/governed-knowledge?kind=legal"]}><LegalKnowledgeAdminList search="" onSearch={()=>{}} onSelectOtherKind={()=>{}} /></MemoryRouter></QueryClientProvider>);
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
