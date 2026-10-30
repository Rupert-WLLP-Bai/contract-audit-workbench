import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Button, Checkbox, Input, Modal, Select } from "antd";
import { errorMessage } from "../api/http";
import {
  useProbeLLMEndpoint,
  useSaveLLMEndpoint,
  useSaveLLMModels,
} from "../api/queries";
import type { LLMEndpointView } from "../api/types";

const protocolOptions = [
  { value: "openai-compatible", label: "OpenAI 兼容（/v1）" },
];

function endpointActive(endpoint: LLMEndpointView): boolean {
  return (
    endpoint.configured &&
    endpoint.review_model !== "" &&
    endpoint.ocr_model !== "" &&
    endpoint.models.includes(endpoint.review_model) &&
    endpoint.models.includes(endpoint.ocr_model)
  );
}

function endpointState(endpoint: LLMEndpointView | undefined, loading: boolean) {
  if (loading) return { label: "正在读取", tone: "attention" as const };
  if (!endpoint?.configured)
    return { label: "未配置", tone: "attention" as const };
  if (endpointActive(endpoint))
    return { label: "已启用", tone: "good" as const };
  return { label: "待选择模型", tone: "attention" as const };
}

type ConfigDialogProps = {
  endpoint: LLMEndpointView | undefined;
  open: boolean;
  onOpenChange: (open: boolean) => void;
};

