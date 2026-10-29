import { spawn } from "node:child_process";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";
import type {
  OpenApiOperation,
  OpenApiParameter,
  OpenApiRequestBody,
  OpenApiRecord,
} from "./openapi";
import {
  apiParameterInputKey,
  apiRequestBodyFields,
  apiRequestBodyMode,
  apiRequestCode,
  buildApiRequest,
  type ApiRequestInput,
} from "./openapi-request";

const origin = "http://api.test";

function parameter(
  overrides: Partial<OpenApiParameter> & {
    name: string;
    location: OpenApiParameter["location"];
  },
): OpenApiParameter {
  return {
    required: false,
    description: "",
    schema: { type: "string" },
    example: null,
    raw: null,
    ...overrides,
  };
}

function requestBody(
  overrides: Partial<OpenApiRequestBody> = {},
): OpenApiRequestBody {
  return {
    required: false,
    description: "",
    mediaType: "application/json",
    schema: null,
    example: null,
    raw: null,
    ...overrides,
  };
}

function operation(
  overrides: Partial<OpenApiOperation> & { path: string },
): OpenApiOperation {
  const method = (overrides.method ?? "GET").toUpperCase();
  return {
    key: `${method} ${overrides.path}`,
    operationId: "",
    summary: "",
    description: "",
    tags: [],
    parameters: [],
    requestBody: null,
    responses: [],
    security: null,
    raw: null,
    ...overrides,
    method,
  };
}

function input(
  target: OpenApiOperation,
  overrides: Partial<ApiRequestInput> = {},
): ApiRequestInput {
  return {
    operation: target,
    values: {},
    bodyText: "",
    bodyValues: {},
    bodyFiles: {},
    origin,
    schemaRoot: {},
    ...overrides,
  };
}

function build(
  target: OpenApiOperation,
  overrides: Partial<ApiRequestInput> = {},
) {
  return buildApiRequest(input(target, overrides));
}

describe("apiParameterInputKey", () => {
  it("namespaces parameter values by operation, location and name", () => {
    expect(
      apiParameterInputKey(operation({ path: "/cases/{id}" }), {
        name: "id",
        location: "path",
        required: true,
        description: "",
        schema: null,
        example: null,
        raw: null,
      }),
    ).toBe("GET /cases/{id}:path:id");
  });
});

describe("apiRequestBodyMode", () => {
  it.each([
    [null, "none"],
    [requestBody({ mediaType: "application/json" }), "json"],
    [requestBody({ mediaType: "application/json; charset=utf-8" }), "json"],
    [requestBody({ mediaType: "APPLICATION/JSON" }), "json"],
    [requestBody({ mediaType: "multipart/form-data; boundary=x" }), "multipart"],
    [requestBody({ mediaType: "application/x-www-form-urlencoded" }), "urlencoded"],
    [requestBody({ mediaType: "application/octet-stream" }), "binary"],
    [requestBody({ mediaType: "image/png" }), "binary"],
    [requestBody({ mediaType: "audio/mpeg" }), "binary"],
    [requestBody({ mediaType: "video/mp4" }), "binary"],
    [requestBody({ mediaType: "text/plain" }), "text"],
    [requestBody({ mediaType: null }), "text"],
    [requestBody({ mediaType: "application/xml" }), "text"],
  ] as const)("maps %j to %s", (body, expected) => {
    expect(apiRequestBodyMode(body)).toBe(expected);
  });
});

