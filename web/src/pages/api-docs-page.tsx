import { Menu, Tabs, Tag } from "antd";
import { Collapse } from "antd";
import {
  Input,
  Button,
  Table,
  Upload,
  type TableProps,
  type UploadFile,
} from "antd";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  parseOpenApiContract,
  type OpenApiContract,
  type OpenApiOperation,
  type OpenApiParameter,
} from "../api/openapi";
import {
  apiParameterInputKey,
  apiRequestBodyFields,
  apiRequestBodyMode,
  apiRequestCode,
  buildApiRequest,
  type ApiRequestBodyField,
} from "../api/openapi-request";
import { useSession } from "../api/queries";
import "./api-docs.css";

type DocumentationSection =
  | "docs"
  | "reference"
  | "auth"
  | "sdk"
  | "changelog"
  | "community";
type ApiResponse =
  | {
      kind: "http";
      status: number;
      statusText: string;
      ok: boolean;
      headers: string[];
      body: string;
    }
  | { kind: "network-error"; message: string }
  | { kind: "cancelled" };

const SECTIONS: { key: DocumentationSection; label: string }[] = [
  { key: "docs", label: "文档" },
  { key: "reference", label: "API 参考" },
  { key: "auth", label: "鉴权" },
  { key: "sdk", label: "SDK" },
  { key: "changelog", label: "更新日志" },
  { key: "community", label: "开发者社区" },
];

function methodTone(method: string): string {
  const tones: Record<string, string> = {
    GET: "success",
    POST: "processing",
    PUT: "warning",
    PATCH: "warning",
    DELETE: "error",
  };
  return tones[method] ?? "default";
}

function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2) ?? "";
}

function uploadFileUid(file: File, index: number): string {
  return `${file.name}:${file.lastModified}:${file.size}:${index}`;
}

function operationGroups(
  contract: OpenApiContract,
  query: string,
): { tag: string; operations: OpenApiOperation[] }[] {
  const needle = query.trim().toLocaleLowerCase("zh-CN");
  const groups = new Map<string, OpenApiOperation[]>();
  for (const operation of contract.operations) {
    if (
      needle !== "" &&
      !`${operation.method} ${operation.path} ${operation.operationId} ${operation.summary} ${operation.description} ${operation.tags.join(" ")}`
        .toLocaleLowerCase("zh-CN")
        .includes(needle)
    )
      continue;
    const tag = operation.tags[0] ?? "未分类";
    groups.set(tag, [...(groups.get(tag) ?? []), operation]);
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right, "zh-CN"))
    .map(([tag, operations]) => ({ tag, operations }));
}

function ContractLoader({
  attempt,
  onLoad,
  onError,
}: {
  attempt: number;
  onLoad: (contract: OpenApiContract) => void;
  onError: (message: string) => void;
}) {
  useEffect(() => {
    const controller = new AbortController();
    onError("");
    void (async () => {
      try {
        const response = await fetch("/openapi.json", {
          credentials: "same-origin",
          cache: "no-store",
          signal: controller.signal,
          headers: { Accept: "application/json" },
        });
        if (!response.ok)
          throw new Error(`接口契约加载失败（HTTP ${response.status}）`);
        let parsed: OpenApiContract;
        try {
          parsed = parseOpenApiContract(await response.json());
        } catch (failure) {
          throw new Error(
            `接口契约格式不正确：${failure instanceof Error ? failure.message : "契约内容无效"}`,
          );
        }
        if (!controller.signal.aborted) onLoad(parsed);
      } catch (failure) {
        if (!controller.signal.aborted)
          onError(
            failure instanceof Error ? failure.message : "接口契约加载失败",
          );
      }
    })();
    return () => controller.abort();
  }, [attempt, onError, onLoad]);
  return null;
}

function ParameterField({
  operation,
  parameter,
  value,
  onChange,
}: {
  operation: OpenApiOperation;
  parameter: OpenApiParameter;
  value: string;
  onChange: (key: string, value: string) => void;
}) {
  const key = apiParameterInputKey(operation, parameter);
  const example =
    parameter.example === null
      ? ""
      : typeof parameter.example === "string"
        ? parameter.example
        : pretty(parameter.example);
  const schemaType =
    parameter.schema && typeof parameter.schema.type === "string"
      ? parameter.schema.type
      : "string";
  return (
    <label className="api-request-field">
      <span>
        {parameter.name}
        <small>
          {parameter.location}
          {parameter.required ? " · 必填" : " · 可选"}
          {schemaType === "string" ? "" : ` · ${schemaType}`}
        </small>
      </span>
      {parameter.description ? (
        <small className="api-field-description">{parameter.description}</small>
      ) : null}
      <Input
        aria-label={`${parameter.name} ${parameter.location}`}
        value={value}
        placeholder={
          example || (parameter.required ? "填写真实参数" : "可留空")
        }
        onChange={(event) => onChange(key, event.target.value)}
      />
    </label>
  );
}

