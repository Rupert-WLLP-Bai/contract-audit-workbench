import { lazy, Suspense } from "react";
import { Route, Routes, useLocation } from "react-router";
import { AppShell } from "./components/app-shell";
import { PageStatus } from "./components/page-status";
import { SessionGate } from "./pages/session-gate";
const AdminApp = lazy(() => import("./admin-app").then((module) => ({ default: module.AdminApp })));

const HomePage = lazy(() => import("./pages/home-page").then((module) => ({ default: module.HomePage })));
const ApiDocsPage = lazy(() => import("./pages/api-docs-page"));
const CaseDetailPage = lazy(() => import("./pages/case-detail-page").then((module) => ({ default: module.CaseDetailPage })));
const CasesPage = lazy(() => import("./pages/cases-page").then((module) => ({ default: module.CasesPage })));
const LoginPage = lazy(() => import("./pages/login-page").then((module) => ({ default: module.LoginPage })));
const NotFoundPage = lazy(() => import("./pages/not-found-page").then((module) => ({ default: module.NotFoundPage })));
const ReviewPage = lazy(() => import("./pages/review-page").then((module) => ({ default: module.ReviewPage })));
const ComparePage = lazy(() => import("./pages/compare-page").then((module) => ({ default: module.ComparePage })));
const ReviewEntryPage = lazy(() => import("./pages/review-entry-page").then((module) => ({ default: module.ReviewEntryPage })));
const WorkbenchModulePage = lazy(() => import("./pages/workbench-module-page").then((module) => ({ default: module.WorkbenchModulePage })));

export function App() {
  const pathname = useLocation().pathname;
  // Admin owns an absolute route tree; do not add a second /admin route base.
  if (pathname === "/admin" || pathname.startsWith("/admin/")) {
    return <Suspense fallback={<PageStatus kind="loading" title="正在加载管理工作台" />}><AdminApp /></Suspense>;
  }
  return (
    <Suspense fallback={<PageStatus kind="loading" title="正在加载页面" />}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route element={<SessionGate />}>
          <Route element={<AppShell />}>
            <Route index element={<HomePage />} />
            <Route path="/review" element={<ReviewEntryPage />} />
            <Route path="/cases" element={<CasesPage />} />
            <Route path="/cases/:caseId" element={<CaseDetailPage />} />
            <Route path="/cases/:caseId/reviews/:runId" element={<ReviewPage />} />
            <Route path="/compare" element={<ComparePage />} />
            <Route path="/workbench/:module" element={<WorkbenchModulePage />} />
            <Route path="/api-docs" element={<ApiDocsPage />} />
            <Route path="*" element={<NotFoundPage />} />
          </Route>
        </Route>
      </Routes>
    </Suspense>
  );
}
