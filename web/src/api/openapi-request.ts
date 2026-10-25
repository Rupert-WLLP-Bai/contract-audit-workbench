import { resolveOpenApiReference, type OpenApiOperation, type OpenApiParameter, type OpenApiRecord, type OpenApiRequestBody } from "./openapi";

export interface ApiRequestInput {
  operation: OpenApiOperation;
  values: Record<string, string>;
  bodyText: string;
  bodyValues: Record<string, string>;
  bodyFiles: Record<string, File[]>;
  origin: string;
  schemaRoot: OpenApiRecord;
}

export interface ApiRequestBuild {
  request: { url: string; init: RequestInit } | null;
  error: string | null;
}

export interface ApiRequestBodyField {
  name: string;
  description: string;
  required: boolean;
  schema: OpenApiRecord;
  file: boolean;
  multiple: boolean;
}

export type ApiRequestBodyMode = "none" | "json" | "multipart" | "urlencoded" | "binary" | "text";

function inputKey(operation: OpenApiOperation, parameter: OpenApiParameter): string {
  return `${operation.key}:${parameter.location}:${parameter.name}`;
}

function typedParameter(parameter: OpenApiParameter, rawValue: string, root: OpenApiRecord): string | number | boolean | string[] | OpenApiRecord {
  let schema: OpenApiRecord | null = null;
  if (parameter.schema) {
    const resolved = resolveOpenApiReference(parameter.schema, root);
    if (!isRecord(resolved)) throw new Error(`${parameter.name} schema 必须是对象`);
    schema = resolved;
  }
  const type = schema && typeof schema.type === "string" ? schema.type : "string";
  if (type === "integer" || type === "number") {
    if (rawValue.trim() === "") throw new Error(`${parameter.name} 需要填写数字`);
    const value = Number(rawValue);
    if (!Number.isFinite(value) || (type === "integer" && !Number.isInteger(value))) throw new Error(`${parameter.name} 的值不符合 ${type} 类型`);
    return value;
  }
  if (type === "boolean") {
    if (rawValue !== "true" && rawValue !== "false") throw new Error(`${parameter.name} 只能填写 true 或 false`);
    return rawValue === "true";
  }
  if (type === "array") return rawValue.split(",").map((value) => value.trim()).filter((value) => value !== "");
  if (type === "object") {
    let parsed: unknown;
    try { parsed = JSON.parse(rawValue); }
    catch { throw new Error(`${parameter.name} 需要填写 JSON 对象`); }
    if (!isRecord(parsed)) throw new Error(`${parameter.name} 需要填写 JSON 对象`);
    return parsed;
  }
  return rawValue;
}

function asParameterString(value: string | number | boolean | string[] | OpenApiRecord): string {
  return typeof value === "string" ? value : Array.isArray(value) ? value.join(",") : typeof value === "object" ? JSON.stringify(value) : String(value);
}

function isRecord(value: unknown): value is OpenApiRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizedMediaType(requestBody: OpenApiRequestBody | null): string {
  return requestBody?.mediaType?.split(";")[0]?.trim().toLowerCase() ?? "";
}

export function apiRequestBodyMode(requestBody: OpenApiRequestBody | null): ApiRequestBodyMode {
  if (requestBody === null) return "none";
  const mediaType = normalizedMediaType(requestBody);
  if (mediaType.includes("json")) return "json";
  if (mediaType === "multipart/form-data") return "multipart";
  if (mediaType === "application/x-www-form-urlencoded") return "urlencoded";
  if (mediaType === "application/octet-stream" || mediaType.startsWith("image/") || mediaType.startsWith("audio/") || mediaType.startsWith("video/")) return "binary";
  return "text";
}