describe("apiRequestBodyFields", () => {
  it("returns no fields for bodies that are not form bodies", () => {
    expect(apiRequestBodyFields(null, {})).toEqual([]);
    expect(apiRequestBodyFields(requestBody({ schema: null }), {})).toEqual([]);
    expect(
      apiRequestBodyFields(
        requestBody({ mediaType: "application/json", schema: { type: "object" } }),
        {},
      ),
    ).toEqual([]);
    expect(
      apiRequestBodyFields(
        requestBody({ mediaType: "text/plain", schema: { type: "object" } }),
        {},
      ),
    ).toEqual([]);
  });

  it("returns no fields for a text body that declares properties", () => {
    expect(
      apiRequestBodyFields(
        requestBody({
          mediaType: "text/plain",
          schema: { type: "object", properties: { a: { type: "string" } } },
        }),
        {},
      ),
    ).toEqual([]);
  });

  it("returns no fields when the schema has no properties", () => {
    expect(
      apiRequestBodyFields(
        requestBody({ mediaType: "multipart/form-data", schema: { type: "object" } }),
        {},
      ),
    ).toEqual([]);
  });

  it("reads required flags, descriptions, binary and array fields", () => {
    const root = {
      components: {
        schemas: {
          Upload: {
            type: "object",
            required: ["title", "attachment"],
            properties: {
              title: { type: "string", description: "标题" },
              count: { type: "integer" },
              attachment: { type: "string", format: "binary" },
              gallery: { type: "array", items: { type: "string", format: "binary" } },
              parts: { $ref: "#/components/schemas/Parts" },
            },
          },
          Parts: { type: "array", items: { type: "string" } },
        },
      },
    };
    expect(
      apiRequestBodyFields(
        requestBody({
          mediaType: "multipart/form-data",
          schema: { $ref: "#/components/schemas/Upload" },
        }),
        root,
      ),
    ).toEqual([
      {
        name: "title",
        description: "标题",
        required: true,
        schema: { type: "string", description: "标题" },
        file: false,
        multiple: false,
      },
      {
        name: "count",
        description: "",
        required: false,
        schema: { type: "integer" },
        file: false,
        multiple: false,
      },
      {
        name: "attachment",
        description: "",
        required: true,
        schema: { type: "string", format: "binary" },
        file: true,
        multiple: false,
      },
      {
        name: "gallery",
        description: "",
        required: false,
        schema: { type: "array", items: { type: "string", format: "binary" } },
        file: true,
        multiple: true,
      },
      {
        name: "parts",
        description: "",
        required: false,
        schema: { type: "array", items: { type: "string" } },
        file: false,
        multiple: false,
      },
    ]);
  });

  it("resolves field and item references", () => {
    const root = {
      components: {
        schemas: {
          Text: { type: "string", description: "文案" },
          List: { type: "array", items: { $ref: "#/components/schemas/Text" } },
        },
      },
    };
    expect(
      apiRequestBodyFields(
        requestBody({
          mediaType: "application/x-www-form-urlencoded",
          schema: {
            type: "object",
            properties: { list: { $ref: "#/components/schemas/List" } },
          },
        }),
        root,
      ),
    ).toEqual([
      {
        name: "list",
        description: "",
        required: false,
        schema: { type: "array", items: { $ref: "#/components/schemas/Text" } },
        file: false,
        multiple: false,
      },
    ]);
  });

  it.each([
    [
      "non-object body schema",
      requestBody({ mediaType: "multipart/form-data", schema: { $ref: "#/bad" } }),
      { bad: 7 },
      "请求体 schema 必须是对象",
    ],
    [
      "non-object properties",
      requestBody({ mediaType: "multipart/form-data", schema: { properties: 7 } }),
      {},
      "表单 schema properties 必须是对象",
    ],
    [
      "non-array required",
      requestBody({
        mediaType: "multipart/form-data",
        schema: { required: "a", properties: {} },
      }),
      {},
      "表单 schema required 必须是字符串数组",
    ],
    [
      "non-string required member",
      requestBody({
        mediaType: "multipart/form-data",
        schema: { required: [7], properties: {} },
      }),
      {},
      "表单 schema required 必须是字符串数组",
    ],
    [
      "required name without property",
      requestBody({
        mediaType: "multipart/form-data",
        schema: { required: ["missing"], properties: { a: { type: "string" } } },
      }),
      {},
      "表单 schema 必填字段 missing 没有属性定义",
    ],
    [
      "non-object field schema",
      requestBody({
        mediaType: "multipart/form-data",
        schema: { properties: { a: { $ref: "#/bad" } } },
      }),
      { bad: 7 },
      "表单字段 a 的 schema 必须是对象",
    ],
    [
      "non-object items schema",
      requestBody({
        mediaType: "multipart/form-data",
        schema: { properties: { a: { type: "array", items: { $ref: "#/bad" } } } },
      }),
      { bad: 7 },
      "表单字段 a 的 items schema 必须是对象",
    ],
    [
      "binary in urlencoded form",
      requestBody({
        mediaType: "application/x-www-form-urlencoded",
        schema: { properties: { a: { type: "string", format: "binary" } } },
      }),
      {},
      "表单字段 a 是文件，但媒体类型不支持文件上传",
    ],
  ])("rejects %s", (_label, body, root, message) => {
    expect(() => apiRequestBodyFields(body, root as OpenApiRecord)).toThrow(
      message,
    );
  });
});