function LLMEndpointDialog({
  endpoint,
  open,
  onOpenChange,
}: ConfigDialogProps) {
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("openai-compatible");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [clearAPIKey, setClearAPIKey] = useState(false);
  const [models, setModels] = useState<string[]>([]);
  const [probedTarget, setProbedTarget] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const probe = useProbeLLMEndpoint();
  const save = useSaveLLMEndpoint();

  useEffect(() => {
    if (!open) return;
    setName(endpoint?.name ?? "");
    setProtocol(endpoint?.protocol || "openai-compatible");
    setBaseUrl(endpoint?.base_url ?? "");
    setApiKey("");
    setClearAPIKey(false);
    setModels(endpoint?.models ? [...endpoint.models] : []);
    setProbedTarget("");
    setSubmitted(false);
    // Refetches that keep the same revision must not overwrite an open form.
    // Reset mutation state only when the dialog opens: resetting it while a
    // successful save invalidates the endpoint would cancel the close callback.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, endpoint?.revision]);

  useEffect(() => {
    if (!open) return;
    probe.reset();
    save.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const nameError = name.trim() === "" ? "请填写端点名称" : "";
  const urlError = baseUrl.trim() === "" ? "请填写端点地址" : "";
  const modelsError = models.length === 0 ? "请至少保留一个模型" : "";
  const targetURL = baseUrl.trim().replace(/\/+$/, "");
  const credentialMoveError =
    endpoint?.has_api_key &&
    !clearAPIKey &&
    apiKey.trim() === "" &&
    targetURL !== "" &&
    endpoint.base_url !== targetURL
      ? "端点地址已更改：请重新输入密钥，或勾选清除已保存的密钥"
      : "";
  const probeResult = probe.data;
  const probeFresh = probeResult !== undefined && probedTarget === baseUrl.trim();

  const modelOptions = useMemo(() => {
    const ids = new Set<string>([
      ...(endpoint?.models ?? []),
      ...(probeResult?.models ?? []),
      ...models,
    ]);
    return Array.from(ids)
      .sort((left, right) => left.localeCompare(right))
      .map((id) => ({ value: id, label: id }));
  }, [endpoint, probeResult, models]);

  function onProbe() {
    if (baseUrl.trim() === "") {
      setSubmitted(true);
      return;
    }
    const target = baseUrl.trim();
    probe.mutate(
      {
        protocol,
        base_url: target,
        api_key: clearAPIKey ? undefined : apiKey.trim() || undefined,
        clear_api_key: clearAPIKey || undefined,
      },
      {
        onSuccess: (result) => {
          setProbedTarget(target);
          setModels(result.models);
        },
      },
    );
  }

  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitted(true);
    if (nameError || urlError || modelsError || credentialMoveError || save.isPending) return;
    save.mutate(
      {
        name: name.trim(),
        protocol,
        base_url: baseUrl.trim(),
        models,
        api_key: clearAPIKey ? undefined : apiKey.trim() || undefined,
        clear_api_key: clearAPIKey || undefined,
        expected_revision: endpoint?.configured
          ? endpoint.revision
          : undefined,
      },
      { onSuccess: () => onOpenChange(false) },
    );
  }

  return (
    <Modal
      open={open}
      title="配置 LLM 端点"
      footer={null}
      width={680}
      onCancel={() => onOpenChange(false)}
      destroyOnHidden
    >
      <form className="llm-endpoint-form" onSubmit={onSubmit}>
        <p className="settings-panel-note">
          密钥只在保存时提交一次，服务端加密保存；页面和接口都不会再返回明文。
        </p>
        <label className="field">
          名称
          <Input
            value={name}
            maxLength={80}
            placeholder="例如：生产 LLM 网关"
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        {submitted && nameError ? <p role="alert">{nameError}</p> : null}
        <label className="field">
          协议
          <Select
            aria-label="模型协议"
            value={protocol}
            options={protocolOptions}
            onChange={setProtocol}
          />
        </label>
        <label className="field">
          端点地址
          <Input
            value={baseUrl}
            placeholder="https://llm.example.com/v1"
            spellCheck={false}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </label>
        {submitted && urlError ? <p role="alert">{urlError}</p> : null}
        <label className="field">
          API 密钥
          <Input.Password
            value={apiKey}
            disabled={clearAPIKey}
            autoComplete="off"
            placeholder={
              endpoint?.has_api_key
                ? "已保存密钥；留空保持不变"
                : "端点不需要密钥时可留空"
            }
            onChange={(event) => setApiKey(event.target.value)}
          />
        </label>
        {endpoint?.has_api_key ? (
          <Checkbox
            checked={clearAPIKey}
            onChange={(event) => {
              setClearAPIKey(event.target.checked);
              if (event.target.checked) setApiKey("");
            }}
          >
            清除已保存的密钥
          </Checkbox>
        ) : null}
        {credentialMoveError ? (
          <p className="settings-panel-note" role="status">
            {credentialMoveError}
          </p>
        ) : null}
        <div className="llm-endpoint-probe">
          <Button
            type="default"
            htmlType="button"
            loading={probe.isPending}
            disabled={baseUrl.trim() === ""}
            onClick={onProbe}
          >
            探测模型
          </Button>
          {probeFresh ? (
            <span className="settings-panel-note" role="status">
              发现 {probeResult.models.length} 个模型，耗时 {probeResult.latency_ms} ms
            </span>
          ) : (
            <span className="settings-panel-note">
              探测读取端点的 /models 列表，不会保存配置。
            </span>
          )}
        </div>
        {probe.isError ? (
          <p className="workflow-error" role="alert">
            {errorMessage(probe.error)}
          </p>
        ) : null}
        <label className="field">
          模型列表
          <Select
            mode="tags"
            aria-label="端点模型列表"
            className="llm-endpoint-model-list"
            value={models}
            options={modelOptions}
            maxTagCount="responsive"
            allowClear
            placeholder="探测端点，或输入模型 ID 后回车"
            tokenSeparators={[",", " ", "\n"]}
            onChange={(values: string[]) => setModels(values)}
            notFoundContent={null}
          />
        </label>
        {submitted && modelsError ? <p role="alert">{modelsError}</p> : null}
        {save.isError ? (
          <p className="workflow-error" role="alert">
            {errorMessage(save.error)}
          </p>
        ) : null}
        <div className="dialog-actions">
          <Button
            type="text"
            htmlType="button"
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button
            type="primary"
            htmlType="submit"
            loading={save.isPending}
            disabled={save.isPending}
          >
            保存端点
          </Button>
        </div>
      </form>
    </Modal>
  );
}

export function LLMEndpointPanel({
  endpoint,
  loading,
  error,
  onRetry,
  open,
  onOpenChange,
}: {
  endpoint: LLMEndpointView | undefined;
  loading: boolean;
  error: unknown;
  onRetry: () => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const state = endpointState(endpoint, loading);
  return (
    <section className="settings-panel" aria-labelledby="settings-endpoint-title">
      <LLMEndpointDialog
        endpoint={endpoint}
        open={open}
        onOpenChange={onOpenChange}
      />
      <header className="settings-panel-head">
        <h2 id="settings-endpoint-title">LLM 端点</h2>
        <span className="settings-badge" data-tone={state.tone}>
          {state.label}
        </span>
      </header>
      <p className="meta">
        平台级的 OpenAI 兼容端点。端点模型列表保存后，还需在模型连通里选择审查模型和图片转写模型才会启用。
      </p>
      {error ? (
        <p className="workflow-error" role="alert">
          {errorMessage(error)}{" "}
          <Button htmlType="button" size="small" onClick={onRetry}>
            重试
          </Button>
        </p>
      ) : null}
      {endpoint?.configured ? (
        <dl className="settings-facts">
          <div>
            <dt>名称</dt>
            <dd>{endpoint.name}</dd>
          </div>
          <div>
            <dt>协议</dt>
            <dd>
              {endpoint.protocol === "openai-compatible"
                ? "OpenAI 兼容"
                : endpoint.protocol}
            </dd>
          </div>
          <div>
            <dt>地址</dt>
            <dd className="is-code">{endpoint.base_url}</dd>
          </div>
          <div>
            <dt>模型</dt>
            <dd>{endpoint.models.length} 个候选</dd>
          </div>
          <div>
            <dt>密钥</dt>
            <dd>{endpoint.has_api_key ? "已保存（服务端加密）" : "未保存"}</dd>
          </div>
          <div>
            <dt>更新</dt>
            <dd className="is-code">
              {endpoint.updated_at}
              {endpoint.updated_by ? ` · ${endpoint.updated_by}` : ""}
            </dd>
          </div>
        </dl>
      ) : (
        <p className="settings-panel-note">
          {loading
            ? "正在读取已保存的端点。"
            : "尚未保存端点。当前运行仍使用部署环境里的模型配置。"}
        </p>
      )}
      <div className="settings-endpoint-actions">
        <Button
          type={endpoint?.configured ? "default" : "primary"}
          htmlType="button"
          onClick={() => onOpenChange(true)}
        >
          {endpoint?.configured ? "修改端点" : "配置端点"}
        </Button>
        <span className="settings-panel-note">
          保存后新的模型调用立即使用新配置；进行中的审查若继续调用旧模型，可能因模型列表变化而失败。
        </span>
      </div>
    </section>
  );
}

export function LLMModelSelection({
  endpoint,
  loading,
  onConfigure,
}: {
  endpoint: LLMEndpointView | undefined;
  loading: boolean;
  onConfigure: () => void;
}) {
  const save = useSaveLLMModels();
  const [reviewModel, setReviewModel] = useState("");
  const [ocrModel, setOCRModel] = useState("");

  useEffect(() => {
    setReviewModel(endpoint?.review_model ?? "");
    setOCRModel(endpoint?.ocr_model ?? "");
  }, [endpoint?.review_model, endpoint?.ocr_model]);

  if (loading) {
    return <p className="settings-panel-note">正在读取端点模型列表…</p>;
  }
  if (!endpoint?.configured || endpoint.models.length === 0) {
    return (
      <div className="settings-model-controls">
        <p className="settings-panel-note">
          还没有可选的模型列表。先配置 LLM 端点并探测模型。
        </p>
        <div>
          <Button type="default" htmlType="button" onClick={onConfigure}>
            配置端点
          </Button>
        </div>
      </div>
    );
  }

  const options = endpoint.models.map((id) => ({ value: id, label: id }));
  const changed =
    reviewModel !== endpoint.review_model || ocrModel !== endpoint.ocr_model;
  const ready = reviewModel !== "" && ocrModel !== "";
  const active = endpointActive(endpoint);

  return (
    <div className="settings-model-controls">
      <div className="settings-model-grid">
        <label className="field">
          审查模型
          <Select
            id="review-model-select"
            aria-label="审查模型"
            value={reviewModel || undefined}
            options={options}
            placeholder="选择审查模型"
            showSearch
            optionFilterProp="label"
            onChange={setReviewModel}
          />
        </label>
        <label className="field">
          图片转写模型
          <Select
            id="ocr-model-select"
            aria-label="图片转写模型"
            value={ocrModel || undefined}
            options={options}
            placeholder="选择图片转写模型"
            showSearch
            optionFilterProp="label"
            onChange={setOCRModel}
          />
        </label>
      </div>
      <div className="settings-model-actions">
        <span className="settings-panel-note">
          {active
            ? "已启用。保存新选择后，新的模型调用立即使用该配置。"
            : "同时选定两个模型后才会启用。"}
        </span>
        <Button
          type="primary"
          htmlType="button"
          loading={save.isPending}
          disabled={!ready || !changed || save.isPending}
          onClick={() => {
            save.mutate({
              review_model: reviewModel,
              ocr_model: ocrModel,
              expected_revision: endpoint.revision,
            });
          }}
        >
          保存模型选择
        </Button>
      </div>
      {save.isError ? (
        <p className="workflow-error" role="alert">
          {errorMessage(save.error)}
        </p>
      ) : null}
    </div>
  );
}
