import {
  resolveOpenApiReference,
  type OpenApiOperation,
  type OpenApiParameter,
  type OpenApiRecord,
  type OpenApiRequestBody,
} from "./openapi";

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

export type ApiRequestBodyMode =
  | "none"
  | "json"
  | "multipart"
  | "urlencoded"
  | "binary"
  | "text";

type ParameterValue = string | number | boolean | string[] | OpenApiRecord;

function inputKey(
  operation: OpenApiOperation,
  parameter: OpenApiParameter,
): string {
  return `${operation.key}:${parameter.location}:${parameter.name}`;
}

function isRecord(value: unknown): value is OpenApiRecord {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function failureMessage(failure: unknown, fallback: string): string {
  return failure instanceof Error ? failure.message : fallback;
}

function parameterSchema(
  parameter: OpenApiParameter,
  root: OpenApiRecord,
): OpenApiRecord | null {
  if (!parameter.schema) return null;
  const resolved = resolveOpenApiReference(parameter.schema, root);
  if (!isRecord(resolved)) throw new Error(`${parameter.name} schema 必须是对象`);
  return resolved;
}

function parameterType(schema: OpenApiRecord | null): string {
  return schema !== null && typeof schema.type === "string"
    ? schema.type
    : "string";
}

function numericText(rawValue: string, type: string, label: string): number {
  if (rawValue.trim() === "") throw new Error(`${label} 需要填写数字`);
  const value = Number(rawValue);
  if (!Number.isFinite(value) || (type === "integer" && !Number.isInteger(value)))
    throw new Error(`${label} 的值不符合 ${type} 类型`);
  return value;
}

function booleanText(rawValue: string, label: string): boolean {
  if (rawValue !== "true" && rawValue !== "false")
    throw new Error(`${label} 只能填写 true 或 false`);
  return rawValue === "true";
}

function arrayParameter(rawValue: string): string[] {
  return rawValue
    .split(",")
    .map((value) => value.trim())
    .filter((value) => value !== "");
}

function objectParameter(rawValue: string, name: string): OpenApiRecord {
  let parsed: unknown;
  try {
    parsed = JSON.parse(rawValue);
  } catch {
    throw new Error(`${name} 需要填写 JSON 对象`);
  }
  if (!isRecord(parsed)) throw new Error(`${name} 需要填写 JSON 对象`);
  return parsed;
}

function typedParameter(
  parameter: OpenApiParameter,
  rawValue: string,
  root: OpenApiRecord,
): ParameterValue {
  const schema = parameterSchema(parameter, root);
  const type = parameterType(schema);
  if (type === "integer" || type === "number")
    return numericText(rawValue, type, parameter.name);
  if (type === "boolean") return booleanText(rawValue, parameter.name);
  if (type === "array") return arrayParameter(rawValue);
  if (type === "object") return objectParameter(rawValue, parameter.name);
  return rawValue;
}

function asParameterString(value: ParameterValue): string {
  return typeof value === "string"
    ? value
    : Array.isArray(value)
      ? value.join(",")
      : typeof value === "object"
        ? JSON.stringify(value)
        : String(value);
}

function normalizedMediaType(requestBody: OpenApiRequestBody | null): string {
  return requestBody?.mediaType?.split(";")[0]?.trim().toLowerCase() ?? "";
}

function isBinaryMediaType(mediaType: string): boolean {
  return (
    mediaType === "application/octet-stream" ||
    mediaType.startsWith("image/") ||
    mediaType.startsWith("audio/") ||
    mediaType.startsWith("video/")
  );
}

export function apiRequestBodyMode(
  requestBody: OpenApiRequestBody | null,
): ApiRequestBodyMode {
  if (requestBody === null) return "none";
  const mediaType = normalizedMediaType(requestBody);
  if (mediaType.includes("json")) return "json";
  if (mediaType === "multipart/form-data") return "multipart";
  if (mediaType === "application/x-www-form-urlencoded") return "urlencoded";
  if (isBinaryMediaType(mediaType)) return "binary";
  return "text";
}

function isFormMode(mode: ApiRequestBodyMode): boolean {
  return mode === "json" || mode === "multipart" || mode === "urlencoded";
}

function formSchemaValue(
  schema: OpenApiRecord,
  root: OpenApiRecord,
): OpenApiRecord {
  const schemaValue = resolveOpenApiReference(schema, root);
  if (!isRecord(schemaValue)) throw new Error("请求体 schema 必须是对象");
  return schemaValue;
}

function formRequiredNames(
  schemaValue: OpenApiRecord,
  properties: OpenApiRecord,
): Set<string> {
  const requiredValue = schemaValue.required;
  if (
    requiredValue !== undefined &&
    (!Array.isArray(requiredValue) ||
      !requiredValue.every((item) => typeof item === "string"))
  )
    throw new Error("表单 schema required 必须是字符串数组");
  const required = new Set(
    Array.isArray(requiredValue)
      ? requiredValue.filter((item): item is string => typeof item === "string")
      : [],
  );
  for (const name of required)
    if (!Object.hasOwn(properties, name))
      throw new Error(`表单 schema 必填字段 ${name} 没有属性定义`);
  return required;
}

function formFieldItems(
  fieldSchema: OpenApiRecord,
  root: OpenApiRecord,
  name: string,
): OpenApiRecord | null {
  if (fieldSchema.items === undefined) return null;
  const items = resolveOpenApiReference(fieldSchema.items, root);
  if (!isRecord(items))
    throw new Error(`表单字段 ${name} 的 items schema 必须是对象`);
  return items;
}

function isBinaryField(
  fieldSchema: OpenApiRecord,
  type: string,
  items: OpenApiRecord | null,
): boolean {
  return (
    fieldSchema.format === "binary" ||
    (type === "array" && items !== null && items.format === "binary")
  );
}

function formField(
  name: string,
  fieldValue: unknown,
  root: OpenApiRecord,
  required: Set<string>,
  mode: ApiRequestBodyMode,
): ApiRequestBodyField {
  const fieldSchema = resolveOpenApiReference(fieldValue, root);
  if (!isRecord(fieldSchema))
    throw new Error(`表单字段 ${name} 的 schema 必须是对象`);
  const type = typeof fieldSchema.type === "string" ? fieldSchema.type : "";
  const items = formFieldItems(fieldSchema, root, name);
  const binary = isBinaryField(fieldSchema, type, items);
  if (mode === "urlencoded" && binary)
    throw new Error(`表单字段 ${name} 是文件，但媒体类型不支持文件上传`);
  return {
    name,
    description:
      typeof fieldSchema.description === "string"
        ? fieldSchema.description
        : "",
    required: required.has(name),
    schema: fieldSchema,
    file: binary,
    multiple: type === "array" && binary,
  };
}

export function apiRequestBodyFields(
  requestBody: OpenApiRequestBody | null,
  root: OpenApiRecord,
): ApiRequestBodyField[] {
  if (requestBody === null || requestBody.schema === null) return [];
  const mode = apiRequestBodyMode(requestBody);
  if (!isFormMode(mode)) return [];
  const schemaValue = formSchemaValue(requestBody.schema, root);
  const properties = schemaValue.properties;
  if (properties === undefined) return [];
  if (!isRecord(properties))
    throw new Error("表单 schema properties 必须是对象");
  const required = formRequiredNames(schemaValue, properties);
  return Object.entries(properties).map(([name, fieldValue]) =>
    formField(name, fieldValue, root, required, mode),
  );
}

const schemaTypeMatchers: Record<string, (value: unknown) => boolean> = {
  object: (value) => isRecord(value),
  array: (value) => Array.isArray(value),
  string: (value) => typeof value === "string",
  integer: (value) => typeof value === "number" && Number.isInteger(value),
  number: (value) => typeof value === "number" && Number.isFinite(value),
  boolean: (value) => typeof value === "boolean",
  null: (value) => value === null,
};

function matchesSchemaType(value: unknown, type: string): boolean {
  return schemaTypeMatchers[type]?.(value) ?? false;
}

function schemaTypes(schema: OpenApiRecord): string[] {
  if (typeof schema.type === "string") return [schema.type];
  if (
    Array.isArray(schema.type) &&
    schema.type.every((type): type is string => typeof type === "string")
  )
    return schema.type;
  return [];
}

function validateSchemaType(
  value: unknown,
  types: string[],
  path: string,
): string | null {
  if (types.length > 0 && !types.some((type) => matchesSchemaType(value, type)))
    return `${path} 类型不符合 OpenAPI schema`;
  return null;
}

function validateConstAndEnum(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
): string | null {
  if (
    Object.hasOwn(schema, "const") &&
    JSON.stringify(value) !== JSON.stringify(schema.const)
  )
    return `${path} 不符合 schema const`;
  if (
    Array.isArray(schema.enum) &&
    !schema.enum.some(
      (entry) => JSON.stringify(entry) === JSON.stringify(value),
    )
  )
    return `${path} 不在 schema enum 允许值中`;
  return null;
}

function isObjectSchemaTarget(
  value: unknown,
  types: string[],
  schema: OpenApiRecord,
): boolean {
  return (
    isRecord(value) &&
    (types.includes("object") ||
      isRecord(schema.properties) ||
      Array.isArray(schema.required))
  );
}

function validateRequiredKeyword(
  value: Record<string, unknown>,
  schema: OpenApiRecord,
  path: string,
): string | null {
  if (!Array.isArray(schema.required)) {
    if (Object.hasOwn(schema, "required"))
      return `${path} 的 schema required 无效`;
    return null;
  }
  for (const key of schema.required) {
    if (typeof key !== "string") return `${path} 的 schema required 无效`;
    if (!Object.hasOwn(value, key)) return `${path}.${key} 是必填字段`;
  }
  return null;
}

function validateAdditionalProperties(
  value: Record<string, unknown>,
  properties: OpenApiRecord,
  schema: OpenApiRecord,
  path: string,
): string | null {
  if (schema.additionalProperties !== false) return null;
  const unknownKey = Object.keys(value).find(
    (key) => !Object.hasOwn(properties, key),
  );
  return unknownKey ? `${path}.${unknownKey} 未在 schema 中声明` : null;
}

function validatePropertyEntries(
  value: Record<string, unknown>,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  const properties = schema.properties;
  if (properties !== undefined && !isRecord(properties))
    return `${path} 的 schema properties 无效`;
  if (!isRecord(properties)) return null;
  for (const [key, childSchema] of Object.entries(properties)) {
    if (!Object.hasOwn(value, key)) continue;
    if (!isRecord(childSchema)) return `${path}.${key} 的 schema 无效`;
    const error = validateJsonSchema(
      value[key],
      childSchema,
      `${path}.${key}`,
      root,
      depth + 1,
    );
    if (error) return error;
  }
  return validateAdditionalProperties(value, properties, schema, path);
}

function validateObjectKeywords(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
  types: string[],
): string | null {
  if (!isObjectSchemaTarget(value, types, schema)) return null;
  const record = value as Record<string, unknown>;
  const requiredError = validateRequiredKeyword(record, schema, path);
  if (requiredError) return requiredError;
  return validatePropertyEntries(record, schema, path, root, depth);
}

function isArraySchemaTarget(
  value: unknown,
  types: string[],
  schema: OpenApiRecord,
): boolean {
  return (
    Array.isArray(value) &&
    (types.includes("array") || schema.items !== undefined)
  );
}

function validateArrayItems(
  value: unknown[],
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  if (schema.items !== undefined && !isRecord(schema.items))
    return `${path} 的 schema items 无效`;
  if (!isRecord(schema.items)) return null;
  for (let index = 0; index < value.length; index += 1) {
    const error = validateJsonSchema(
      value[index],
      schema.items,
      `${path}[${index}]`,
      root,
      depth + 1,
    );
    if (error) return error;
  }
  return null;
}

function validateArrayKeywords(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
  types: string[],
): string | null {
  if (!isArraySchemaTarget(value, types, schema)) return null;
  return validateArrayItems(value as unknown[], schema, path, root, depth);
}

function validateAllOf(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  if (!Array.isArray(schema.allOf)) return null;
  for (const part of schema.allOf) {
    if (!isRecord(part)) return `${path} 的 schema allOf 无效`;
    const error = validateJsonSchema(value, part, path, root, depth + 1);
    if (error) return error;
  }
  return null;
}

function validateAnyOf(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  if (!Array.isArray(schema.anyOf)) return null;
  if (
    !schema.anyOf.some(
      (part) =>
        isRecord(part) &&
        validateJsonSchema(value, part, path, root, depth + 1) === null,
    )
  )
    return `${path} 不符合 schema anyOf`;
  return null;
}

function validateOneOf(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  if (!Array.isArray(schema.oneOf)) return null;
  const matches = schema.oneOf.filter(
    (part) =>
      isRecord(part) &&
      validateJsonSchema(value, part, path, root, depth + 1) === null,
  ).length;
  if (matches !== 1) return `${path} 不符合 schema oneOf`;
  return null;
}

function validateSchemaReference(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
): string | null {
  let resolved: unknown;
  try {
    resolved = resolveOpenApiReference(schema, root);
  } catch (failure) {
    return failure instanceof Error
      ? failure.message
      : `${path} 的 schema 引用无效`;
  }
  if (!isRecord(resolved)) return `${path} 的 schema 引用没有指向对象`;
  return validateJsonSchema(value, resolved, path, root, depth + 1);
}

function validateValueKeywords(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  types: string[],
): string | null {
  const typeError = validateSchemaType(value, types, path);
  if (typeError) return typeError;
  return validateConstAndEnum(value, schema, path);
}

function validateNestedKeywords(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth: number,
  types: string[],
): string | null {
  const objectError = validateObjectKeywords(
    value,
    schema,
    path,
    root,
    depth,
    types,
  );
  if (objectError) return objectError;
  const arrayError = validateArrayKeywords(
    value,
    schema,
    path,
    root,
    depth,
    types,
  );
  if (arrayError) return arrayError;
  const allOfError = validateAllOf(value, schema, path, root, depth);
  if (allOfError) return allOfError;
  const anyOfError = validateAnyOf(value, schema, path, root, depth);
  if (anyOfError) return anyOfError;
  return validateOneOf(value, schema, path, root, depth);
}

function validateJsonSchema(
  value: unknown,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
  depth = 0,
): string | null {
  if (depth > 64) return `${path} 的 schema 递归层级超出限制`;
  if (typeof schema.$ref === "string")
    return validateSchemaReference(value, schema, path, root, depth);
  const types = schemaTypes(schema);
  if (value === null && (schema.nullable === true || types.includes("null")))
    return null;
  const valueError = validateValueKeywords(value, schema, path, types);
  if (valueError) return valueError;
  return validateNestedKeywords(value, schema, path, root, depth, types);
}

function objectFormValue(rawValue: string, path: string): unknown {
  try {
    return JSON.parse(rawValue);
  } catch {
    throw new Error(`${path} 需要填写 JSON 对象`);
  }
}

function formScalarValue(
  rawValue: string,
  type: string,
  path: string,
): unknown {
  if (type === "integer" || type === "number")
    return numericText(rawValue, type, path);
  if (type === "boolean") return booleanText(rawValue, path);
  if (type === "object") return objectFormValue(rawValue, path);
  if (type === "array") throw new Error(`${path} 的数组 items schema 缺失`);
  return rawValue;
}

function serializeFormValue(
  rawValue: string,
  schema: OpenApiRecord,
  path: string,
  root: OpenApiRecord,
): string {
  const type = typeof schema.type === "string" ? schema.type : "string";
  const value = formScalarValue(rawValue, type, path);
  const error = validateJsonSchema(value, schema, path, root);
  if (error) throw new Error(error);
  return typeof value === "string" ? value : JSON.stringify(value);
}

function replaceHeader(
  headers: Record<string, string>,
  name: string,
  value: string | null,
): void {
  for (const current of Object.keys(headers))
    if (current.toLowerCase() === name.toLowerCase()) delete headers[current];
  if (value !== null) headers[name] = value;
}

export function apiParameterInputKey(
  operation: OpenApiOperation,
  parameter: OpenApiParameter,
): string {
  return inputKey(operation, parameter);
}

interface RequestState {
  path: string;
  queryValues: { name: string; value: ParameterValue }[];
  headers: Record<string, string>;
}

type ParameterRead =
  | { action: "use"; value: ParameterValue }
  | { action: "skip" }
  | { action: "error"; error: string };

function readParameterValue(
  parameter: OpenApiParameter,
  rawValue: string,
  schemaRoot: OpenApiRecord,
): ParameterRead {
  if (parameter.location === "cookie") {
    if (parameter.required)
      return {
        action: "error",
        error: `必填 Cookie 参数 ${parameter.name} 由浏览器当前会话管理，不能在此手动覆盖。`,
      };
    return { action: "skip" };
  }
  if (parameter.required && rawValue.trim() === "")
    return { action: "error", error: `请填写必填参数：${parameter.name}` };
  if (rawValue.trim() === "") return { action: "skip" };
  try {
    return {
      action: "use",
      value: typedParameter(parameter, rawValue, schemaRoot),
    };
  } catch (failure) {
    return {
      action: "error",
      error: failureMessage(failure, `参数 ${parameter.name} 无效`),
    };
  }
}

function applyParameter(
  parameter: OpenApiParameter,
  value: ParameterValue,
  state: RequestState,
): string | null {
  if (parameter.location === "path") {
    const placeholder = `{${parameter.name}}`;
    if (!state.path.includes(placeholder))
      return `契约路径缺少参数占位符 ${placeholder}`;
    state.path = state.path.replaceAll(
      placeholder,
      encodeURIComponent(asParameterString(value)),
    );
  } else if (parameter.location === "query") {
    state.queryValues.push({ name: parameter.name, value });
  } else {
    state.headers[parameter.name] = asParameterString(value);
  }
  return null;
}

function collectParameters(
  operation: OpenApiOperation,
  input: ApiRequestInput,
): { state: RequestState } | { error: string } {
  const state: RequestState = {
    path: operation.path,
    queryValues: [],
    headers: { Accept: "application/json" },
  };
  for (const parameter of operation.parameters) {
    const rawValue = input.values[inputKey(operation, parameter)] ?? "";
    const read = readParameterValue(parameter, rawValue, input.schemaRoot);
    if (read.action === "skip") continue;
    if (read.action === "error") return { error: read.error };
    const error = applyParameter(parameter, read.value, state);
    if (error) return { error };
  }
  return { state };
}

function requestUrl(
  path: string,
  origin: string,
  queryValues: RequestState["queryValues"],
): { url: URL } | { error: string } {
  if (/[{}]/.test(path)) return { error: "路径仍包含未填写的参数" };
  if (path.startsWith("//") || !path.startsWith("/"))
    return { error: "请求路径必须是契约中的同源绝对路径" };
  let url: URL;
  try {
    url = new URL(path, origin);
  } catch {
    return { error: "请求路径无效" };
  }
  if (url.origin !== origin) return { error: "请求只能发送到当前服务源" };
  for (const entry of queryValues) {
    if (Array.isArray(entry.value)) {
      for (const item of entry.value)
        url.searchParams.append(entry.name, item);
    } else url.searchParams.append(entry.name, asParameterString(entry.value));
  }
  return { url };
}

interface BodyBuild {
  body?: BodyInit;
  contentType: string | null | undefined;
}

type BodyBuildResult = BodyBuild | { error: string };

function readFormFields(
  requestBody: OpenApiRequestBody,
  schemaRoot: OpenApiRecord,
): { fields: ApiRequestBodyField[] } | { error: string } {
  if (requestBody.required && requestBody.schema === null)
    return { error: "必填表单请求体没有可用的 OpenAPI schema。" };
  try {
    return { fields: apiRequestBodyFields(requestBody, schemaRoot) };
  } catch (failure) {
    return {
      error: failure instanceof Error ? failure.message : "表单 schema 无效。",
    };
  }
}

function createForm(mode: ApiRequestBodyMode): FormData | URLSearchParams {
  return mode === "multipart" ? new FormData() : new URLSearchParams();
}

function fileFieldError(
  field: ApiRequestBodyField,
  files: File[],
): string | null {
  if (field.required && files.length === 0)
    return `请选择必填文件：${field.name}`;
  if (!field.multiple && files.length > 1)
    return `${field.name} 只接受一个文件`;
  return null;
}

function appendFileField(
  field: ApiRequestBodyField,
  form: FormData | URLSearchParams,
  bodyFiles: Record<string, File[]>,
  mode: ApiRequestBodyMode,
): string | null {
  const files = bodyFiles[field.name] ?? [];
  const error = fileFieldError(field, files);
  if (error) return error;
  if (mode !== "multipart" || !(form instanceof FormData))
    return `${field.name} 的文件字段必须使用 multipart/form-data`;
  for (const file of files) form.append(field.name, file, file.name);
  return null;
}

type ScalarRead =
  | { action: "use"; value: string }
  | { action: "skip" }
  | { action: "error"; error: string };

function readFormFieldValue(
  field: ApiRequestBodyField,
  bodyValues: Record<string, string>,
): ScalarRead {
  const rawValue = bodyValues[field.name] ?? "";
  if (field.required && rawValue.trim() === "")
    return { action: "error", error: `请求体.${field.name} 是必填字段` };
  if (rawValue.trim() === "") return { action: "skip" };
  return { action: "use", value: rawValue };
}

function formFieldValues(rawValue: string, fieldType: string): string[] {
  return fieldType === "array"
    ? rawValue
        .split(",")
        .map((item) => item.trim())
        .filter((item) => item !== "")
    : [rawValue];
}

function formFieldItemSchema(
  field: ApiRequestBodyField,
  fieldType: string,
  schemaRoot: OpenApiRecord,
): { schema: OpenApiRecord } | { error: string } {
  const itemValue =
    fieldType === "array" ? field.schema.items : field.schema;
  let itemSchema: OpenApiRecord = { type: "string" };
  if (itemValue !== undefined) {
    let resolved: unknown;
    try {
      resolved = resolveOpenApiReference(itemValue, schemaRoot);
    } catch (failure) {
      return {
        error: failureMessage(failure, `表单字段 ${field.name} schema 无效`),
      };
    }
    if (!isRecord(resolved))
      return { error: `表单字段 ${field.name} schema 无效` };
    itemSchema = resolved;
  }
  return { schema: itemSchema };
}

function emptyRequiredArray(
  field: ApiRequestBodyField,
  fieldType: string,
  values: string[],
): boolean {
  return fieldType === "array" && values.length === 0 && field.required;
}

function appendSerializedValues(
  field: ApiRequestBodyField,
  form: FormData | URLSearchParams,
  values: string[],
  itemSchema: OpenApiRecord,
  schemaRoot: OpenApiRecord,
): string | null {
  for (const value of values) {
    let serialized: string;
    try {
      serialized = serializeFormValue(
        value,
        itemSchema,
        `请求体.${field.name}`,
        schemaRoot,
      );
    } catch (failure) {
      return failureMessage(failure, `请求体.${field.name} 无效`);
    }
    form.append(field.name, serialized);
  }
  return null;
}

function appendScalarField(
  field: ApiRequestBodyField,
  form: FormData | URLSearchParams,
  bodyValues: Record<string, string>,
  schemaRoot: OpenApiRecord,
): string | null {
  const read = readFormFieldValue(field, bodyValues);
  if (read.action === "skip") return null;
  if (read.action === "error") return read.error;
  const fieldType =
    typeof field.schema.type === "string" ? field.schema.type : "string";
  const values = formFieldValues(read.value, fieldType);
  const item = formFieldItemSchema(field, fieldType, schemaRoot);
  if ("error" in item) return item.error;
  if (emptyRequiredArray(field, fieldType, values))
    return `请求体.${field.name} 至少需要一个值`;
  return appendSerializedValues(field, form, values, item.schema, schemaRoot);
}

function appendFormField(
  field: ApiRequestBodyField,
  form: FormData | URLSearchParams,
  input: ApiRequestInput,
  mode: ApiRequestBodyMode,
): string | null {
  if (field.file)
    return appendFileField(field, form, input.bodyFiles, mode);
  return appendScalarField(field, form, input.bodyValues, input.schemaRoot);
}

function appendFormFields(
  fields: ApiRequestBodyField[],
  form: FormData | URLSearchParams,
  input: ApiRequestInput,
  mode: ApiRequestBodyMode,
): string | null {
  for (const field of fields) {
    const error = appendFormField(field, form, input, mode);
    if (error) return error;
  }
  return null;
}

function formBodyValue(
  requestBody: OpenApiRequestBody,
  form: FormData | URLSearchParams,
): FormData | URLSearchParams | undefined {
  return requestBody.required || [...form.keys()].length > 0
    ? form
    : undefined;
}

function formContentType(
  mode: ApiRequestBodyMode,
  body: FormData | URLSearchParams | undefined,
  mediaType: string | null,
): string | null {
  return mode === "urlencoded" && body !== undefined ? mediaType : null;
}

function buildFormBody(
  requestBody: OpenApiRequestBody,
  mode: ApiRequestBodyMode,
  input: ApiRequestInput,
): BodyBuildResult {
  const read = readFormFields(requestBody, input.schemaRoot);
  if ("error" in read) return read;
  const form = createForm(mode);
  const error = appendFormFields(read.fields, form, input, mode);
  if (error) return { error };
  const body = formBodyValue(requestBody, form);
  return { body, contentType: formContentType(mode, body, requestBody.mediaType) };
}

function buildBinaryBody(
  requestBody: OpenApiRequestBody,
  bodyFiles: Record<string, File[]>,
): BodyBuildResult {
  const files = bodyFiles.__request_body__ ?? [];
  if (requestBody.required && files.length === 0)
    return { error: "请选择请求原件。" };
  if (files.length > 1) return { error: "此操作只接受一个请求原件。" };
  if (files[0]) return { body: files[0], contentType: requestBody.mediaType };
  return { contentType: null };
}

function requiredBodyError(
  requestBody: OpenApiRequestBody,
  mode: ApiRequestBodyMode,
  bodyText: string,
): string | null {
  if (bodyText.trim() !== "" || !requestBody.required) return null;
  return mode === "json"
    ? "此操作要求请求体，请填写真实请求 JSON。"
    : "此操作要求请求体，请填写真实请求内容。";
}

function jsonBodyValue(
  requestBody: OpenApiRequestBody,
  bodyText: string,
  schemaRoot: OpenApiRecord,
): { body: string } | { error: string } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(bodyText);
  } catch {
    return { error: "请求体不是有效 JSON。" };
  }
  if (requestBody.schema) {
    const schemaError = validateJsonSchema(
      parsed,
      requestBody.schema,
      "请求体",
      schemaRoot,
    );
    if (schemaError) return { error: schemaError };
  }
  return { body: JSON.stringify(parsed) };
}