describe("buildApiRequest parameters", () => {
  it("builds a bodyless same-origin request", () => {
    const built = build(operation({ path: "/cases" }));
    expect(built.error).toBeNull();
    expect(built.request?.url).toBe("/cases");
    expect(built.request?.init).toEqual({
      method: "GET",
      credentials: "same-origin",
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    expect(built.request?.init).not.toHaveProperty("body");
  });

  it("serialises path, query and header values by their schema type", () => {
    const target = operation({
      method: "post",
      path: "/cases/{id}/{slug}",
      parameters: [
        parameter({ name: "id", location: "path", required: true, schema: { type: "integer" } }),
        parameter({ name: "slug", location: "path", required: true, schema: { type: "string" } }),
        parameter({ name: "tags", location: "query", schema: { type: "array" } }),
        parameter({ name: "limit", location: "query", schema: { type: "integer" } }),
        parameter({ name: "ratio", location: "query", schema: { type: "number" } }),
        parameter({ name: "meta", location: "query", schema: { type: "object" } }),
        parameter({ name: "X-Flag", location: "header", schema: { type: "boolean" } }),
        parameter({ name: "X-Rate", location: "query", schema: { type: "integer" } }),
        parameter({ name: "empty", location: "query", schema: { type: "string" } }),
      ],
    });
    const built = build(target, {
      values: {
        "POST /cases/{id}/{slug}:path:id": "42",
        "POST /cases/{id}/{slug}:path:slug": "a b/c",
        "POST /cases/{id}/{slug}:query:tags": "a, b ,,c",
        "POST /cases/{id}/{slug}:query:limit": "7",
        "POST /cases/{id}/{slug}:query:ratio": "1.5",
        "POST /cases/{id}/{slug}:query:meta": '{"a":1}',
        "POST /cases/{id}/{slug}:header:X-Flag": "true",
        "POST /cases/{id}/{slug}:query:X-Rate": "3",
        "POST /cases/{id}/{slug}:query:empty": "   ",
        "unrelated": "x",
      },
    });
    expect(built.error).toBeNull();
    expect(built.request?.url).toBe(
      "/cases/42/a%20b%2Fc?tags=a&tags=b&tags=c&limit=7&ratio=1.5&meta=%7B%22a%22%3A1%7D&X-Rate=3",
    );
    expect(built.request?.init.headers).toEqual({
      Accept: "application/json",
      "X-Flag": "true",
    });
    expect(built.request?.init.method).toBe("POST");
  });

  it("treats a parameter without a schema as a string", () => {
    const target = operation({
      path: "/cases",
      parameters: [parameter({ name: "note", location: "query", schema: null })],
    });
    const built = build(target, {
      values: { "GET /cases:query:note": "hello" },
    });
    expect(built.error).toBeNull();
    expect(built.request?.url).toBe("/cases?note=hello");
  });

  it("replaces every path placeholder occurrence", () => {
    const target = operation({
      path: "/pairs/{id}/with/{id}",
      parameters: [
        parameter({ name: "id", location: "path", required: true, schema: { type: "string" } }),
      ],
    });
    expect(
      build(target, { values: { "GET /pairs/{id}/with/{id}:path:id": "x y" } })
        .request?.url,
    ).toBe("/pairs/x%20y/with/x%20y");
  });

  it("rejects a required cookie instead of sending it", () => {
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "session", location: "cookie", required: true, schema: null }),
      ],
    });
    expect(build(target).error).toBe(
      "必填 Cookie 参数 session 由浏览器当前会话管理，不能在此手动覆盖。",
    );
  });

  it("ignores an optional cookie and skips empty optional values", () => {
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "session", location: "cookie", schema: null }),
        parameter({ name: "q", location: "query", schema: { type: "integer" } }),
      ],
    });
    expect(
      build(target, { values: { "GET /cases:query:q": " " } }).request?.url,
    ).toBe("/cases");
  });

  it("rejects a missing required parameter", () => {
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "q", location: "query", required: true, schema: { type: "string" } }),
      ],
    });
    expect(build(target).error).toBe("请填写必填参数：q");
    expect(
      build(target, { values: { "GET /cases:query:q": "  " } }).error,
    ).toBe("请填写必填参数：q");
  });

  it("rejects a path without its placeholder", () => {
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "id", location: "path", required: true, schema: { type: "string" } }),
      ],
    });
    expect(
      build(target, { values: { "GET /cases:path:id": "1" } }).error,
    ).toBe("契约路径缺少参数占位符 {id}");
  });

  it("rejects unfilled braces and non-absolute paths", () => {
    expect(
      build(operation({ path: "/cases/{other}" })).error,
    ).toBe("路径仍包含未填写的参数");
    for (const path of ["cases", "//evil.test/cases"]) {
      expect(build(operation({ path })).error).toBe(
        "请求路径必须是契约中的同源绝对路径",
      );
    }
  });

  it("rejects an invalid base origin and a non-normalised origin", () => {
    expect(
      buildApiRequest(
        input(operation({ path: "/cases", method: "get" }), { origin: "not a url" }),
      ).error,
    ).toBe("请求路径无效");
    expect(
      buildApiRequest(
        input(operation({ path: "/cases" }), { origin: "http://api.test:80" }),
      ).error,
    ).toBe("请求只能发送到当前服务源");
  });

  it.each([
    [
      "integer text",
      parameter({ name: "p", location: "query", schema: { type: "integer" } }),
      "abc",
      "p 的值不符合 integer 类型",
    ],
    [
      "integer fraction",
      parameter({ name: "p", location: "query", schema: { type: "integer" } }),
      "1.5",
      "p 的值不符合 integer 类型",
    ],
    [
      "number text",
      parameter({ name: "p", location: "query", schema: { type: "number" } }),
      "abc",
      "p 的值不符合 number 类型",
    ],
    [
      "boolean text",
      parameter({ name: "p", location: "query", schema: { type: "boolean" } }),
      "yes",
      "p 只能填写 true 或 false",
    ],
    [
      "object text",
      parameter({ name: "p", location: "query", schema: { type: "object" } }),
      "no",
      "p 需要填写 JSON 对象",
    ],
    [
      "object array",
      parameter({ name: "p", location: "query", schema: { type: "object" } }),
      "[1]",
      "p 需要填写 JSON 对象",
    ],
  ])("rejects %s", (_label, target, value, message) => {
    const target_operation = operation({
      path: "/cases",
      parameters: [target],
    });
    const built = build(target_operation, {
      values: { "GET /cases:query:p": value },
    });
    expect(built.error).toBe(message);
    expect(built.request).toBeNull();
  });

  it("rejects a parameter schema that does not resolve to an object", () => {
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "p", location: "query", schema: { $ref: "#/bad" } }),
      ],
    });
    expect(
      build(target, { schemaRoot: { bad: 7 }, values: { "GET /cases:query:p": "x" } })
        .error,
    ).toBe("p schema 必须是对象");
  });

  it("reports a non-Error failure generically", () => {
    const hostile = new Proxy(
      {},
      {
        get() {
          throw "boom";
        },
      },
    );
    const target = operation({
      path: "/cases",
      parameters: [
        parameter({ name: "p", location: "query", schema: hostile as OpenApiRecord }),
      ],
    });
    expect(
      build(target, {
        values: { "GET /cases:query:p": "x" },
      }).error,
    ).toBe("参数 p 无效");
  });
});

