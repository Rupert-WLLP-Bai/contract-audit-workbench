// File metadata (manifest mirror):
// Purpose: Defines the admin route tree and lazy-loads settings, health, rule-pack and governed-knowledge pages behind the admin session gate.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-02
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AdminShell } from "./components/admin-shell";
import { PageStatus } from "./components/page-status";
import { LoginPage } from "./pages/login-page";
import { SessionGate } from "./pages/session-gate";

const SystemHealthPage = lazy(() =>
  import("./pages/system-health-page").then((module) => ({
    default: module.SystemHealthPage,
  })),
);
const TenantLifecyclePage = lazy(() =>
  import("./pages/tenant-lifecycle-page").then((module) => ({
    default: module.TenantLifecyclePage,
  })),
);
const JurisdictionOperationsPage = lazy(() =>
  import("./pages/jurisdiction-operations-page").then((module) => ({
    default: module.JurisdictionOperationsPage,
  })),
);
const SettingsPage = lazy(() =>
  import("./pages/settings-page").then((module) => ({
    default: module.SettingsPage,
  })),
);
const RulePacksPage = lazy(() =>
  import("./pages/rulepacks-page").then((module) => ({
    default: module.RulePacksPage,
  })),
);
const RulePackCreatePage = lazy(() =>
  import("./pages/rulepack-create-page").then((module) => ({
    default: module.RulePackCreatePage,
  })),
);
const RulePackDetailPage = lazy(() =>
  import("./pages/rulepack-detail-page").then((module) => ({
    default: module.RulePackDetailPage,
  })),
);
const GovernedKnowledgeListPage = lazy(() =>
  import("./pages/governed-knowledge-page").then((module) => ({
    default: module.GovernedKnowledgeListPage,
  })),
);
const GovernedKnowledgeCreatePage = lazy(() =>
  import("./pages/governed-knowledge-page").then((module) => ({
    default: module.GovernedKnowledgeCreatePage,
  })),
);
const GovernedKnowledgeDetailPage = lazy(() =>
  import("./pages/governed-knowledge-page").then((module) => ({
    default: module.GovernedKnowledgeDetailPage,
  })),
);
const PendingMembershipPage = lazy(() =>
  import("./pages/pending-membership-page").then((module) => ({
    default: module.PendingMembershipPage,
  })),
);

export function AdminApp() {
  return (
    <Suspense
      fallback={<PageStatus kind="loading" title="正在加载管理工作台" />}
    >
      <Routes>
        <Route path="/admin/login/membership" element={<PendingMembershipPage audience="admin" />} />
        <Route path="/admin/login" element={<LoginPage audience="admin" />} />
        <Route
          element={<SessionGate audience="admin" loginPath="/admin/login" />}
        >
          <Route element={<AdminShell />}>
            <Route index element={<Navigate to="/admin/settings" replace />} />
            <Route
              path="/admin"
              element={<Navigate to="/admin/settings" replace />}
            />
            <Route
              path="/admin/infrastructure"
              element={<SystemHealthPage audience="admin" />}
            />
            <Route
              path="/admin/settings"
              element={<SettingsPage audience="admin" />}
            />
            <Route path="/admin/tenant-lifecycle" element={<TenantLifecyclePage />} />
            <Route path="/admin/jurisdiction-operations" element={<JurisdictionOperationsPage />} />
            <Route path="/admin/rulepacks" element={<RulePacksPage />} />
            <Route
              path="/admin/rulepacks/new"
              element={<RulePackCreatePage />}
            />
            <Route
              path="/admin/rulepacks/:packId/versions/:version"
              element={<RulePackDetailPage />}
            />
            <Route path="/admin/governed-knowledge" element={<GovernedKnowledgeListPage />} />
            <Route path="/admin/governed-knowledge/new" element={<GovernedKnowledgeCreatePage />} />
            <Route path="/admin/governed-knowledge/:knowledgeId/versions/:versionId" element={<GovernedKnowledgeDetailPage />} />
            <Route
              path="*"
              element={<PageStatus kind="empty" title="没有这个管理页面" />}
            />
          </Route>
        </Route>
      </Routes>
    </Suspense>
  );
}
