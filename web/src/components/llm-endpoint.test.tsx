import { useState } from "react";
import {
  cleanup,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import * as api from "../api/endpoints";
import type { LLMEndpointView } from "../api/types";
import { renderWithAntd as render } from "../test/antd";
import { LLMEndpointPanel, LLMModelSelection } from "./llm-endpoint";

const endpoint: LLMEndpointView = {
  configured: true,
  name: "生产网关",
  protocol: "openai-compatible",
  base_url: "https://llm.example.com/v1",
  models: ["alpha", "beta", "gamma"],
  review_model: "alpha",
  ocr_model: "beta",
  has_api_key: true,
  revision: 7,
  updated_at: "2026-10-30T08:00:00.000Z",
  updated_by: "admin-1",
};

const clients: QueryClient[] = [];

async function chooseScopedOption(
  user: ReturnType<typeof userEvent.setup>,
  control: HTMLElement,
  name: string,
) {
  await user.click(control);
  const listId = control.getAttribute("aria-controls");
  if (listId === null) throw new Error("select has no listbox");
  const listbox = document.getElementById(listId);
  if (listbox === null) throw new Error("select listbox is unavailable");
  await user.click(within(listbox).getByRole("option", { name }));
}

afterEach(() => {
  cleanup();
  for (const client of clients.splice(0)) client.clear();
  vi.restoreAllMocks();
});

function renderPanel(open: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  function Harness() {
    const [isOpen, setOpen] = useState(open);
    return (
      <LLMEndpointPanel
        endpoint={endpoint}
        loading={false}
        error={null}
        onRetry={() => undefined}
        open={isOpen}
        onOpenChange={setOpen}
      />
    );
  }
  return render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  );
}

describe("LLM endpoint settings", () => {
  it("probes the typed credential and saves it only in the request body", async () => {
    const user = userEvent.setup();
    const probe = vi.spyOn(api, "probeLLMEndpoint").mockResolvedValue({
      models: ["alpha", "beta"],
      latency_ms: 42,
    });
    const save = vi.spyOn(api, "saveLLMEndpoint").mockResolvedValue(endpoint);
    renderPanel(true);
    expect(screen.getByText("已保存（服务端加密）")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("端点地址"), {
      target: { value: "https://new.example.com/v1" },
    });
    await user.type(screen.getByLabelText("API 密钥"), "sk-new-key");
    await user.click(screen.getByRole("button", { name: "探测模型" }));

    expect(await screen.findByText(/发现 2 个模型，耗时 42 ms/)).toBeInTheDocument();
    expect(probe).toHaveBeenCalledWith({
      protocol: "openai-compatible",
      base_url: "https://new.example.com/v1",
      api_key: "sk-new-key",
      clear_api_key: undefined,
    });

    await user.click(screen.getByRole("button", { name: "保存端点" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0]?.[0]).toEqual({
      name: "生产网关",
      protocol: "openai-compatible",
      base_url: "https://new.example.com/v1",
      models: ["alpha", "beta"],
      api_key: "sk-new-key",
      clear_api_key: undefined,
      expected_revision: 7,
    });
  });

  it("does not carry a stored credential to a different endpoint", async () => {
    const user = userEvent.setup();
    const save = vi.spyOn(api, "saveLLMEndpoint").mockResolvedValue(endpoint);
    renderPanel(true);

    fireEvent.change(screen.getByLabelText("端点地址"), {
      target: { value: "https://new.example.com/v1" },
    });
    expect(
      screen.getByText(
        "端点地址已更改：请重新输入密钥，或勾选清除已保存的密钥",
      ),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存端点" }));
    expect(save).not.toHaveBeenCalled();
  });

  it("selects role models from the saved catalog with the current revision", async () => {
    const user = userEvent.setup();
    const save = vi.spyOn(api, "saveLLMModels").mockResolvedValue({
      ...endpoint,
      review_model: "gamma",
      ocr_model: "alpha",
      revision: 8,
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <LLMModelSelection
          endpoint={endpoint}
          loading={false}
          onConfigure={() => undefined}
        />
      </QueryClientProvider>,
    );

    const saveButton = screen.getByRole("button", { name: "保存模型选择" });
    expect(saveButton).toBeDisabled();
    await chooseScopedOption(
      user,
      screen.getByRole("combobox", { name: "审查模型" }),
      "gamma",
    );
    await chooseScopedOption(
      user,
      screen.getByRole("combobox", { name: "图片转写模型" }),
      "alpha",
    );
    expect(saveButton).toBeEnabled();
    await user.click(saveButton);
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0]?.[0]).toEqual({
      review_model: "gamma",
      ocr_model: "alpha",
      expected_revision: 7,
    });
    expect(screen.queryByLabelText("API 密钥")).not.toBeInTheDocument();
  });

  it("does not show role selectors before a model catalog exists", () => {
    const client = new QueryClient();
    clients.push(client);
    render(
      <QueryClientProvider client={client}>
        <LLMModelSelection
          endpoint={{ ...endpoint, models: [], review_model: "", ocr_model: "" }}
          loading={false}
          onConfigure={() => undefined}
        />
      </QueryClientProvider>,
    );
    expect(screen.getByText(/先配置 LLM 端点并探测模型/)).toBeInTheDocument();
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  });
});