describe("buildApiRequest JSON bodies", () => {
  const target = (body: OpenApiRequestBody | null) =>
    operation({ path: "/cases", method: "post", requestBody: body });

  it("requires a JSON body when the contract marks it required", () => {
    expect(
      build(target(requestBody({ required: true }))).error,
    ).toBe("此操作要求请求体，请填写真实请求 JSON。");
    expect(
      build(target(requestBody({ required: true, mediaType: "text/plain" }))).error,
    ).toBe("此操作要求请求体，请填写真实请求内容。");
  });

  it("rejects invalid JSON text", () => {
    expect(
      build(target(requestBody()), { bodyText: "{" }).error,
    ).toBe("请求体不是有效 JSON。");
  });

  it("normalises an accepted JSON body and sets the media type", () => {
    const built = build(target(requestBody()), {
      bodyText: '{ "a": 1 }',
    });
    expect(built.error).toBeNull();
    expect(built.request?.init.body).toBe('{"a":1}');
    expect(built.request?.init.headers).toEqual({
      Accept: "application/json",
      "Content-Type": "application/json",
    });
  });

  it("keeps a text body verbatim", () => {
    const built = build(
      target(requestBody({ mediaType: "text/plain; charset=utf-8" })),
      { bodyText: "hello\n" },
    );
    expect(built.request?.init.body).toBe("hello\n");
    expect(built.request?.init.headers).toEqual({
      Accept: "application/json",
      "Content-Type": "text/plain; charset=utf-8",
    });
  });

  it("leaves an optional empty body untouched", () => {
    const built = build(target(requestBody()));
    expect(built.request?.init).not.toHaveProperty("body");
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
  });

  it.each([
    ["type", { type: "object" }, "[]", "请求体 类型不符合 OpenAPI schema"],
    [
      "required",
      { type: "object", required: ["a"] },
      "{}",
      "请求体.a 是必填字段",
    ],
    [
      "required shape",
      { type: "object", required: "a" },
      "{}",
      "请求体 的 schema required 无效",
    ],
    [
      "properties shape",
      { type: "object", properties: 7 },
      '{"a":1}',
      "请求体 的 schema properties 无效",
    ],
    [
      "property schema",
      { type: "object", properties: { a: 7 } },
      '{"a":1}',
      "请求体.a 的 schema 无效",
    ],
    [
      "additional properties",
      { type: "object", properties: { a: { type: "number" } }, additionalProperties: false },
      '{"b":1}',
      "请求体.b 未在 schema 中声明",
    ],
    ["const", { const: { a: 1 } }, '{"a":2}', "请求体 不符合 schema const"],
    ["enum", { enum: [{ a: 1 }, "x"] }, '"y"', "请求体 不在 schema enum 允许值中"],
    [
      "allOf shape",
      { allOf: [7] },
      "1",
      "请求体 的 schema allOf 无效",
    ],
    [
      "allOf member",
      { allOf: [{ type: "string" }] },
      "1",
      "请求体 类型不符合 OpenAPI schema",
    ],
    ["anyOf", { anyOf: [{ type: "string" }, { type: "boolean" }] }, "1", "请求体 不符合 schema anyOf"],
    ["oneOf", { oneOf: [{ type: "number" }, { type: "integer" }] }, "1", "请求体 不符合 schema oneOf"],
    ["oneOf none", { oneOf: [{ type: "string" }] }, "1", "请求体 不符合 schema oneOf"],
    ["items shape", { type: "array", items: 7 }, "[]", "请求体 的 schema items 无效"],
    [
      "array member",
      { type: "array", items: { type: "string" } },
      '["a",1]',
      "请求体[1] 类型不符合 OpenAPI schema",
    ],
  ])("validates %s", (_label, schema, bodyText, message) => {
    const built = build(target(requestBody({ schema })), { bodyText });
    expect(built.error).toBe(message);
    expect(built.request).toBeNull();
  });

  it.each([
    ["object", { type: "object" }, "{}", true],
    ["array", { type: "array" }, "[]", true],
    ["string", { type: "string" }, '"a"', true],
    ["integer", { type: "integer" }, "1", true],
    ["integer fraction", { type: "integer" }, "1.5", false],
    ["number", { type: "number" }, "1.5", true],
    ["boolean", { type: "boolean" }, "true", true],
    ["null", { type: "null" }, "null", true],
    ["multi type", { type: ["string", "integer"] }, '"a"', true],
    ["multi type miss", { type: ["string", "integer"] }, "true", false],
    ["nullable", { nullable: true }, "null", true],
    ["null type precedence", { type: "string" }, "null", false],
  ])("accepts or rejects schema type %s", (_label, schema, bodyText, valid) => {
    const built = build(target(requestBody({ schema })), { bodyText });
    expect(built.error === null).toBe(valid);
  });

  it("accepts a null value for an explicitly nullable schema", () => {
    for (const schema of [
      { nullable: true },
      { type: ["string", "null"] },
      { nullable: true, type: "string" },
    ]) {
      const built = build(target(requestBody({ schema })), { bodyText: "null" });
      expect(built.error).toBeNull();
      expect(built.request?.init.body).toBe("null");
    }
  });

  it("still validates a non-null value against a nullable schema", () => {
    const built = build(
      target(requestBody({ schema: { nullable: true, type: "string" } })),
      { bodyText: "1" },
    );
    expect(built.error).toBe("请求体 类型不符合 OpenAPI schema");
  });

  it("ignores non-string type declarations", () => {
    const built = build(
      target(requestBody({ schema: { type: ["string", 7] } })),
      { bodyText: '"a"' },
    );
    expect(built.error).toBeNull();
  });

  it("rejects a value when the declared type is unknown", () => {
    const built = build(target(requestBody({ schema: { type: "date" } })), {
      bodyText: '"2026-01-01"',
    });
    expect(built.error).toBe("请求体 类型不符合 OpenAPI schema");
    expect(built.request).toBeNull();
  });

  it("applies required only to object values", () => {
    const built = build(
      target(requestBody({ schema: { required: ["a"] } })),
      { bodyText: '"text"' },
    );
    expect(built.error).toBeNull();
  });

  it("validates properties declared without an explicit object type", () => {
    const built = build(
      target(requestBody({ schema: { properties: { a: { type: "string" } } } })),
      { bodyText: '{"a":1}' },
    );
    expect(built.error).toBe("请求体.a 类型不符合 OpenAPI schema");
  });

  it("does not treat object values with length-like keys as arrays", () => {
    const built = build(
      target(requestBody({ schema: { items: { type: "string" } } })),
      { bodyText: '{"length":1,"0":7}' },
    );
    expect(built.error).toBeNull();
  });

  it("validates items for an array schema without an explicit array type", () => {
    const built = build(
      target(requestBody({ schema: { items: { type: "string" } } })),
      { bodyText: "[1]" },
    );
    expect(built.error).toBe("请求体[0] 类型不符合 OpenAPI schema");
  });

  it("accepts every item of a valid array", () => {
    const built = build(
      target(requestBody({ schema: { type: "array", items: { type: "string" } } })),
      { bodyText: '["a"]' },
    );
    expect(built.error).toBeNull();
    expect(built.request?.init.body).toBe('["a"]');
  });

  it("returns a valid nonempty array request without hanging", async () => {
    const moduleUrl = pathToFileURL(
      resolve(process.cwd(), "src/api/openapi-request.ts"),
    ).href;
    const script = `
      import { buildApiRequest } from ${JSON.stringify(moduleUrl)};
      const built = buildApiRequest({
        operation: {
          key: "POST /cases",
          operationId: "",
          summary: "",
          description: "",
          tags: [],
          method: "POST",
          path: "/cases",
          parameters: [],
          requestBody: {
            required: false,
            description: "",
            mediaType: "application/json",
            schema: { type: "array", items: { type: "string" } },
            example: null,
            raw: null,
          },
          responses: [],
          security: null,
          raw: null,
        },
        values: {},
        bodyText: '["contract"]',
        bodyValues: {},
        bodyFiles: {},
        origin: "http://api.test",
        schemaRoot: {},
      });
      if (built.error !== null || built.request?.init.body !== '["contract"]') {
        throw new Error(built.error ?? "array request body was not preserved");
      }
      process.stdout.write("ok");
    `;
    const child = spawn("bun", ["-e", script], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    child.stdout?.setEncoding("utf8").on("data", (chunk: string) => {
      stdout += chunk;
    });
    child.stderr?.setEncoding("utf8").on("data", (chunk: string) => {
      stderr += chunk;
    });
    const result = await new Promise<{
      timedOut: boolean;
      status: number | null;
      error: Error | null;
    }>((resolveResult) => {
      let timedOut = false;
      const deadline = setTimeout(() => {
        timedOut = true;
        child.kill("SIGKILL");
      }, 2000);
      child.on("error", (error) => {
        clearTimeout(deadline);
        resolveResult({ timedOut, status: null, error });
      });
      child.on("close", (status) => {
        clearTimeout(deadline);
        resolveResult({ timedOut, status, error: null });
      });
    });

    expect(result.timedOut, stderr).toBe(false);
    expect(result.error, stderr).toBeNull();
    expect(result.status, stderr).toBe(0);
    expect(stdout).toBe("ok");
  });

  it("reports the index of the first invalid array item", () => {
    const built = build(
      target(requestBody({ schema: { type: "array", items: { type: "string" } } })),
      { bodyText: '[1,"a"]' },
    );
    expect(built.error).toBe("请求体[0] 类型不符合 OpenAPI schema");
  });

  it("reports the index of a later invalid array item", () => {
    const built = build(
      target(requestBody({ schema: { type: "array", items: { type: "string" } } })),
      { bodyText: '["a",1]' },
    );
    expect(built.error).toBe("请求体[1] 类型不符合 OpenAPI schema");
  });

  it("limits nested allOf recursion depth", () => {
    let schema: OpenApiRecord = { type: "integer" };
    for (let index = 0; index < 70; index += 1) schema = { allOf: [schema] };
    const built = build(target(requestBody({ schema })), { bodyText: "1" });
    expect(built.error).toMatch(/递归层级超出限制$/);
  });

  it("limits nested anyOf recursion depth", () => {
    let schema: OpenApiRecord = { type: "integer" };
    for (let index = 0; index < 70; index += 1) schema = { anyOf: [schema] };
    const built = build(target(requestBody({ schema })), { bodyText: "1" });
    expect(built.error).toBe("请求体 不符合 schema anyOf");
  });

  it("limits nested oneOf recursion depth", () => {
    let schema: OpenApiRecord = { type: "integer" };
    for (let index = 0; index < 70; index += 1) schema = { oneOf: [schema] };
    const built = build(target(requestBody({ schema })), { bodyText: "1" });
    expect(built.error).toBe("请求体 不符合 schema oneOf");
  });

  it("limits recursion depth when following a schema reference", () => {
    let deep: OpenApiRecord = { type: "integer" };
    let value: unknown = 1;
    for (let index = 0; index < 64; index += 1) {
      deep = { type: "object", properties: { nest: deep } };
      value = { nest: value };
    }
    const schemaRoot = { components: { schemas: { Deep: deep } } };
    const built = build(
      target(requestBody({ schema: { $ref: "#/components/schemas/Deep" } })),
      { bodyText: JSON.stringify(value), schemaRoot },
    );
    expect(built.error).toMatch(/递归层级超出限制$/);
  });

  it("limits nested array item recursion depth", () => {
    let schema: OpenApiRecord = { type: "integer" };
    let value: unknown = 1;
    for (let index = 0; index < 65; index += 1) {
      schema = { type: "array", items: schema };
      value = [value];
    }
    const built = build(target(requestBody({ schema })), {
      bodyText: JSON.stringify(value),
    });
    expect(built.error).toMatch(/递归层级超出限制$/);
  });

  it("follows schema references", () => {
    const schemaRoot = {
      components: {
        schemas: {
          Payload: { type: "object", required: ["a"], properties: { a: { type: "string" } } },
        },
      },
    };
    const built = build(
      target(requestBody({ schema: { $ref: "#/components/schemas/Payload" } })),
      { bodyText: '{"a":"x"}', schemaRoot },
    );
    expect(built.error).toBeNull();
    const invalid = build(
      target(requestBody({ schema: { $ref: "#/components/schemas/Payload" } })),
      { bodyText: "{}", schemaRoot },
    );
    expect(invalid.error).toBe("请求体.a 是必填字段");
  });

  it("rejects broken, non-object and over-deep schema references", () => {
    expect(
      build(target(requestBody({ schema: { $ref: "#/missing" } })), {
        bodyText: "1",
      }).error,
    ).toBe("无法解析 OpenAPI 引用：#/missing");
    expect(
      build(target(requestBody({ schema: { $ref: "#/bad" } })), {
        bodyText: "1",
        schemaRoot: { bad: 7 },
      }).error,
    ).toBe("请求体 的 schema 引用没有指向对象");
    let schema: OpenApiRecord = { type: "string" };
    let value: unknown = "x";
    for (let index = 0; index < 70; index += 1) {
      schema = { type: "object", properties: { nest: schema } };
      value = { nest: value };
    }
    expect(
      build(target(requestBody({ schema })), {
        bodyText: JSON.stringify(value),
      }).error,
    ).toMatch(/^请求体(?:\.nest){65} 的 schema 递归层级超出限制$/);
  });

  it("validates nested objects and arrays with dotted paths", () => {
    const schema = {
      type: "object",
      properties: {
        list: { type: "array", items: { type: "object", required: ["id"], properties: { id: { type: "integer" } } } },
      },
    };
    const built = build(target(requestBody({ schema })), {
      bodyText: '{"list":[{"id":1},{"id":"x"}]}',
    });
    expect(built.error).toBe("请求体.list[1].id 类型不符合 OpenAPI schema");
  });
});

describe("buildApiRequest binary and form bodies", () => {
  const target = (body: OpenApiRequestBody) =>
    operation({ path: "/upload", method: "post", requestBody: body });

  it("requires a request file for a binary body", () => {
    expect(
      build(target(requestBody({ mediaType: "application/octet-stream", required: true }))).error,
    ).toBe("请选择请求原件。");
  });

  it("accepts exactly one request file", () => {
    const file = new File(["x"], "contract.bin");
    const built = build(
      target(requestBody({ mediaType: "application/octet-stream" })),
      { bodyFiles: { __request_body__: [file, new File(["y"], "second.bin")] } },
    );
    expect(built.error).toBe("此操作只接受一个请求原件。");
    const single = build(
      target(requestBody({ mediaType: "image/png" })),
      { bodyFiles: { __request_body__: [file] } },
    );
    expect(single.request?.init.body).toBe(file);
    expect(single.request?.init.headers).toEqual({
      Accept: "application/json",
      "Content-Type": "image/png",
    });
  });

  it("omits an optional binary body and its content type", () => {
    const built = build(target(requestBody({ mediaType: "audio/mpeg" })));
    expect(built.request?.init).not.toHaveProperty("body");
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
  });

  it("requires a form schema when the form body is required", () => {
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", required: true }))).error,
    ).toBe("必填表单请求体没有可用的 OpenAPI schema。");
  });

  it("allows an optional multipart body without a schema", () => {
    const built = build(target(requestBody({ mediaType: "multipart/form-data" })));
    expect(built.error).toBeNull();
    expect(built.request?.init).not.toHaveProperty("body");
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
  });

  it("sends a multipart form that contains exactly one entry", () => {
    const built = build(
      target(
        requestBody({
          mediaType: "multipart/form-data",
          schema: { type: "object", properties: { note: { type: "string" } } },
        }),
      ),
      { bodyValues: { note: "x" } },
    );
    expect(built.error).toBeNull();
    const form = built.request?.init.body as FormData;
    expect(form).toBeInstanceOf(FormData);
    expect(form.getAll("note")).toEqual(["x"]);
  });

  it("sends a urlencoded form that contains exactly one entry", () => {
    const built = build(
      target(
        requestBody({
          mediaType: "application/x-www-form-urlencoded",
          schema: { type: "object", properties: { note: { type: "string" } } },
        }),
      ),
      { bodyValues: { note: "x" } },
    );
    expect(built.error).toBeNull();
    const body = built.request?.init.body as URLSearchParams;
    expect(body).toBeInstanceOf(URLSearchParams);
    expect([...body.entries()]).toEqual([["note", "x"]]);
    expect(built.request?.init.headers).toEqual({
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
    });
  });

  it("accepts schema nesting at the limit and rejects the next level for a form field", () => {
    function buildWithAllOfDepth(depth: number) {
      let field: OpenApiRecord = { type: "string" };
      for (let index = 0; index < depth; index += 1)
        field = { allOf: [field] };
      return build(
        target(
          requestBody({
            mediaType: "application/x-www-form-urlencoded",
            schema: { type: "object", properties: { title: field } },
          }),
        ),
        { bodyValues: { title: "contract" } },
      );
    }

    const atLimit = buildWithAllOfDepth(64);
    expect(atLimit.error).toBeNull();
    expect([...(atLimit.request?.init.body as URLSearchParams).entries()]).toEqual([
      ["title", "contract"],
    ]);

    const beyondLimit = buildWithAllOfDepth(65);
    expect(beyondLimit.error).toBe(
      "请求体.title 的 schema 递归层级超出限制",
    );
    expect(beyondLimit.request).toBeNull();
  });

  it("returns form schema errors unchanged", () => {
    expect(
      build(
        target(
          requestBody({
            mediaType: "multipart/form-data",
            schema: { properties: 7 },
          }),
        ),
      ).error,
    ).toBe("表单 schema properties 必须是对象");
    expect(
      build(
        target(
          requestBody({
            mediaType: "multipart/form-data",
            schema: { $ref: "#/missing" },
          }),
        ),
      ).error,
    ).toBe("无法解析 OpenAPI 引用：#/missing");
  });

  it("builds a multipart form with text, array and file fields", () => {
    const file = new File(["x"], "a.txt");
    const second = new File(["y"], "b.txt");
    const schema = {
      type: "object",
      required: ["title", "attachment", "gallery"],
      properties: {
        title: { type: "string" },
        count: { type: "integer" },
        tags: { type: "array", items: { type: "string" } },
        attachment: { type: "string", format: "binary" },
        gallery: { type: "array", items: { type: "string", format: "binary" } },
      },
    };
    const built = build(
      target(requestBody({ mediaType: "multipart/form-data", schema })),
      {
        bodyValues: { title: "合同", count: "3", tags: "a, b" },
        bodyFiles: { attachment: [file], gallery: [file, second] },
      },
    );
    expect(built.error).toBeNull();
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
    const form = built.request?.init.body as FormData;
    expect(form).toBeInstanceOf(FormData);
    expect(form.getAll("title")).toEqual(["合同"]);
    expect(form.getAll("count")).toEqual(["3"]);
    expect(form.getAll("tags")).toEqual(["a", "b"]);
    expect(form.getAll("attachment")).toEqual([file]);
    expect(form.getAll("gallery")).toEqual([file, second]);
  });

  it("rejects missing required files and duplicate single files", () => {
    const schema = {
      type: "object",
      required: ["attachment", "single"],
      properties: {
        attachment: { type: "string", format: "binary" },
        single: { type: "string", format: "binary" },
      },
    };
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", schema })), {
        bodyFiles: { single: [new File(["x"], "s.txt")] },
      }).error,
    ).toBe("请选择必填文件：attachment");
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", schema })), {
        bodyFiles: {
          attachment: [new File(["x"], "a.txt")],
          single: [new File(["x"], "s1.txt"), new File(["y"], "s2.txt")],
        },
      }).error,
    ).toBe("single 只接受一个文件");
  });

  it("rejects empty required form values and scalar conversion errors", () => {
    const schema = {
      type: "object",
      required: ["title", "list", "count", "flag", "meta"],
      properties: {
        title: { type: "string" },
        list: { type: "array", items: { type: "string" } },
        count: { type: "integer" },
        flag: { type: "boolean" },
        meta: { type: "object" },
      },
    };
    const form = (bodyValues: Record<string, string>) =>
      build(target(requestBody({ mediaType: "multipart/form-data", schema })), {
        bodyValues,
      });
    expect(form({})?.error).toBe("请求体.title 是必填字段");
    expect(
      form({ title: "t", list: " , ", count: "1", flag: "true", meta: "{}" }).error,
    ).toBe("请求体.list 至少需要一个值");
    expect(
      form({ title: "t", list: "a", count: "1.5", flag: "true", meta: "{}" }).error,
    ).toBe("请求体.count 的值不符合 integer 类型");
    expect(
      form({ title: "t", list: "a", count: "1", flag: "yes", meta: "{}" }).error,
    ).toBe("请求体.flag 只能填写 true 或 false");
    expect(
      form({ title: "t", list: "a", count: "1", flag: "true", meta: "no" }).error,
    ).toBe("请求体.meta 需要填写 JSON 对象");
  });

  it("serialises an array field without an items schema as strings", () => {
    const built = build(
      target(
        requestBody({
          mediaType: "multipart/form-data",
          schema: { type: "object", properties: { tags: { type: "array" } } },
        }),
      ),
      { bodyValues: { tags: "a, b" } },
    );
    expect(built.error).toBeNull();
    const form = built.request?.init.body as FormData;
    expect(form.getAll("tags")).toEqual(["a", "b"]);
  });

  it("rejects a nested array item schema and an unresolvable item reference", () => {
    const nested = {
      type: "object",
      properties: { matrix: { type: "array", items: { type: "array" } } },
    };
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", schema: nested })), {
        bodyValues: { matrix: "a" },
      }).error,
    ).toBe("请求体.matrix 的数组 items schema 缺失");
    const broken = {
      type: "object",
      properties: { value: { type: "array", items: { $ref: "#/missing" } } },
    };
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", schema: broken })), {
        bodyValues: { value: "a" },
      }).error,
    ).toBe("无法解析 OpenAPI 引用：#/missing");
    const nonObject = {
      type: "object",
      properties: { value: { type: "array", items: { $ref: "#/bad" } } },
    };
    expect(
      build(target(requestBody({ mediaType: "multipart/form-data", schema: nonObject })), {
        bodyValues: { value: "a" },
        schemaRoot: { bad: 7 },
      }).error,
    ).toBe("表单字段 value 的 items schema 必须是对象");
  });

  it("omits an empty optional multipart form and removes the content type", () => {
    const built = build(
      target(
        requestBody({
          mediaType: "multipart/form-data",
          schema: { type: "object", properties: { note: { type: "string" } } },
        }),
      ),
    );
    expect(built.request?.init).not.toHaveProperty("body");
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
  });

  it("builds a urlencoded form with the declared content type", () => {
    const schema = {
      type: "object",
      required: ["title"],
      properties: {
        title: { type: "string" },
        count: { type: "integer" },
        tags: { type: "array", items: { type: "string" } },
      },
    };
    const built = build(
      target(requestBody({ mediaType: "application/x-www-form-urlencoded", schema })),
      { bodyValues: { title: "a b", count: "2", tags: "x,y" } },
    );
    expect(built.error).toBeNull();
    expect(built.request?.init.headers).toEqual({
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded",
    });
    const body = built.request?.init.body as URLSearchParams;
    expect(body).toBeInstanceOf(URLSearchParams);
    expect([...body.entries()]).toEqual([
      ["title", "a b"],
      ["count", "2"],
      ["tags", "x"],
      ["tags", "y"],
    ]);
  });

  it("omits an empty optional urlencoded body and its content type", () => {
    const built = build(
      target(
        requestBody({
          mediaType: "application/x-www-form-urlencoded",
          schema: { type: "object", properties: { note: { type: "string" } } },
        }),
      ),
    );
    expect(built.request?.init).not.toHaveProperty("body");
    expect(built.request?.init.headers).toEqual({ Accept: "application/json" });
  });
});