function buildTextBody(
  requestBody: OpenApiRequestBody,
  mode: ApiRequestBodyMode,
  input: ApiRequestInput,
): BodyBuildResult {
  const requiredError = requiredBodyError(requestBody, mode, input.bodyText);
  if (requiredError) return { error: requiredError };
  if (input.bodyText.trim() === "") return { contentType: undefined };
  if (mode !== "json")
    return { body: input.bodyText, contentType: requestBody.mediaType };
  const parsed = jsonBodyValue(requestBody, input.bodyText, input.schemaRoot);
  if ("error" in parsed) return parsed;
  return { body: parsed.body, contentType: requestBody.mediaType };
}

function buildRequestBody(
  operation: OpenApiOperation,
  input: ApiRequestInput,
): BodyBuildResult | null {
  const requestBody = operation.requestBody;
  if (requestBody === null) return null;
  const mode = apiRequestBodyMode(requestBody);
  if (mode === "multipart" || mode === "urlencoded")
    return buildFormBody(requestBody, mode, input);
  if (mode === "binary") return buildBinaryBody(requestBody, input.bodyFiles);
  return buildTextBody(requestBody, mode, input);
}

export function buildApiRequest(input: ApiRequestInput): ApiRequestBuild {
  const { operation, origin } = input;
  const collected = collectParameters(operation, input);
  if ("error" in collected) return { request: null, error: collected.error };
  const target = requestUrl(
    collected.state.path,
    origin,
    collected.state.queryValues,
  );
  if ("error" in target) return { request: null, error: target.error };
  const built = buildRequestBody(operation, input);
  if (built !== null && "error" in built)
    return { request: null, error: built.error };
  const headers = collected.state.headers;
  if (built !== null && built.contentType !== undefined)
    replaceHeader(headers, "Content-Type", built.contentType);
  return {
    request: {
      url: `${target.url.pathname}${target.url.search}`,
      init: {
        method: operation.method,
        credentials: "same-origin",
        cache: "no-store",
        headers,
        ...(built === null || built.body === undefined
          ? {}
          : { body: built.body }),
      },
    },
    error: null,
  };
}