export function apiRequestBodyFields(requestBody: OpenApiRequestBody | null, root: OpenApiRecord): ApiRequestBodyField[] {
  const mode = apiRequestBodyMode(requestBody);
  if (requestBody === null || requestBody.schema === null || (mode !== "json" && mode !== "multipart" && mode !== "urlencoded")) return [];
  const schemaValue = resolveOpenApiReference(requestBody.schema, root);
  if (!isRecord(schemaValue)) throw new Error("请求体 schema 必须是对象");
  const properties = schemaValue.properties;
  if (properties === undefined) return [];
  if (!isRecord(properties)) throw new Error("表单 schema properties 必须是对象");
  const requiredValue = schemaValue.required;
  if (requiredValue !== undefined && (!Array.isArray(requiredValue) || !requiredValue.every((item) => typeof item === "string"))) {
    throw new Error("表单 schema required 必须是字符串数组");
  }
  const required = new Set(Array.isArray(requiredValue) ? requiredValue.filter((item): item is string => typeof item === "string") : []);
  for (const name of required) if (!Object.hasOwn(properties, name)) throw new Error(`表单 schema 必填字段 ${name} 没有属性定义`);
  return Object.entries(properties).map(([name, fieldValue]) => {
    const fieldSchema = resolveOpenApiReference(fieldValue, root);
    if (!isRecord(fieldSchema)) throw new Error(`表单字段 ${name} 的 schema 必须是对象`);
    const type = typeof fieldSchema.type === "string" ? fieldSchema.type : "";
    const itemsValue = fieldSchema.items;
    const items = itemsValue === undefined ? null : resolveOpenApiReference(itemsValue, root);
    if (items !== null && !isRecord(items)) throw new Error(`表单字段 ${name} 的 items schema 必须是对象`);
    const binary = fieldSchema.format === "binary" || (type === "array" && isRecord(items) && items.format === "binary");
    if (mode === "urlencoded" && binary) throw new Error(`表单字段 ${name} 是文件，但媒体类型不支持文件上传`);
    return {
      name,
      description: typeof fieldSchema.description === "string" ? fieldSchema.description : "",
      required: required.has(name),
      schema: fieldSchema,
      file: binary,
      multiple: type === "array" && binary,
    };
  });
}

function matchesSchemaType(value: unknown, type: string): boolean {
  if (type === "object") return isRecord(value);
  if (type === "array") return Array.isArray(value);
  if (type === "string") return typeof value === "string";
  if (type === "integer") return typeof value === "number" && Number.isInteger(value);
  if (type === "number") return typeof value === "number" && Number.isFinite(value);
  if (type === "boolean") return typeof value === "boolean";
  return type === "null" && value === null;
}

function validateJsonSchema(value: unknown, schema: OpenApiRecord, path: string, root: OpenApiRecord, depth = 0): string | null {
  if (depth > 64) return `${path} 的 schema 递归层级超出限制`;
  if (typeof schema.$ref === "string") {
    let resolved: unknown;
    try { resolved = resolveOpenApiReference(schema, root); }
    catch (failure) { return failure instanceof Error ? failure.message : `${path} 的 schema 引用无效`; }
    if (!isRecord(resolved)) return `${path} 的 schema 引用没有指向对象`;
    return validateJsonSchema(value, resolved, path, root, depth + 1);
  }

  const types = typeof schema.type === "string"
    ? [schema.type]
    : Array.isArray(schema.type) && schema.type.every((type): type is string => typeof type === "string")
      ? schema.type
      : [];
  if (value === null && (schema.nullable === true || types.includes("null"))) return null;
  if (types.length > 0 && !types.some((type) => matchesSchemaType(value, type))) return `${path} 类型不符合 OpenAPI schema`;
  if (Object.hasOwn(schema, "const") && JSON.stringify(value) !== JSON.stringify(schema.const)) return `${path} 不符合 schema const`;
  if (Array.isArray(schema.enum) && !schema.enum.some((entry) => JSON.stringify(entry) === JSON.stringify(value))) return `${path} 不在 schema enum 允许值中`;

  if (isRecord(value) && (types.includes("object") || isRecord(schema.properties) || Array.isArray(schema.required))) {
    if (Array.isArray(schema.required)) {
      for (const key of schema.required) {
        if (typeof key !== "string") return `${path} 的 schema required 无效`;
        if (!Object.hasOwn(value, key)) return `${path}.${key} 是必填字段`;
      }
    } else if (Object.hasOwn(schema, "required")) return `${path} 的 schema required 无效`;
    const properties = schema.properties;
    if (properties !== undefined && !isRecord(properties)) return `${path} 的 schema properties 无效`;
    if (isRecord(properties)) {
      for (const [key, childSchema] of Object.entries(properties)) {
        if (!Object.hasOwn(value, key)) continue;
        if (!isRecord(childSchema)) return `${path}.${key} 的 schema 无效`;
        const error = validateJsonSchema(value[key], childSchema, `${path}.${key}`, root, depth + 1);
        if (error) return error;
      }
    }
    if (schema.additionalProperties === false && isRecord(properties)) {
      const unknownKey = Object.keys(value).find((key) => !Object.hasOwn(properties, key));
      if (unknownKey) return `${path}.${unknownKey} 未在 schema 中声明`;
    }
  }

  if (Array.isArray(value) && (types.includes("array") || schema.items !== undefined)) {
    if (schema.items !== undefined && !isRecord(schema.items)) return `${path} 的 schema items 无效`;
    if (isRecord(schema.items)) {
      for (let index = 0; index < value.length; index += 1) {
        const error = validateJsonSchema(value[index], schema.items, `${path}[${index}]`, root, depth + 1);
        if (error) return error;
      }
    }
  }
  if (Array.isArray(schema.allOf)) {
    for (const part of schema.allOf) {
      if (!isRecord(part)) return `${path} 的 schema allOf 无效`;
      const error = validateJsonSchema(value, part, path, root, depth + 1);
      if (error) return error;
    }
  }
  if (Array.isArray(schema.anyOf) && !schema.anyOf.some((part) => isRecord(part) && validateJsonSchema(value, part, path, root, depth + 1) === null)) return `${path} 不符合 schema anyOf`;
  if (Array.isArray(schema.oneOf)) {
    const matches = schema.oneOf.filter((part) => isRecord(part) && validateJsonSchema(value, part, path, root, depth + 1) === null).length;
    if (matches !== 1) return `${path} 不符合 schema oneOf`;
  }
  return null;
}

