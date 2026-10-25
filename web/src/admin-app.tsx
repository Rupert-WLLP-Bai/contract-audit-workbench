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

export function AdminApp() {
  return (
    <Suspense
      fallback={<PageStatus kind="loading" title="正在加载管理工作台" />}
    >
      <Routes>
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
            <Route path="/admin/rulepacks" element={<RulePacksPage />} />
            <Route
              path="/admin/rulepacks/new"
              element={<RulePackCreatePage />}
            />
            <Route
              path="/admin/rulepacks/:packId/versions/:version"
              element={<RulePackDetailPage />}
            />
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
