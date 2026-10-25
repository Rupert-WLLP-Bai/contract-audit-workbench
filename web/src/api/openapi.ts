export type OpenApiRecord = Record<string, unknown>;
export type OpenApiParameterLocation = "path" | "query" | "header" | "cookie";

export interface OpenApiParameter {
  name: string;
  location: OpenApiParameterLocation;
  required: boolean;
  description: string;
  schema: OpenApiRecord | null;
  example: unknown;
  raw: unknown;
}

export interface OpenApiRequestBody {
  required: boolean;
  description: string;
  mediaType: string | null;
  schema: OpenApiRecord | null;
  example: unknown;
  raw: unknown;
}

export interface OpenApiResponse {
  status: string;
  description: string;
  raw: unknown;
}

export interface OpenApiOperation {
  key: string;
  method: string;
  path: string;
  operationId: string;
  summary: string;
  description: string;
  tags: string[];
  parameters: OpenApiParameter[];
  requestBody: OpenApiRequestBody | null;
  responses: OpenApiResponse[];
  security: unknown;
  raw: unknown;
}

export interface OpenApiContract {
  openapi: string;
  title: string;
  version: string;
  description: string;
  tags: { name: string; description: string }[];
  operations: OpenApiOperation[];
  raw: OpenApiRecord;
}

const METHODS: Record<string, true> = { get: true, post: true, put: true, patch: true, delete: true, head: true, options: true, trace: true };

function recordOf(value: unknown, label: string): OpenApiRecord {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(`${label} 必须是对象`);
  return value as OpenApiRecord;
}

function stringValue(value: unknown, label: string): string {
  if (typeof value !== "string") throw new Error(`${label} 必须是字符串`);
  return value;
}

function optionalString(record: OpenApiRecord, key: string, fallback = ""): string {
  if (!Object.hasOwn(record, key) || record[key] === undefined) return fallback;
  return stringValue(record[key], key);
}

function optionalBoolean(record: OpenApiRecord, key: string, fallback = false): boolean {
  if (!Object.hasOwn(record, key) || record[key] === undefined) return fallback;
  const value = record[key];
  if (typeof value !== "boolean") throw new Error(`${key} 必须是布尔值`);
  return value;
}

function arrayValue(value: unknown, label: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(`${label} 必须是数组`);
  return value;
}

export function resolveOpenApiReference(value: unknown, root: OpenApiRecord): unknown {
  let current = value;
  const visited = new Set<string>();
  while (typeof current === "object" && current !== null && !Array.isArray(current)) {
    const reference = (current as OpenApiRecord).$ref;
    if (typeof reference !== "string") return current;
    if (!reference.startsWith("#/")) throw new Error(`不支持外部 OpenAPI 引用：${reference}`);
    if (visited.has(reference)) throw new Error(`OpenAPI 引用存在循环：${reference}`);
    visited.add(reference);
    let target: unknown = root;
    for (const encodedPart of reference.slice(2).split("/")) {
      const part = encodedPart.replaceAll("~1", "/").replaceAll("~0", "~");
      const parent = recordOf(target, `引用 ${reference}`);
      if (!Object.hasOwn(parent, part)) throw new Error(`无法解析 OpenAPI 引用：${reference}`);
      target = parent[part];
    }
    current = target;
  }
  return current;
}

function parseParameters(value: unknown, root: OpenApiRecord, label: string): OpenApiParameter[] {
  if (value === undefined) return [];
  return arrayValue(value, label).map((entry) => {
    const raw = resolveOpenApiReference(entry, root);
    const parameter = recordOf(raw, "参数");
    const location = stringValue(parameter.in, "参数位置");
    if (location !== "path" && location !== "query" && location !== "header" && location !== "cookie") throw new Error(`不支持的参数位置：${location}`);
    const schema = parameter.schema === undefined ? null : recordOf(parameter.schema, "参数 schema");
    const example = Object.hasOwn(parameter, "example") ? parameter.example : schema?.example;
    return {
      name: stringValue(parameter.name, "参数名称"),
      location,
      required: location === "path" || optionalBoolean(parameter, "required"),
      description: optionalString(parameter, "description"),
      schema,
      example: example ?? null,
      raw,
    };
  });
}