function serializeFormValue(rawValue: string, schema: OpenApiRecord, path: string, root: OpenApiRecord): string {
  let value: unknown = rawValue;
  const type = typeof schema.type === "string" ? schema.type : "string";
  if (type === "integer" || type === "number") {
    if (rawValue.trim() === "") throw new Error(`${path} 需要填写数字`);
    const numeric = Number(rawValue);
    if (!Number.isFinite(numeric) || (type === "integer" && !Number.isInteger(numeric))) throw new Error(`${path} 的值不符合 ${type} 类型`);
    value = numeric;
  } else if (type === "boolean") {
    if (rawValue !== "true" && rawValue !== "false") throw new Error(`${path} 只能填写 true 或 false`);
    value = rawValue === "true";
  } else if (type === "object") {
    try { value = JSON.parse(rawValue); }
    catch { throw new Error(`${path} 需要填写 JSON 对象`); }
  } else if (type === "array") throw new Error(`${path} 的数组 items schema 缺失`);
  const error = validateJsonSchema(value, schema, path, root);
  if (error) throw new Error(error);
  return typeof value === "string" ? value : JSON.stringify(value);
}

function replaceHeader(headers: Record<string, string>, name: string, value: string | null): void {
  for (const current of Object.keys(headers)) if (current.toLowerCase() === name.toLowerCase()) delete headers[current];
  if (value !== null) headers[name] = value;
}

export function apiParameterInputKey(operation: OpenApiOperation, parameter: OpenApiParameter): string {
  return inputKey(operation, parameter);
}