function fileInputLookup(input: string, name: string): string {
  return `const ${input} = Array.from(document.querySelectorAll('input[type="file"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === ${JSON.stringify(name)});`;
}

function appendFileCode(
  setup: string[],
  name: string,
  field: ApiRequestBodyField | undefined,
  inputIndex: number,
): number {
  const input = `fileInput${inputIndex}`;
  setup.push(fileInputLookup(input, name));
  if (field?.multiple) {
    setup.push(
      `for (const file of ${input}?.files ?? []) body.append(${JSON.stringify(name)}, file);`,
    );
    return inputIndex + 1;
  }
  const file = `file${inputIndex + 1}`;
  setup.push(`const ${file} = ${input}?.files?.[0];`);
  setup.push(
    `if (!${file}) throw new Error(${JSON.stringify(`请选择文件：${name}`)});`,
  );
  setup.push(`body.append(${JSON.stringify(name)}, ${file});`);
  return inputIndex + 1;
}

function formDataSetup(
  operation: OpenApiOperation,
  body: FormData,
  schemaRoot: OpenApiRecord,
): string[] | null {
  const setup: string[] = ["const body = new FormData();"];
  const seenFiles = new Set<string>();
  let inputIndex = 0;
  let fields: ApiRequestBodyField[];
  try {
    fields = apiRequestBodyFields(operation.requestBody, schemaRoot);
  } catch {
    return null;
  }
  for (const [name, value] of body.entries()) {
    if (typeof value === "string") {
      setup.push(
        `body.append(${JSON.stringify(name)}, ${JSON.stringify(value)});`,
      );
      continue;
    }
    if (seenFiles.has(name)) continue;
    seenFiles.add(name);
    const field = fields.find((entry) => entry.name === name);
    inputIndex = appendFileCode(setup, name, field, inputIndex);
  }
  return setup;
}