function parseRequestBody(value: unknown, root: OpenApiRecord): OpenApiRequestBody | null {
  if (value === undefined) return null;
  const raw = resolveOpenApiReference(value, root);
  const requestBody = recordOf(raw, "请求体");
  const content = requestBody.content === undefined ? {} : recordOf(requestBody.content, "请求体 content");
  const entries = Object.entries(content);
  const selected = entries.find(([mediaType]) => mediaType === "application/json") ?? entries[0] ?? null;
  if (!selected) return {
    required: optionalBoolean(requestBody, "required"),
    description: optionalString(requestBody, "description"),
    mediaType: null,
    schema: null,
    example: null,
    raw,
  };
  const [mediaType, rawMedia] = selected;
  const media = recordOf(rawMedia, "请求体 media type");
  const schema = media.schema === undefined ? null : recordOf(media.schema, "请求体 schema");
  let example: unknown = Object.hasOwn(media, "example") ? media.example : null;
  if (example === null && media.examples !== undefined) {
    const examples = recordOf(media.examples, "请求体 examples");
    const firstExample = Object.values(examples)[0];
    if (firstExample !== undefined) {
      const firstRecord = recordOf(firstExample, "请求体 example");
      if (Object.hasOwn(firstRecord, "value")) example = firstRecord.value;
    }
  }
  if (example === null && schema) {
    if (Object.hasOwn(schema, "example")) example = schema.example;
    else if (Object.hasOwn(schema, "default")) example = schema.default;
  }
  return {
    required: optionalBoolean(requestBody, "required"),
    description: optionalString(requestBody, "description"),
    mediaType,
    schema,
    example,
    raw,
  };
}

function parseResponses(value: unknown, root: OpenApiRecord): OpenApiResponse[] {
  const responses = recordOf(value, "responses");
  return Object.entries(responses).map(([status, entry]) => {
    const raw = resolveOpenApiReference(entry, root);
    const response = recordOf(raw, `responses.${status}`);
    return { status, description: optionalString(response, "description", "响应说明未提供"), raw };
  });
}

function parseOperation(method: string, path: string, pathItem: OpenApiRecord, rawOperation: unknown, root: OpenApiRecord): OpenApiOperation {
  const operation = recordOf(rawOperation, `${method.toUpperCase()} ${path}`);
  const pathParameters = parseParameters(pathItem.parameters, root, `${path} parameters`);
  const operationParameters = parseParameters(operation.parameters, root, `${method.toUpperCase()} ${path} parameters`);
  const parameterMap = new Map<string, OpenApiParameter>();
  for (const parameter of [...pathParameters, ...operationParameters]) parameterMap.set(`${parameter.location}:${parameter.name}`, parameter);
  const operationId = optionalString(operation, "operationId", `${method.toUpperCase()} ${path}`);
  const tags = operation.tags === undefined ? [] : arrayValue(operation.tags, "operation tags").map((tag) => stringValue(tag, "operation tag"));
  const responses = parseResponses(operation.responses, root);
  return {
    key: `${method.toUpperCase()} ${path}`,
    method: method.toUpperCase(),
    path,
    operationId,
    summary: optionalString(operation, "summary", operationId),
    description: optionalString(operation, "description"),
    tags,
    parameters: [...parameterMap.values()],
    requestBody: parseRequestBody(operation.requestBody, root),
    responses,
    security: Object.hasOwn(operation, "security") ? operation.security : null,
    raw: operation,
  };
}

export function parseOpenApiContract(value: unknown): OpenApiContract {
  const root = recordOf(value, "OpenAPI 契约");
  const openapi = stringValue(root.openapi, "openapi");
  if (!openapi.startsWith("3.")) throw new Error("仅支持 OpenAPI 3.x 契约");
  const info = recordOf(root.info, "info");
  const paths = recordOf(root.paths, "paths");
  const operations: OpenApiOperation[] = [];
  for (const [path, rawPathItem] of Object.entries(paths)) {
    if (!path.startsWith("/") || path.startsWith("//") || path.includes("?") || path.includes("#")) throw new Error(`OpenAPI 路径无效：${path}`);
    const pathItem = recordOf(rawPathItem, `paths.${path}`);
    for (const [method, rawOperation] of Object.entries(pathItem)) {
      if (METHODS[method.toLowerCase()] === true) operations.push(parseOperation(method.toLowerCase(), path, pathItem, rawOperation, root));
    }
  }
  if (operations.length === 0) throw new Error("OpenAPI 契约没有可用操作");
  const tags = root.tags === undefined ? [] : arrayValue(root.tags, "tags").map((entry) => {
    const tag = recordOf(entry, "tag");
    return { name: stringValue(tag.name, "tag.name"), description: optionalString(tag, "description") };
  });
  return {
    openapi,
    title: stringValue(info.title, "info.title"),
    version: stringValue(info.version, "info.version"),
    description: optionalString(info, "description"),
    tags,
    operations,
    raw: root,
  };
}
