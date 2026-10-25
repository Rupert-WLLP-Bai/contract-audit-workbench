import { App as AntdApp, ConfigProvider } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import "./styles.css";

const rootStyle = getComputedStyle(document.documentElement);
const uiTheme = {
  token: {
    colorPrimary: rootStyle.getPropertyValue("--action-primary").trim(),
    colorInfo: rootStyle.getPropertyValue("--action-primary").trim(),
    colorText: rootStyle.getPropertyValue("--text-primary").trim(),
    colorTextSecondary: rootStyle.getPropertyValue("--text-secondary").trim(),
    colorBorder: rootStyle.getPropertyValue("--border-default").trim(),
    colorBgLayout: rootStyle.getPropertyValue("--surface-page").trim(),
    colorBgContainer: rootStyle.getPropertyValue("--surface-raised").trim(),
    borderRadius: Number.parseFloat(rootStyle.getPropertyValue("--panel-radius")),
    fontFamily: rootStyle.getPropertyValue("--font-ui").trim(),
    fontSize: Number.parseFloat(rootStyle.getPropertyValue("--font-size-ui")),
    controlHeight: 32,
  },
};

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: false,
    },
  },
});

function rootElement(): HTMLElement {
  const root = document.getElementById("root");
  if (!root) throw new Error("缺少根节点");
  return root;
}

export function mountApplication(application: ReactNode, options: { routing?: boolean; queries?: boolean } = {}) {
  let content = application;
  if (options.routing !== false) content = <BrowserRouter>{content}</BrowserRouter>;
  if (options.queries !== false) content = <QueryClientProvider client={queryClient}>{content}</QueryClientProvider>;
  createRoot(rootElement()).render(
    <StrictMode>
      <ConfigProvider theme={uiTheme}>
        <AntdApp component={false}>{content}</AntdApp>
      </ConfigProvider>
    </StrictMode>,
  );
}
