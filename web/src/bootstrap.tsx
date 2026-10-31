import { App as AntdApp, ConfigProvider, type ThemeConfig } from "antd";
import { StyleProvider } from "@ant-design/cssinjs";
import zhCN from "antd/locale/zh_CN";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { queryRetryDelay, shouldRetryQuery } from "./api/http";
import "./styles.css";

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
    borderRadius: Number.parseFloat(cssToken("--panel-radius")),
    fontFamily: cssToken("--font-ui"),
    fontSize: Number.parseFloat(cssToken("--font-size-ui")),
    controlHeight: 32,
    motion: !window.matchMedia("(prefers-reduced-motion: reduce)").matches,
  },
  components: {
    Button: {
      primaryColor: cssToken("--button-primary-fg"),
      primaryShadow: "none",
      fontWeight: 500,
    },
    Layout: {
      bodyBg: cssToken("--surface-page"),
      headerBg: cssToken("--surface-rail"),
      headerColor: cssToken("--text-inverse"),
      siderBg: cssToken("--surface-rail"),
      triggerBg: cssToken("--surface-rail-raised"),
      triggerColor: cssToken("--text-inverse-secondary"),
    },
    Menu: {
      darkItemBg: cssToken("--surface-rail"),
      darkPopupBg: cssToken("--surface-rail"),
      darkSubMenuItemBg: cssToken("--surface-rail"),
      darkItemColor: cssToken("--text-inverse-secondary"),
      darkGroupTitleColor: cssToken("--text-inverse-secondary"),
      darkItemSelectedBg: cssToken("--nav-item-selected"),
      darkItemSelectedColor: cssToken("--nav-item-selected-fg"),
      darkItemHoverBg: cssToken("--surface-rail-raised"),
      darkItemHoverColor: cssToken("--text-inverse"),
      itemBorderRadius: Number.parseFloat(cssToken("--radius-control")),
      groupTitleFontSize: 14,
    },
    Table: {
      headerBg: cssToken("--surface-subtle"),
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
        router={createBrowserRouter([{ path: "*", element: content }])}
      />
    );
  if (options.queries !== false)
    content = (
      <QueryClientProvider client={queryClient}>{content}</QueryClientProvider>
    );
  createRoot(rootElement()).render(
    <StrictMode>
      <StyleProvider layer>
        <ConfigProvider
          theme={uiTheme}
          locale={zhCN}
          button={{ autoInsertSpace: false }}
        >
          <AntdApp component={false}>{content}</AntdApp>
        </ConfigProvider>
      </StyleProvider>
    </StrictMode>,
  );
}