function urlSearchParamsSetup(body: URLSearchParams): string[] {
  const setup: string[] = ["const body = new URLSearchParams();"];
  for (const [name, value] of body.entries())
    setup.push(
      `body.append(${JSON.stringify(name)}, ${JSON.stringify(value)});`,
    );
  return setup;
}

function blobSetup(): string[] {
  return [
    'const requestFileInput = Array.from(document.querySelectorAll(\'input[type="file"][data-request-body-field]\')).find((element) => element.dataset.requestBodyField === "__request_body__");',
    "const body = requestFileInput?.files?.[0];",
    'if (!body) throw new Error("请选择请求原件");',
  ];
}

function requestCodePrelude(
  operation: OpenApiOperation,
  request: { url: string; init: RequestInit },
  schemaRoot: OpenApiRecord,
): { setup: string[]; bodyExpression: string | null } | null {
  const body = request.init.body;
  const setup: string[] = [];
  let bodyExpression: string | null = null;
  if (body instanceof FormData) {
    const formSetup = formDataSetup(operation, body, schemaRoot);
    if (formSetup === null) return null;
    setup.push(...formSetup);
    bodyExpression = "body";
  } else if (body instanceof URLSearchParams) {
    setup.push(...urlSearchParamsSetup(body));
    bodyExpression = "body";
  } else if (typeof Blob !== "undefined" && body instanceof Blob) {
    setup.push(...blobSetup());
    bodyExpression = "body";
  } else if (typeof body === "string") bodyExpression = JSON.stringify(body);
  return { setup, bodyExpression };
}

export function apiRequestCode(
  operation: OpenApiOperation,
  request: { url: string; init: RequestInit } | null,
  schemaRoot: OpenApiRecord,
): string | null {
  if (!request) return null;
  const prelude = requestCodePrelude(operation, request, schemaRoot);
  if (prelude === null) return null;
  const headers = Object.fromEntries(
    new Headers(request.init.headers).entries(),
  );
  const headersCode = JSON.stringify(headers, null, 2).replaceAll("\n", "\n  ");
  const initLines = [
    `  method: ${JSON.stringify(operation.method)},`,
    `  credentials: "same-origin",`,
    `  headers: ${headersCode},`,
  ];
  if (prelude.bodyExpression !== null)
    initLines.push(`  body: ${prelude.bodyExpression},`);
  const initCode = ` {\n${initLines.join("\n")}\n}`;
  const preludeCode =
    prelude.setup.length > 0 ? `${prelude.setup.join("\n")}\n` : "";
  return `${preludeCode}const response = await fetch(${JSON.stringify(request.url)},${initCode});\nconst rawResponse = await response.text();\nconsole.log(response.status, rawResponse);`;
}