export function buildApiRequest({ operation, values, bodyText, bodyValues, bodyFiles, origin, schemaRoot }: ApiRequestInput): ApiRequestBuild {
  let path = operation.path;
  const queryValues: { name: string; value: string | number | boolean | string[] | OpenApiRecord }[] = [];
  const headers: Record<string, string> = { Accept: "application/json" };
  for (const parameter of operation.parameters) {
    if (parameter.location === "cookie") {
      if (parameter.required) return { request: null, error: `必填 Cookie 参数 ${parameter.name} 由浏览器当前会话管理，不能在此手动覆盖。` };
      continue;
    }
    const rawValue = values[inputKey(operation, parameter)] ?? "";
    if (parameter.required && rawValue.trim() === "") return { request: null, error: `请填写必填参数：${parameter.name}` };
    if (rawValue.trim() === "") continue;
    let value: string | number | boolean | string[] | OpenApiRecord;
    try { value = typedParameter(parameter, rawValue, schemaRoot); }
    catch (failure) { return { request: null, error: failure instanceof Error ? failure.message : `参数 ${parameter.name} 无效` }; }
    if (parameter.location === "path") {
      const placeholder = `{${parameter.name}}`;
      if (!path.includes(placeholder)) return { request: null, error: `契约路径缺少参数占位符 ${placeholder}` };
      path = path.replaceAll(placeholder, encodeURIComponent(asParameterString(value)));
    } else if (parameter.location === "query") {
      queryValues.push({ name: parameter.name, value });
    } else {
      headers[parameter.name] = asParameterString(value);
    }
  }
  if (/[{}]/.test(path)) return { request: null, error: "路径仍包含未填写的参数" };
  if (path.startsWith("//") || !path.startsWith("/")) return { request: null, error: "请求路径必须是契约中的同源绝对路径" };
  let url: URL;
  try { url = new URL(path, origin); }
  catch { return { request: null, error: "请求路径无效" }; }
  if (url.origin !== origin) return { request: null, error: "请求只能发送到当前服务源" };
  for (const entry of queryValues) {
    if (Array.isArray(entry.value)) {
      for (const item of entry.value) url.searchParams.append(entry.name, item);
    } else url.searchParams.append(entry.name, asParameterString(entry.value));
  }

  let body: BodyInit | undefined;
  if (operation.requestBody) {
    const requestBody = operation.requestBody;
    const mode = apiRequestBodyMode(requestBody);
    if (mode === "multipart" || mode === "urlencoded") {
      if (requestBody.required && requestBody.schema === null) return { request: null, error: "必填表单请求体没有可用的 OpenAPI schema。" };
      let fields: ApiRequestBodyField[];
      try { fields = apiRequestBodyFields(requestBody, schemaRoot); }
      catch (failure) { return { request: null, error: failure instanceof Error ? failure.message : "表单 schema 无效。" }; }
      const form = mode === "multipart" ? new FormData() : new URLSearchParams();
      for (const field of fields) {
        if (field.file) {
          const files = bodyFiles[field.name] ?? [];
          if (field.required && files.length === 0) return { request: null, error: `请选择必填文件：${field.name}` };
          if (!field.multiple && files.length > 1) return { request: null, error: `${field.name} 只接受一个文件` };
          if (mode !== "multipart" || !(form instanceof FormData)) return { request: null, error: `${field.name} 的文件字段必须使用 multipart/form-data` };
          for (const file of files) form.append(field.name, file, file.name);
          continue;
        }
        const rawValue = bodyValues[field.name] ?? "";
        if (field.required && rawValue.trim() === "") return { request: null, error: `请求体.${field.name} 是必填字段` };
        if (rawValue.trim() === "") continue;
        const fieldType = typeof field.schema.type === "string" ? field.schema.type : "string";
        const values = fieldType === "array" ? rawValue.split(",").map((item) => item.trim()).filter((item) => item !== "") : [rawValue];
        const itemValue = fieldType === "array" ? field.schema.items : field.schema;
        let itemSchema: OpenApiRecord = { type: "string" };
        if (itemValue !== undefined) {
          let resolved: unknown;
          try { resolved = resolveOpenApiReference(itemValue, schemaRoot); }
          catch (failure) { return { request: null, error: failure instanceof Error ? failure.message : `表单字段 ${field.name} schema 无效` }; }
          if (!isRecord(resolved)) return { request: null, error: `表单字段 ${field.name} schema 无效` };
          itemSchema = resolved;
        }
        if (fieldType === "array" && values.length === 0 && field.required) return { request: null, error: `请求体.${field.name} 至少需要一个值` };
        for (const value of values) {
          let serialized: string;
          try { serialized = serializeFormValue(value, itemSchema, `请求体.${field.name}`, schemaRoot); }
          catch (failure) { return { request: null, error: failure instanceof Error ? failure.message : `请求体.${field.name} 无效` }; }
          form.append(field.name, serialized);
        }
      }
      if (requestBody.required || [...form.keys()].length > 0) body = form;
      replaceHeader(headers, "Content-Type", mode === "urlencoded" && body !== undefined ? requestBody.mediaType : null);
    } else if (mode === "binary") {
      const files = bodyFiles.__request_body__ ?? [];
      if (requestBody.required && files.length === 0) return { request: null, error: "请选择请求原件。" };
      if (files.length > 1) return { request: null, error: "此操作只接受一个请求原件。" };
      if (files[0]) body = files[0];
      replaceHeader(headers, "Content-Type", body === undefined ? null : requestBody.mediaType);
    } else {
      if (bodyText.trim() === "" && requestBody.required) return { request: null, error: mode === "json" ? "此操作要求请求体，请填写真实请求 JSON。" : "此操作要求请求体，请填写真实请求内容。" };
      if (bodyText.trim() !== "") {
        if (mode === "json") {
          let parsed: unknown;
          try { parsed = JSON.parse(bodyText); }
          catch { return { request: null, error: "请求体不是有效 JSON。" }; }
          if (requestBody.schema) {
            const schemaError = validateJsonSchema(parsed, requestBody.schema, "请求体", schemaRoot);
            if (schemaError) return { request: null, error: schemaError };
          }
          body = JSON.stringify(parsed);
        } else body = bodyText;
        replaceHeader(headers, "Content-Type", requestBody.mediaType);
      }
    }
  }

  return {
    request: {
      url: `${url.pathname}${url.search}`,
      init: { method: operation.method, credentials: "same-origin", cache: "no-store", headers, ...(body === undefined ? {} : { body }) },
    },
    error: null,
  };
}