function OperationReference({
  operation,
  contract,
  bodyFields,
}: {
  operation: OpenApiOperation;
  contract: OpenApiContract;
  bodyFields: ApiRequestBodyField[];
}) {
  const responses = operation.responses;
  const operationSecurity = operation.security ?? contract.raw.security ?? null;
  const parameterColumns: TableProps<OpenApiParameter>["columns"] = [
    { title: "名称", dataIndex: "name", key: "name", rowScope: "row" },
    { title: "位置", dataIndex: "location", key: "location" },
    {
      title: "类型",
      key: "type",
      render: (_, parameter) =>
        parameter.schema && typeof parameter.schema.type === "string"
          ? parameter.schema.type
          : parameter.schema?.$ref
            ? String(parameter.schema.$ref)
            : "未声明",
    },
    {
      title: "必填",
      key: "required",
      render: (_, parameter) => (parameter.required ? "是" : "否"),
    },
    {
      title: "说明",
      key: "description",
      render: (_, parameter) => parameter.description || "未提供",
    },
  ];
  const bodyColumns: TableProps<ApiRequestBodyField>["columns"] = [
    { title: "名称", dataIndex: "name", key: "name", rowScope: "row" },
    {
      title: "类型",
      key: "type",
      render: (_, field) =>
        typeof field.schema.type === "string"
          ? field.schema.type
          : "见完整定义",
    },
    {
      title: "必填",
      key: "required",
      render: (_, field) => (field.required ? "是" : "否"),
    },
    {
      title: "说明",
      key: "description",
      render: (_, field) =>
        field.description ||
        (Array.isArray(field.schema.enum)
          ? field.schema.enum.join(" / ")
          : "未提供"),
    },
  ];
  return (
    <article className="api-operation-reference">
      <div className="api-operation-heading">
        <Tag color={methodTone(operation.method)}>{operation.method}</Tag>
        <code>{operation.path}</code>
      </div>
      <h1>{operation.summary || operation.operationId}</h1>
      <p className="api-operation-id">{operation.operationId}</p>
      {operation.description ? (
        <p className="api-operation-description">{operation.description}</p>
      ) : null}
      <div className="api-operation-tags">
        {operation.tags.map((tag) => (
          <Tag key={tag}>{tag}</Tag>
        ))}
      </div>
      <section className="api-contract-section">
        <h2>参数</h2>
        {operation.parameters.length === 0 ? (
          <p className="api-no-data">此操作未在 OpenAPI 中声明参数。</p>
        ) : (
          <div className="api-parameter-table-wrap">
            <Table
              className="api-parameter-table"
              columns={parameterColumns}
              dataSource={operation.parameters}
              rowKey={(parameter) => `${parameter.location}:${parameter.name}`}
              pagination={false}
            />
          </div>
        )}
      </section>
      {operation.requestBody ? (
        <section className="api-contract-section">
          <h2>
            请求体{operation.requestBody.required ? " · 必填" : " · 可选"}
          </h2>
          <p>
            {operation.requestBody.description || "OpenAPI 未提供请求体说明。"}{" "}
            · {operation.requestBody.mediaType ?? "媒体类型未声明"}
          </p>
          {bodyFields.length > 0 ? (
            <div className="api-parameter-table-wrap">
              <Table
                className="api-parameter-table"
                columns={bodyColumns}
                dataSource={bodyFields}
                rowKey="name"
                pagination={false}
              />
            </div>
          ) : null}
          {operation.requestBody.schema ? (
            <Collapse
              className="api-openapi-source"
              items={[
                {
                  key: "content",
                  label: "完整请求体 Schema",
                  children: (
                    <>
                      <pre className="api-json-schema">
                        {pretty(operation.requestBody.schema)}
                      </pre>
                    </>
                  ),
                },
              ]}
            />
          ) : (
            <p className="api-no-data">契约未提供请求体 schema。</p>
          )}
        </section>
      ) : null}
      <section className="api-contract-section">
        <h2>响应</h2>
        {responses.length === 0 ? (
          <p className="api-no-data">契约没有返回响应定义。</p>
        ) : (
          <ul className="api-response-list">
            {responses.map((response) => (
              <li key={response.status}>
                <code>{response.status}</code>
                <span>{response.description}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="api-contract-section">
        <h2>鉴权声明</h2>
        <pre className="api-json-schema">
          {operationSecurity === null
            ? "OpenAPI 未声明此操作的 security。服务端仍会执行实际身份与资源权限检查。"
            : pretty(operationSecurity)}
        </pre>
      </section>
      <Collapse
        className="api-openapi-source"
        items={[
          {
            key: "content",
            label: "查看此操作的 OpenAPI 原始定义",
            children: (
              <>
                <pre>{pretty(operation.raw)}</pre>
              </>
            ),
          },
        ]}
      />
    </article>
  );
}

function ApiExplorer({
  contract,
  onCodeChange,
}: {
  contract: OpenApiContract;
  onCodeChange: (code: string | null) => void;
}) {
  const [operationKey, setOperationKey] = useState("");
  const [filter, setFilter] = useState("");
  const [parameterValues, setParameterValues] = useState<
    Record<string, string>
  >({});
  const [bodyText, setBodyText] = useState("");
  const [bodyValues, setBodyValues] = useState<Record<string, string>>({});
  const [bodyFiles, setBodyFiles] = useState<Record<string, File[]>>({});
  const [response, setResponse] = useState<ApiResponse | null>(null);
  const [validation, setValidation] = useState("");
  const [sending, setSending] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);

  const groups = useMemo(
    () => operationGroups(contract, filter),
    [contract, filter],
  );
  const operation =
    contract.operations.find((entry) => entry.key === operationKey) ??
    contract.operations[0] ??
    null;
  const requestBody = operation?.requestBody ?? null;
  const bodyMode = apiRequestBodyMode(requestBody);
  const bodyFieldsResult = useMemo(() => {
    try {
      return {
        fields: apiRequestBodyFields(requestBody, contract.raw),
        error: "",
      };
    } catch (failure) {
      return {
        fields: [],
        error:
          failure instanceof Error ? failure.message : "请求体 schema 无效。",
      };
    }
  }, [contract.raw, requestBody]);
  const built = operation
    ? buildApiRequest({
        operation,
        values: parameterValues,
        bodyText,
        bodyValues,
        bodyFiles,
        origin: window.location.origin,
        schemaRoot: contract.raw,
      })
    : { request: null, error: "请选择 OpenAPI 操作。" };
  const code = operation
    ? apiRequestCode(operation, built.request, contract.raw)
    : null;
  useEffect(() => onCodeChange(code), [code, onCodeChange]);
  const hasCookieParameter =
    operation?.parameters.some(
      (parameter) => parameter.location === "cookie",
    ) ?? false;
  const hasExample =
    requestBody?.example !== null && requestBody?.example !== undefined;
  const binaryRequest = bodyMode === "binary";

  function selectOperation(next: OpenApiOperation) {
    setOperationKey(next.key);
    setParameterValues({});
    setBodyText("");
    setBodyValues({});
    setBodyFiles({});
    setResponse(null);
    setValidation("");
  }

  function updateParameter(key: string, value: string) {
    setParameterValues((current) => ({ ...current, [key]: value }));
    setValidation("");
  }

  function updateBodyValue(key: string, value: string) {
    setBodyValues((current) => ({ ...current, [key]: value }));
    setValidation("");
  }

  function updateBodyFiles(key: string, files: File[]) {
    setBodyFiles((current) => ({ ...current, [key]: files }));
    setValidation("");
  }

  function handleUploadChange(key: string, fileList: UploadFile[]) {
    const previous = bodyFiles[key] ?? [];
    const previousByUid = new Map(
      previous.map((file, index) => [uploadFileUid(file, index), file]),
    );
    updateBodyFiles(
      key,
      fileList.flatMap((file) => {
        const original = file.originFileObj ?? previousByUid.get(file.uid);
        return original ? [original] : [];
      }),
    );
  }

  async function submitRequest(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!operation) return;
    const request = buildApiRequest({
      operation,
      values: parameterValues,
      bodyText,
      bodyValues,
      bodyFiles,
      origin: window.location.origin,
      schemaRoot: contract.raw,
    });
    if (!request.request) {
      setValidation(request.error ?? "请求参数无效。");
      return;
    }
    setValidation("");
    setResponse(null);
    const nextController = new AbortController();
    controller.current = nextController;
    setSending(true);
    try {
      const httpResponse = await fetch(request.request.url, {
        ...request.request.init,
        signal: nextController.signal,
      });
      const rawBody = await httpResponse.text();
      setResponse({
        kind: "http",
        status: httpResponse.status,
        statusText: httpResponse.statusText,
        ok: httpResponse.ok,
        headers: [...httpResponse.headers.entries()].map(
          ([name, value]) => `${name}: ${value}`,
        ),
        body: rawBody,
      });
    } catch (failure) {
      if (nextController.signal.aborted) setResponse({ kind: "cancelled" });
      else
        setResponse({
          kind: "network-error",
          message: failure instanceof Error ? failure.message : "同源请求失败",
        });
    } finally {
      if (controller.current === nextController) controller.current = null;
      setSending(false);
    }
  }
  return (
    <div className="api-explorer-grid">
      <aside className="api-explorer-sidebar" aria-label="OpenAPI 操作导航">
        <div className="api-explorer-sidebar-head">
          <strong>接口目录</strong>
          <span>{contract.operations.length} 个操作</span>
        </div>
        <label className="api-filter-label">
          筛选接口
          <Input
            type="search"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder="方法、路径、名称"
          />
        </label>
        <Menu
          mode="inline"
          selectedKeys={operation ? [operation.key] : []}
          onClick={({ key }) => {
            const selectedOperation = contract.operations.find(
              (item) => item.key === key,
            );
            if (selectedOperation) selectOperation(selectedOperation);
          }}
          items={groups.map((group) => ({
            key: group.tag,
            type: "group",
            label: group.tag,
            children: group.operations.map((entry) => ({
              key: entry.key,
              label: (
                <span title={entry.path}>
                  <Tag color={methodTone(entry.method)}>{entry.method}</Tag>
                  {entry.summary || entry.operationId}
                </span>
              ),
            })),
          }))}
        />
      </aside>
      {operation ? (
        <>
          <main className="api-reference-content" aria-label="接口详细文档">
            <OperationReference
              operation={operation}
              contract={contract}
              bodyFields={bodyFieldsResult.fields}
            />
          </main>
          <aside className="api-request-panel" aria-label="真实请求调试区">
            <header className="api-request-head">
              <div>
                <p>同源请求</p>
                <h2>试运行</h2>
              </div>
              <Tag color={methodTone(operation.method)}>{operation.method}</Tag>
            </header>
            <p className="api-request-route">{operation.path}</p>
            <p className="api-request-auth">
              使用当前登录会话。服务端按每次请求执行身份和案件权限检查。
            </p>
            <section className="api-code-section">
              <header>
                <div>
                  <h2>同源调用示例</h2>
                  <p>示例随右侧真实请求参数更新，使用当前浏览器会话。</p>
                </div>
              </header>
              {code ? (
                <pre>
                  <code>{code}</code>
                </pre>
              ) : (
                <p className="api-no-data">
                  填写右侧必填参数和必要请求体后，这里显示当前操作的可执行调用代码。
                </p>
              )}
            </section>
            {hasCookieParameter ? (
              <p className="api-request-auth">
                Cookie 参数由浏览器会话管理。此处不会收集或覆盖身份 Cookie。
              </p>
            ) : null}
            <form onSubmit={(event) => void submitRequest(event)}>
              {operation.parameters.filter(
                (parameter) => parameter.location !== "cookie",
              ).length > 0 ? (
                <section className="api-request-fields">
                  <h3>路径、查询与 Header 参数</h3>
                  {operation.parameters
                    .filter((parameter) => parameter.location !== "cookie")
                    .map((parameter) => (
                      <ParameterField
                        key={`${parameter.location}:${parameter.name}`}
                        operation={operation}
                        parameter={parameter}
                        value={
                          parameterValues[
                            apiParameterInputKey(operation, parameter)
                          ] ?? ""
                        }
                        onChange={updateParameter}
                      />
                    ))}
                </section>
              ) : (
                <p className="api-no-data">此操作没有额外参数。</p>
              )}
              {requestBody ? (
                <section className="api-request-body" aria-label="请求体编辑器">
                  <div className="api-request-body-heading">
                    <h3>
                      请求体{requestBody.required ? " · 必填" : " · 可选"}
                    </h3>
                    <span>{requestBody.mediaType ?? "媒体类型未声明"}</span>
                  </div>
                  {bodyMode === "multipart" || bodyMode === "urlencoded" ? (
                    <>
                      {bodyFieldsResult.fields.map((field) => (
                        <label
                          className="api-request-field api-request-body-field"
                          key={`${operation.key}:${field.name}`}
                        >
                          <span>
                            {field.name}
                            <small>
                              {field.required ? "必填" : "可选"}
                              {field.file
                                ? field.multiple
                                  ? " · 多个原件"
                                  : " · 原件文件"
                                : typeof field.schema.type === "string"
                                  ? ` · ${field.schema.type}`
                                  : ""}
                            </small>
                          </span>
                          {field.description ? (
                            <small className="api-field-description">
                              {field.description}
                            </small>
                          ) : null}
                          {field.file ? (
                            <Upload
                              key={`${operation.key}:${field.name}`}
                              aria-label={`请求体 ${field.name}`}
                              data-request-body-field={field.name}
                              multiple={field.multiple}
                              beforeUpload={() => false}
                              fileList={(bodyFiles[field.name] ?? []).map(
                                (file, index) => ({
                                  uid: uploadFileUid(file, index),
                                  name: file.name,
                                }),
                              )}
                              onChange={({ fileList }) =>
                                handleUploadChange(field.name, fileList)
                              }
                            >
                              <Button aria-label={"选择文件"} htmlType="button">
                                选择文件
                              </Button>
                            </Upload>
                          ) : field.schema.type === "object" ? (
                            <Input.TextArea
                              aria-label={`请求体 ${field.name}`}
                              value={bodyValues[field.name] ?? ""}
                              placeholder="填写 JSON 对象"
                              onChange={(event) =>
                                updateBodyValue(field.name, event.target.value)
                              }
                            />
                          ) : (
                            <Input
                              aria-label={`请求体 ${field.name}`}
                              value={bodyValues[field.name] ?? ""}
                              placeholder={
                                field.schema.type === "array"
                                  ? "多个值用逗号分隔"
                                  : field.required
                                    ? "填写真实字段值"
                                    : "可留空"
                              }
                              onChange={(event) =>
                                updateBodyValue(field.name, event.target.value)
                              }
                            />
                          )}
                        </label>
                      ))}
                      {bodyFieldsResult.fields.length === 0 ? (
                        <p className="api-no-data">
                          {requestBody.schema
                            ? "此表单 schema 未声明可编辑字段。"
                            : "契约未提供表单 schema，无法填写并发送该请求。"}
                        </p>
                      ) : null}
                      {bodyMode === "multipart" ? (
                        <p className="api-request-body-note">
                          文件以浏览器所选原件作为 multipart part
                          发送；浏览器会自动添加 boundary。
                        </p>
                      ) : null}
                    </>
                  ) : binaryRequest ? (
                    <label className="api-request-field api-request-body-field">
                      <span>
                        请求原件
                        <small>{requestBody.required ? "必填" : "可选"}</small>
                      </span>
                      <Upload
                        key={`${operation.key}:binary`}
                        aria-label="请求原件"
                        data-request-body-field="__request_body__"
                        beforeUpload={() => false}
                        fileList={(bodyFiles.__request_body__ ?? []).map(
                          (file, index) => ({
                            uid: uploadFileUid(file, index),
                            name: file.name,
                          }),
                        )}
                        onChange={({ fileList }) =>
                          handleUploadChange("__request_body__", fileList)
                        }
                      >
                        <Button aria-label={"选择请求原件"} htmlType="button">
                          选择请求原件
                        </Button>
                      </Upload>
                    </label>
                  ) : (
                    <label className="api-request-body-text">
                      <span>请求内容</span>
                      <Input.TextArea
                        aria-label="真实请求体"
                        value={bodyText}
                        onChange={(event) => {
                          setBodyText(event.target.value);
                          setValidation("");
                        }}
                        placeholder={
                          bodyMode === "json"
                            ? "按 OpenAPI schema 填写 JSON"
                            : "填写契约声明的真实请求内容"
                        }
                      />
                    </label>
                  )}
                  {hasExample &&
                  (bodyMode === "json" || bodyMode === "text") ? (
                    <Button
                      aria-label={"使用 OpenAPI 契约示例"}
                      className="api-example-button"
                      htmlType="button"
                      onClick={() => setBodyText(pretty(requestBody.example))}
                    >
                      使用 OpenAPI 契约示例
                    </Button>
                  ) : null}
                </section>
              ) : null}
              {validation || bodyFieldsResult.error || built.error ? (
                <p className="api-request-error" role="alert">
                  {validation || bodyFieldsResult.error || built.error}
                </p>
              ) : null}
              <div className="api-request-actions">
                <Button
                  aria-label={String(sending ? "正在发送" : "发送真实请求")}
                  type="primary"
                  htmlType="submit"
                  disabled={sending || built.request === null}
                >
                  {sending ? "正在发送" : "发送真实请求"}
                </Button>
                {sending ? (
                  <Button
                    aria-label={"取消请求"}
                    htmlType="button"
                    onClick={() => controller.current?.abort()}
                  >
                    取消请求
                  </Button>
                ) : null}
              </div>
            </form>
            {response ? (
              <section className="api-response-panel" aria-live="polite">
                <header>
                  <h3>原始响应</h3>
                  {response.kind === "http" ? (
                    <span data-ok={response.ok}>
                      {response.status} {response.statusText}
                    </span>
                  ) : null}
                </header>
                {response.kind === "http" ? (
                  <>
                    <Collapse
                      items={[
                        {
                          key: "content",
                          label: "响应 Header",
                          children: (
                            <>
                              <pre>
                                {response.headers.join("\n") ||
                                  "没有可读取的响应 Header。"}
                              </pre>
                            </>
                          ),
                        },
                      ]}
                    />
                    <pre className="api-response-body">
                      {response.body || "（响应体为空）"}
                    </pre>
                  </>
                ) : response.kind === "network-error" ? (
                  <p role="alert">{response.message}</p>
                ) : (
                  <p role="status">请求已取消。</p>
                )}
              </section>
            ) : null}
          </aside>
        </>
      ) : (
        <main className="api-reference-content">
          <p className="api-no-data">从左侧选择 OpenAPI 操作。</p>
        </main>
      )}
    </div>
  );
}

function SectionContent({
  section,
  contract,
  sampleCode,
}: {
  section: DocumentationSection;
  contract: OpenApiContract;
  sampleCode: string | null;
}) {
  if (section === "reference") return null;
  if (section === "auth")
    return (
      <section className="api-section-page">
        <p className="api-section-eyebrow">API 平台 / 鉴权</p>
        <h1>身份与权限</h1>
        <p>
          API Explorer
          使用当前浏览器同源会话发送请求。服务端仍会逐次校验会话身份、组织范围、案件成员授权和资源访问权限。
        </p>
        <dl>
          <div>
            <dt>当前接口版本</dt>
            <dd>{contract.version}</dd>
          </div>
          <div>
            <dt>认证方案声明</dt>
            <dd>
              <pre>
                {contract.raw.security === undefined
                  ? "OpenAPI 没有提供全局 security 声明。实际访问仍以服务端授权结果为准。"
                  : pretty(contract.raw.security)}
              </pre>
            </dd>
          </div>
        </dl>
        <p>
          页面不会要求输入身份 Cookie 或远程服务地址；请求通过当前页面同源发送。
        </p>
      </section>
    );
  if (section === "sdk")
    return (
      <section className="api-section-page">
        <p className="api-section-eyebrow">API 平台 / SDK</p>
        <h1>SDK 资源</h1>
        <p>
          当前 OpenAPI 契约没有声明已发布的 SDK
          包或下载资源。请求区提供基于实际操作、参数和同源会话生成的 Fetch
          调用代码。
        </p>
        {sampleCode ? (
          <pre className="api-section-code">
            <code>{sampleCode}</code>
          </pre>
        ) : (
          <p className="api-no-data">
            选择 API 参考中的操作并填写必填参数后，可查看同源调用代码。
          </p>
        )}
      </section>
    );
  if (section === "changelog")
    return (
      <section className="api-section-page">
        <p className="api-section-eyebrow">API 平台 / 更新日志</p>
        <h1>接口版本</h1>
        <dl>
          <div>
            <dt>契约标题</dt>
            <dd>{contract.title}</dd>
          </div>
          <div>
            <dt>OpenAPI 版本</dt>
            <dd>{contract.openapi}</dd>
          </div>
          <div>
            <dt>服务版本</dt>
            <dd>{contract.version}</dd>
          </div>
        </dl>
        <p>
          当前 OpenAPI 文档未声明独立的 API
          更新日志资源；此处显示本次读取到的服务端契约版本。
        </p>
      </section>
    );
  if (section === "community")
    return (
      <section className="api-section-page">
        <p className="api-section-eyebrow">API 平台 / 开发者社区</p>
        <h1>开发者支持</h1>
        <p>当前没有配置可验证的公开开发者社区、论坛或发帖入口。</p>
        <p>请以服务端 OpenAPI 契约和授权后的真实响应为准。</p>
      </section>
    );
  return (
    <section className="api-section-page api-docs-home">
      <p className="api-section-eyebrow">
        开发者资源 / OpenAPI {contract.openapi}
      </p>
      <h1>{contract.title}</h1>
      <p>{contract.description || "服务端 OpenAPI 契约未提供总览说明。"}</p>
      <div className="api-docs-home-grid">
        <article>
          <span>服务端版本</span>
          <strong>{contract.version}</strong>
        </article>
        <article>
          <span>实际接口</span>
          <strong>{contract.operations.length}</strong>
        </article>
        <article>
          <span>资源标签</span>
          <strong>{contract.tags.length}</strong>
        </article>
      </div>
      <h2>快速开始</h2>
      <p>
        所有试运行请求从当前页面同源发送，自动使用浏览器当前身份。选择 API
        参考中的真实路由，填写必要参数后发送。
      </p>
      <Button
        aria-label={"下载服务端 OpenAPI JSON"}
        type="primary"
        href="/openapi.json"
        download="openapi.json"
      >
        下载服务端 OpenAPI JSON
      </Button>
      {sampleCode ? (
        <pre className="api-section-code">
          <code>{sampleCode}</code>
        </pre>
      ) : null}
    </section>
  );
}

export default function ApiDocsPage() {
  const session = useSession();
  const [contract, setContract] = useState<OpenApiContract | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [section, setSection] = useState<DocumentationSection>("reference");
  const [sampleCode, setSampleCode] = useState<string | null>(null);
  const onLoad = useCallback((loaded: OpenApiContract) => {
    setContract(loaded);
    setError("");
    setSampleCode(null);
  }, []);
  const onError = useCallback((message: string) => {
    setError(message);
    setContract(null);
  }, []);

  return (
    <div className="api-docs-page">
      <ContractLoader attempt={attempt} onLoad={onLoad} onError={onError} />
      <header className="api-docs-platform-header">
        <a
          className="api-docs-brand"
          href={import.meta.env.VITE_CLIENT_URL || "/"}
          aria-label="返回合同审查工作台"
        >
          <span className="api-brand-mark" aria-hidden="true">
            CA
          </span>
          <strong>合同智能审计工作台</strong>
          <span className="api-docs-platform-badge">API 开放平台</span>
        </a>
        <Tabs
          activeKey={section}
          onChange={(key) => {
            const selectedSection = SECTIONS.find((item) => item.key === key);
            if (selectedSection) setSection(selectedSection.key);
          }}
          items={SECTIONS.map((item) => ({ key: item.key, label: item.label }))}
        />
        <div className="api-docs-session">
          {session.data
            ? `${session.data.display_name} · ${session.data.role}`
            : "当前会话"}
        </div>
      </header>
      {error ? (
        <section className="api-docs-load-error" role="alert">
          <div>
            <h1>无法加载服务端 OpenAPI 契约</h1>
            <p>{error}</p>
          </div>
          <Button
            aria-label={"重新加载"}
            type="primary"
            htmlType="button"
            onClick={() => setAttempt((value) => value + 1)}
          >
            重新加载
          </Button>
        </section>
      ) : !contract ? (
        <div className="api-docs-loading" role="status">
          正在读取服务端 OpenAPI 契约…
        </div>
      ) : section === "reference" ? (
        <ApiExplorer contract={contract} onCodeChange={setSampleCode} />
      ) : (
        <SectionContent
          section={section}
          contract={contract}
          sampleCode={sampleCode}
        />
      )}
    </div>
  );
}
