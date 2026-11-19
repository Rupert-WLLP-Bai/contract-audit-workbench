// File metadata (manifest mirror):
// Purpose: Mounts the web application into the root DOM element and configures its Ant Design theme/tokens and API session identity before React renders routes.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-25
// Updated: 2026-11-19
// Revision: r7
// Copyright: See LICENSE
// End file metadata.

import { App as AntdApp, ConfigProvider, type ThemeConfig } from "antd";
import { StyleProvider } from "@ant-design/cssinjs";
import zhCN from "antd/locale/zh_CN";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { invalidateSessionIdentity, queryRetryDelay, shouldRetryQuery } from "./api/http";
import { clearIdentityQueries } from "./api/queries";
import { installContextInvalidation } from "./api/context-sync";
import { clearAllEvidenceDrafts } from "./features/review/evidence-request-drafts";
import { ApplicationError } from "./components/application-error";

const rootStyle = getComputedStyle(document.documentElement);
const cssToken = (name: string) => rootStyle.getPropertyValue(name).trim();
const uiTheme: ThemeConfig = {
  token: {
    colorPrimary: cssToken("--action-primary"),
    colorPrimaryHover: cssToken("--action-primary-hover"),
    colorPrimaryActive: cssToken("--action-primary-active"),
    colorPrimaryBg: cssToken("--action-primary-soft"),
    colorInfo: cssToken("--signal-info"),
    colorInfoText: cssToken("--state-progress"),
    colorSuccess: cssToken("--signal-success"),
    colorSuccessText: cssToken("--state-compatible"),
    colorWarning: cssToken("--signal-warning"),
    colorWarningText: cssToken("--state-insufficient"),
    colorError: cssToken("--signal-error"),
    colorErrorText: cssToken("--state-conflict"),
    colorText: cssToken("--text-primary"),
    colorTextSecondary: cssToken("--text-secondary"),
    colorTextPlaceholder: cssToken("--text-placeholder"),
    colorBorder: cssToken("--border-control"),
    colorBorderSecondary: cssToken("--border-default"),
    colorBgLayout: cssToken("--surface-page"),
    colorBgContainer: cssToken("--surface-raised"),
    colorBgElevated: cssToken("--surface-raised"),
    borderRadius: Number.parseFloat(cssToken("--radius-control")),
    fontFamily: cssToken("--font-ui"),
    fontSize: Number.parseFloat(cssToken("--font-size-ui")),
    controlHeight: Number.parseFloat(cssToken("--control-height")),
    motion: !window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  },
  components: {
    Button: {
      primaryColor: cssToken("--button-primary-fg"),
      primaryShadow: "none",
      defaultShadow: "none",
      textHoverBg: cssToken("--surface-hover"),
      fontWeight: 500,
    },
    Layout: {
      bodyBg: cssToken("--surface-page"),
      headerBg: cssToken("--surface-rail"),
      headerColor: cssToken("--text-primary"),
      lightSiderBg: cssToken("--surface-rail"),
      lightTriggerBg: cssToken("--surface-rail-raised"),
      lightTriggerColor: cssToken("--text-secondary"),
    },
    Menu: {
      itemBg: cssToken("--surface-rail"),
      collapsedWidth: 72,
      itemHeight: 40,
      popupBg: cssToken("--surface-raised"),
      subMenuItemBg: cssToken("--surface-rail"),
      itemColor: cssToken("--text-secondary"),
      groupTitleColor: cssToken("--text-secondary"),
      itemSelectedBg: cssToken("--nav-item-selected"),
      itemSelectedColor: cssToken("--nav-item-selected-fg"),
      itemHoverBg: cssToken("--surface-rail-raised"),
      itemHoverColor: cssToken("--text-primary"),
      itemBorderRadius: Number.parseFloat(cssToken("--radius-control")),
      groupTitleFontSize: 13,
    },
    Form: {
      labelColor: cssToken("--text-primary"),
      labelFontSize: Number.parseFloat(cssToken("--font-size-ui")),
      itemMarginBottom: Number.parseFloat(cssToken("--space-4")),
    },
    Input: {
      activeBorderColor: cssToken("--focus-ring"),
    },
    Select: {
      activeBorderColor: cssToken("--focus-ring"),
      optionActiveBg: cssToken("--surface-hover"),
      optionSelectedBg: cssToken("--action-primary-soft"),
    },
    Table: {
      headerBg: cssToken("--surface-subtle"),
      borderColor: cssToken("--border-default"),
      cellPaddingBlock: Number.parseFloat(cssToken("--space-3")),
      cellPaddingInline: Number.parseFloat(cssToken("--space-4")),
      headerColor: cssToken("--text-secondary"),
      rowHoverBg: cssToken("--surface-hover"),
      rowSelectedBg: cssToken("--table-row-selected"),
      rowSelectedHoverBg: cssToken("--table-row-selected"),
    },
    Tag: {
      colorInfo: cssToken("--state-progress"),
      colorSuccess: cssToken("--state-compatible"),
      colorWarning: cssToken("--state-insufficient"),
      colorError: cssToken("--state-conflict"),
    },
  },
};

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: shouldRetryQuery,
      retryDelay: queryRetryDelay,
    },
    mutations: {
      retry: false,
    },
  },
});

installContextInvalidation(() => {
  invalidateSessionIdentity();
  clearAllEvidenceDrafts();
  clearIdentityQueries(queryClient);
  void queryClient.resetQueries({ queryKey: ["session"] });
});

function rootElement(): HTMLElement {
  const root = document.getElementById("root");
  if (!root) throw new Error("缺少根节点");
  return root;
}

export function mountApplication(
  application: ReactNode,
  options: { routing?: boolean; queries?: boolean } = {},
) {
  let content = application;
  if (options.routing !== false)
    // Data router: router-level navigation blocking (useBlocker) for
    // dirty-form leave guards only works under RouterProvider.
    content = (
      <RouterProvider
        router={createBrowserRouter([
          { path: "*", element: content, errorElement: <ApplicationError /> },
        ])}
      />
    );
  if (options.queries !== false)
    content = <QueryClientProvider client={queryClient}>{content}</QueryClientProvider>;
  createRoot(rootElement()).render(
    <StrictMode>
      <StyleProvider layer>
        <ConfigProvider
          theme={uiTheme}
          locale={zhCN}
          button={{ autoInsertSpace: false }}
          popupMatchSelectWidth={false}
          virtual={false}
        >
          <AntdApp component={false}>{content}</AntdApp>
        </ConfigProvider>
      </StyleProvider>
    </StrictMode>,
  );
}