export function apiRequestCode(operation: OpenApiOperation, request: { url: string; init: RequestInit } | null, schemaRoot: OpenApiRecord): string | null {
  if (!request) return null;
  const headers = Object.fromEntries(new Headers(request.init.headers).entries());
  const body = request.init.body;
  const setup: string[] = [];
  let bodyExpression: string | null = null;
  if (body instanceof FormData) {
    setup.push("const body = new FormData();");
    const seenFiles = new Set<string>();
    let inputIndex = 0;
    let fields: ApiRequestBodyField[];
    try { fields = apiRequestBodyFields(operation.requestBody, schemaRoot); }
    catch { return null; }
    for (const [name, value] of body.entries()) {
      if (typeof value === "string") {
        setup.push(`body.append(${JSON.stringify(name)}, ${JSON.stringify(value)});`);
        continue;
      }
      if (seenFiles.has(name)) continue;
      seenFiles.add(name);
      const field = fields.find((entry) => entry.name === name);
      const input = `fileInput${inputIndex}`;
      inputIndex += 1;
      setup.push(`const ${input} = Array.from(document.querySelectorAll('input[type="file"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === ${JSON.stringify(name)});`);
      if (field?.multiple) setup.push(`for (const file of ${input}?.files ?? []) body.append(${JSON.stringify(name)}, file);`);
      else {
        const file = `file${inputIndex}`;
        setup.push(`const ${file} = ${input}?.files?.[0];`);
        setup.push(`if (!${file}) throw new Error(${JSON.stringify(`请选择文件：${name}`)});`);
        setup.push(`body.append(${JSON.stringify(name)}, ${file});`);
      }
    }
    bodyExpression = "body";
  } else if (body instanceof URLSearchParams) {
    setup.push("const body = new URLSearchParams();");
    for (const [name, value] of body.entries()) setup.push(`body.append(${JSON.stringify(name)}, ${JSON.stringify(value)});`);
    bodyExpression = "body";
  } else if (typeof Blob !== "undefined" && body instanceof Blob) {
    setup.push("const requestFileInput = Array.from(document.querySelectorAll('input[type=\"file\"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === \"__request_body__\");");
    setup.push("const body = requestFileInput?.files?.[0];");
    setup.push("if (!body) throw new Error(\"请选择请求原件\");");
    bodyExpression = "body";
  } else if (typeof body === "string") bodyExpression = JSON.stringify(body);
  const headersCode = JSON.stringify(headers, null, 2).replaceAll("\n", "\n  ");
  const initLines = [`  method: ${JSON.stringify(operation.method)},`, `  credentials: "same-origin",`, `  headers: ${headersCode},`];
  if (bodyExpression !== null) initLines.push(`  body: ${bodyExpression},`);
  const initCode = ` {\n${initLines.join("\n")}\n}`;
  const prelude = setup.length > 0 ? `${setup.join("\n")}\n` : "";
  return `${prelude}const response = await fetch(${JSON.stringify(request.url)},${initCode});\nconst rawResponse = await response.text();\nconsole.log(response.status, rawResponse);`;
}