describe("apiRequestCode", () => {
  const jsonOperation = operation({ path: "/cases", method: "post" });

  it("returns null without a request", () => {
    expect(apiRequestCode(jsonOperation, null, {})).toBeNull();
  });

  it("prints a buffered text request", () => {
    const code = apiRequestCode(
      jsonOperation,
      {
        url: "/cases",
        init: {
          method: "POST",
          credentials: "same-origin",
          headers: { Accept: "application/json" },
          body: '{"a":1}',
        },
      },
      {},
    );
    expect(code).toBe(
      'const response = await fetch("/cases", {\n' +
        '  method: "POST",\n' +
        '  credentials: "same-origin",\n' +
        '  headers: {\n' +
        '    "Accept": "application/json"\n' +
        "  },\n" +
        '  body: "{\\"a\\":1}",\n' +
        "});\n" +
        "const rawResponse = await response.text();\n" +
        "console.log(response.status, rawResponse);",
    );
  });

  it("prints a URL-encoded form from its entries", () => {
    const body = new URLSearchParams();
    body.append("title", "a b");
    body.append("count", "2");
    const code = apiRequestCode(
      jsonOperation,
      {
        url: "/cases",
        init: { method: "POST", headers: {}, body },
      },
      {},
    );
    expect(code).toContain("const body = new URLSearchParams();");
    expect(code).toContain('body.append("title", "a b");');
    expect(code).toContain('body.append("count", "2");');
    expect(code).toContain("body: body,");
    expect(code).toContain('fetch("/cases"');
  });

  it("prints a multipart form with single and multiple file inputs", () => {
    const body = new FormData();
    body.append("title", "合同");
    body.append("attachment", new File(["x"], "a.txt"), "a.txt");
    body.append("gallery", new File(["x"], "g1.txt"), "g1.txt");
    body.append("gallery", new File(["y"], "g2.txt"), "g2.txt");
    const upload = operation({
      path: "/upload",
      method: "post",
      requestBody: requestBody({
        mediaType: "multipart/form-data",
        schema: {
          type: "object",
          properties: {
            title: { type: "string" },
            attachment: { type: "string", format: "binary" },
            gallery: { type: "array", items: { type: "string", format: "binary" } },
          },
        },
      }),
    });
    const code = apiRequestCode(
      upload,
      { url: "/upload", init: { method: "POST", headers: {}, body } },
      {},
    );
    expect(code).toContain("const body = new FormData();");
    expect(code).toContain('body.append("title", "合同");');
    expect(code).toContain(
      "const fileInput0 = Array.from(document.querySelectorAll('input[type=\"file\"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === \"attachment\");",
    );
    expect(code).toContain("const file1 = fileInput0?.files?.[0];");
    expect(code).toContain('if (!file1) throw new Error("请选择文件：attachment");');
    expect(code).toContain('body.append("attachment", file1);');
    expect(code).toContain(
      'for (const file of fileInput1?.files ?? []) body.append("gallery", file);',
    );
    expect(code).toContain("body: body,");
  });

  it("numbers file inputs after a multiple file field", () => {
    const body = new FormData();
    body.append("gallery", new File(["x"], "g1.txt"), "g1.txt");
    body.append("attachment", new File(["x"], "a.txt"), "a.txt");
    const upload = operation({
      path: "/upload",
      method: "post",
      requestBody: requestBody({
        mediaType: "multipart/form-data",
        schema: {
          type: "object",
          properties: {
            gallery: { type: "array", items: { type: "string", format: "binary" } },
            attachment: { type: "string", format: "binary" },
          },
        },
      }),
    });
    const code = apiRequestCode(
      upload,
      { url: "/upload", init: { method: "POST", headers: {}, body } },
      {},
    );
    expect(code).toContain(
      "const fileInput0 = Array.from(document.querySelectorAll('input[type=\"file\"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === \"gallery\");",
    );
    expect(code).toContain(
      'for (const file of fileInput0?.files ?? []) body.append("gallery", file);',
    );
    expect(code).toContain(
      "const fileInput1 = Array.from(document.querySelectorAll('input[type=\"file\"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === \"attachment\");",
    );
    expect(code).toContain("const file2 = fileInput1?.files?.[0];");
    expect(code).toContain('if (!file2) throw new Error("请选择文件：attachment");');
    expect(code).toContain('body.append("attachment", file2);');
  });

  it("prints a single-file input for a file field missing from the schema", () => {
    const body = new FormData();
    body.append("extra", new File(["x"], "x.bin"), "x.bin");
    const code = apiRequestCode(
      operation({ path: "/upload", method: "post" }),
      { url: "/upload", init: { method: "POST", headers: {}, body } },
      {},
    );
    expect(code).toContain(
      "const fileInput0 = Array.from(document.querySelectorAll('input[type=\"file\"][data-request-body-field]')).find((element) => element.dataset.requestBodyField === \"extra\");",
    );
    expect(code).toContain("const file1 = fileInput0?.files?.[0];");
    expect(code).toContain('if (!file1) throw new Error("请选择文件：extra");');
    expect(code).toContain('body.append("extra", file1);');
  });

  it("prints the form prelude for a form with no entries", () => {
    const code = apiRequestCode(
      operation({ path: "/upload", method: "post" }),
      { url: "/upload", init: { method: "POST", headers: {}, body: new FormData() } },
      {},
    );
    expect(code).toContain("const body = new FormData();\n");
    expect(code).toContain("body: body,");
  });

  it("returns null when the form fields cannot be parsed", () => {
    const body = new FormData();
    body.append("title", "x");
    const upload = operation({
      path: "/upload",
      method: "post",
      requestBody: requestBody({
        mediaType: "multipart/form-data",
        schema: { properties: 7 },
      }),
    });
    expect(
      apiRequestCode(upload, { url: "/upload", init: { method: "POST", body } }, {}),
    ).toBeNull();
  });

  it("prints the binary request file lookup", () => {
    const body = new File(["x"], "a.bin");
    const code = apiRequestCode(
      operation({
        path: "/upload",
        method: "post",
        requestBody: requestBody({ mediaType: "application/octet-stream" }),
      }),
      { url: "/upload", init: { method: "POST", headers: {}, body } },
      {},
    );
    expect(code).toContain(
      'const requestFileInput = Array.from(document.querySelectorAll(\'input[type="file"][data-request-body-field]\')).find((element) => element.dataset.requestBodyField === "__request_body__");',
    );
    expect(code).toContain("const body = requestFileInput?.files?.[0];");
    expect(code).toContain('if (!body) throw new Error("请选择请求原件");');
    expect(code).toContain("body: body,");
  });

  it("indents multi-line header objects", () => {
    const code = apiRequestCode(
      jsonOperation,
      {
        url: "/cases",
        init: {
          method: "GET",
          headers: { Accept: "application/json", "X-Trace": "1" },
        },
      },
      {},
    );
    expect(code).toContain('    "Accept": "application/json",');
    expect(code).toContain('    "X-Trace": "1"\n  },');
    expect(code).not.toContain("body:");
  });
});
