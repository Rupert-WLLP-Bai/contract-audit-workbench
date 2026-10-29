import { describe, expect, it } from "vitest";
import { parseOpenApiContract, resolveOpenApiReference } from "./openapi";

const minimalContract = {
  openapi: "3.0.3",
  info: { title: "审查接口", version: "1.2.3" },
  paths: {
    "/cases": {
      get: { responses: { "200": { description: "案件列表" } } },
    },
  },
};

describe("OpenAPI contract parsing", () => {
  it("parses the documented contract envelope and operation defaults", () => {
    const contract = parseOpenApiContract({
      ...minimalContract,
      info: { title: "审查接口", version: "1.2.3", description: "接口说明" },
      tags: [
        { name: "cases", description: "案件" },
        { name: "plain" },
      ],
      paths: {
        "/cases": {
          post: {
            operationId: "createCase",
            summary: "创建案件",
            description: "创建",
            tags: ["cases"],
            security: [{ bearer: [] }],
            responses: {
              "201": { description: "已创建" },
              "400": {},
            },
          },
        },
      },
    });
    expect(contract).toMatchObject({
      openapi: "3.0.3",
      title: "审查接口",
      version: "1.2.3",
      description: "接口说明",
      tags: [
        { name: "cases", description: "案件" },
        { name: "plain", description: "" },
      ],
    });
    expect(contract.raw).toMatchObject({ openapi: "3.0.3" });
    const [operation] = contract.operations;
    expect(operation).toMatchObject({
      key: "POST /cases",
      method: "POST",
      path: "/cases",
      operationId: "createCase",
      summary: "创建案件",
      description: "创建",
      tags: ["cases"],
      security: [{ bearer: [] }],
      requestBody: null,
    });
    expect(operation?.responses).toEqual([
      { status: "201", description: "已创建", raw: { description: "已创建" } },
      { status: "400", description: "响应说明未提供", raw: {} },
    ]);
    expect(operation?.raw).toMatchObject({ operationId: "createCase" });
  });

  it("falls back to the generated key for operation identity and summarises it", () => {
    const contract = parseOpenApiContract(minimalContract);
    expect(contract.tags).toEqual([]);
    expect(contract.description).toBe("");
    expect(contract.operations[0]).toMatchObject({
      key: "GET /cases",
      method: "GET",
      path: "/cases",
      operationId: "GET /cases",
      summary: "GET /cases",
      description: "",
      tags: [],
      parameters: [],
      security: null,
    });
  });

  it("merges path-level parameters with operation overrides and parses examples", () => {
    const contract = parseOpenApiContract({
      ...minimalContract,
      paths: {
        "/cases/{caseId}": {
          parameters: [
            {
              name: "caseId",
              in: "path",
              description: "案件标识",
              schema: { type: "string", example: "c-1" },
            },
            { name: "shared", in: "query", required: true, schema: { type: "string" } },
          ],
          get: {
            parameters: [
              { name: "shared", in: "query", description: "覆盖", example: 3, schema: { type: "integer" } },
              { name: "X-Trace", in: "header", schema: { type: "string" } },
              { name: "session", in: "cookie", required: true, schema: { type: "string" } },
            ],
            responses: { "200": { description: "ok" } },
          },
        },
      },
    });
    const operation = contract.operations[0];
    expect(operation?.parameters).toEqual([
      {
        name: "caseId",
        location: "path",
        required: true,
        description: "案件标识",
        schema: { type: "string", example: "c-1" },
        example: "c-1",
        raw: {
          name: "caseId",
          in: "path",
          description: "案件标识",
          schema: { type: "string", example: "c-1" },
        },
      },
      {
        name: "shared",
        location: "query",
        required: false,
        description: "覆盖",
        schema: { type: "integer" },
        example: 3,
        raw: {
          name: "shared",
          in: "query",
          description: "覆盖",
          example: 3,
          schema: { type: "integer" },
        },
      },
      {
        name: "X-Trace",
        location: "header",
        required: false,
        description: "",
        schema: { type: "string" },
        example: null,
        raw: { name: "X-Trace", in: "header", schema: { type: "string" } },
      },
      {
        name: "session",
        location: "cookie",
        required: true,
        description: "",
        schema: { type: "string" },
        example: null,
        raw: { name: "session", in: "cookie", required: true, schema: { type: "string" } },
      },
    ]);
  });

  it("resolves parameter references from the contract root", () => {
    const contract = parseOpenApiContract({
      ...minimalContract,
      components: {
        parameters: {
          Limit: { name: "limit", in: "query", schema: { type: "integer" } },
        },
      },
      paths: {
        "/cases": {
          get: {
            parameters: [{ $ref: "#/components/parameters/Limit" }],
            responses: { "200": { description: "ok" } },
          },
        },
      },
    });
    expect(contract.operations[0]?.parameters[0]).toMatchObject({
      name: "limit",
      location: "query",
      required: false,
    });
  });

  it("selects the application/json request body and falls back to the first media type", () => {
    const contract = parseOpenApiContract({
      ...minimalContract,
      paths: {
        "/a": {
          post: {
            requestBody: {
              required: true,
              description: "正文",
              content: {
                "text/plain": { schema: { type: "string" } },
                "application/json": { schema: { $ref: "#/components/schemas/Payload" } },
              },
            },
            responses: { "200": { description: "ok" } },
          },
        },
        "/b": {
          post: {
            requestBody: {
              content: { "application/xml": { example: "<p/>" } },
            },
            responses: { "200": { description: "ok" } },
          },
        },
      },
      components: {
        schemas: { Payload: { type: "object", example: { a: 1 } } },
      },
    });
    expect(contract.operations[0]?.requestBody).toMatchObject({
      required: true,
      description: "正文",
      mediaType: "application/json",
      schema: { $ref: "#/components/schemas/Payload" },
      example: null,
    });
    expect(contract.operations[1]?.requestBody).toMatchObject({
      required: false,
      description: "",
      mediaType: "application/xml",
      schema: null,
      example: "<p/>",
    });
  });

  it("prefers a media example, then the first named example, then schema example or default", () => {
    const parseBody = (content: Record<string, unknown>) =>
      parseOpenApiContract({
        ...minimalContract,
        paths: {
          "/a": {
            post: {
              requestBody: { content },
              responses: { "200": { description: "ok" } },
            },
          },
        },
      }).operations[0]?.requestBody;

    expect(
      parseBody({
        "application/json": { example: "media", examples: { one: { value: "named" } }, schema: { example: "schema" } },
      })?.example,
    ).toBe("media");
    expect(
      parseBody({
        "application/json": { examples: { one: { value: "named" }, two: { value: "later" } }, schema: { example: "schema" } },
      })?.example,
    ).toBe("named");
    expect(
      parseBody({ "application/json": { schema: { example: "schema", default: "default" } } })?.example,
    ).toBe("schema");
    expect(
      parseBody({ "application/json": { schema: { default: "default" } } })?.example,
    ).toBe("default");
    expect(parseBody({ "application/json": { schema: {} } })?.example).toBeNull();
    expect(parseBody({})).toMatchObject({
      mediaType: null,
      schema: null,
      example: null,
    });
  });

  it("keeps an empty request body when the content map is empty or absent", () => {
    const contract = parseOpenApiContract({
      ...minimalContract,
      paths: {
        "/a": {
          post: {
            requestBody: { required: true, description: "空" },
            responses: { "200": { description: "ok" } },
          },
        },
      },
    });
    expect(contract.operations[0]?.requestBody).toEqual({
      required: true,
      description: "空",
      mediaType: null,
      schema: null,
      example: null,
      raw: { required: true, description: "空" },
    });
  });

  it("rejects malformed contracts with the documented messages", () => {
    expect(() => parseOpenApiContract(null)).toThrow("OpenAPI 契约 必须是对象");
    expect(() => parseOpenApiContract("x")).toThrow("OpenAPI 契约 必须是对象");
    expect(() => parseOpenApiContract({})).toThrow("openapi 必须是字符串");
    expect(() => parseOpenApiContract({ openapi: "2.0" })).toThrow(
      "仅支持 OpenAPI 3.x 契约",
    );
    expect(() =>
      parseOpenApiContract({ openapi: "3.0.0", paths: {} }),
    ).toThrow("info 必须是对象");
    expect(() =>
      parseOpenApiContract({ openapi: "3.0.0", info: { title: "t", version: "1" } }),
    ).toThrow("paths 必须是对象");
    expect(() =>
      parseOpenApiContract({
        openapi: "3.0.0",
        info: { title: "t", version: "1" },
        paths: {},
      }),
    ).toThrow("OpenAPI 契约没有可用操作");
    expect(() =>
      parseOpenApiContract({
        openapi: "3.0.0",
        info: { title: 7, version: "1" },
        paths: { "/a": { get: { responses: { "200": {} } } } },
      }),
    ).toThrow("info.title 必须是字符串");
    expect(() =>
      parseOpenApiContract({
        openapi: "3.0.0",
        info: { title: "t", version: "1" },
        tags: [{ name: 7 }],
        paths: { "/a": { get: { responses: { "200": {} } } } },
      }),
    ).toThrow("tag.name 必须是字符串");
    expect(() =>
      parseOpenApiContract({
        openapi: "3.0.0",
        info: { title: "t", version: "1" },
        tags: [null],
        paths: { "/a": { get: { responses: { "200": {} } } } },
      }),
    ).toThrow("tag 必须是对象");
  });

  it("rejects malformed paths", () => {
    for (const path of ["cases", "//cases", "/cases?x=1", "/cases#frag"]) {
      expect(() =>
        parseOpenApiContract({
          ...minimalContract,
          paths: { [path]: { get: { responses: { "200": {} } } } },
        }),
      ).toThrow(`OpenAPI 路径无效：${path}`);
    }
    expect(() =>
      parseOpenApiContract({
        ...minimalContract,
        paths: { "/a": null },
      }),
    ).toThrow("paths./a 必须是对象");
  });

  it("rejects malformed parameters, request bodies and responses", () => {
    const withOperation = (patch: Record<string, unknown>) =>
      parseOpenApiContract({
        ...minimalContract,
        paths: {
          "/a": { get: { responses: { "200": {} }, ...patch } },
        },
      });
    expect(() => withOperation({ parameters: {} })).toThrow(
      "/a parameters 必须是数组",
    );
    expect(() => withOperation({ parameters: [null] })).toThrow(
      "参数 必须是对象",
    );
    expect(() =>
      withOperation({ parameters: [{ name: "p", in: "body" }] }),
    ).toThrow("不支持的参数位置：body");
    expect(() =>
      withOperation({ parameters: [{ name: 7, in: "query" }] }),
    ).toThrow("参数名称 必须是字符串");
    expect(() =>
      withOperation({ parameters: [{ name: "p", in: "query", schema: 7 }] }),
    ).toThrow("参数 schema 必须是对象");
    expect(() =>
      withOperation({ parameters: [{ name: "p", in: "query", required: "yes" }] }),
    ).toThrow("required 必须是布尔值");
    expect(() =>
      withOperation({ parameters: [{ name: "p", in: "query", description: 7 }] }),
    ).toThrow("description 必须是字符串");
    expect(() =>
      withOperation({ requestBody: 7 }),
    ).toThrow("请求体 必须是对象");
    expect(() =>
      withOperation({ requestBody: { content: 7 } }),
    ).toThrow("请求体 content 必须是对象");
    expect(() =>
      withOperation({ requestBody: { content: { "application/json": 7 } } }),
    ).toThrow("请求体 media type 必须是对象");
    expect(() =>
      withOperation({ requestBody: { content: { "application/json": { schema: 7 } } } }),
    ).toThrow("请求体 schema 必须是对象");
    expect(() =>
      withOperation({
        requestBody: { content: { "application/json": { examples: 7 } } },
      }),
    ).toThrow("请求体 examples 必须是对象");
    expect(() =>
      withOperation({
        requestBody: { content: { "application/json": { examples: { one: 7 } } } },
      }),
    ).toThrow("请求体 example 必须是对象");
    expect(() => withOperation({ responses: undefined })).toThrow(
      "responses 必须是对象",
    );
    expect(() =>
      parseOpenApiContract({
        ...minimalContract,
        paths: { "/a": { get: { responses: { "200": null } } } },
      }),
    ).toThrow("responses.200 必须是对象");
  });

  it("reads tags only when present", () => {
    expect(() =>
      parseOpenApiContract({ ...minimalContract, tags: {} }),
    ).toThrow("tags 必须是数组");
    expect(() =>
      parseOpenApiContract({ ...minimalContract, tags: [{}] }),
    ).toThrow("tag.name 必须是字符串");
  });
});

describe("OpenAPI reference resolution", () => {
  const root = {
    components: {
      schemas: {
        Payload: { type: "object", properties: { a: { type: "string" } } },
        Alias: { $ref: "#/components/schemas/Payload" },
        "a/b": { type: "string" },
        "c~d": { type: "number" },
        Loop: { $ref: "#/components/schemas/Loop" },
        Primitive: 7,
        Broken: { $ref: "#/components/schemas/Missing" },
        External: { $ref: "other.yaml#/Thing" },
      },
    },
  };

  it("returns inline values and follows encoded references", () => {
    const inline = { type: "integer" };
    expect(resolveOpenApiReference(inline, root)).toBe(inline);
    expect(resolveOpenApiReference({ $ref: "#/components/schemas/Payload" }, root)).toEqual(
      { type: "object", properties: { a: { type: "string" } } },
    );
    expect(resolveOpenApiReference({ $ref: "#/components/schemas/Alias" }, root)).toEqual({
      type: "object",
      properties: { a: { type: "string" } },
    });
    expect(
      resolveOpenApiReference({ $ref: "#/components/schemas/a~1b" }, root),
    ).toEqual({ type: "string" });
    expect(
      resolveOpenApiReference({ $ref: "#/components/schemas/c~0d" }, root),
    ).toEqual({ type: "number" });
    expect(
      resolveOpenApiReference({ $ref: "#/components/schemas/Primitive" }, root),
    ).toBe(7);
  });

  it("rejects external, cyclic and missing references", () => {
    expect(() =>
      resolveOpenApiReference({ $ref: "other.yaml#/Thing" }, root),
    ).toThrow("不支持外部 OpenAPI 引用：other.yaml#/Thing");
    expect(() =>
      resolveOpenApiReference({ $ref: "#/components/schemas/Loop" }, root),
    ).toThrow("OpenAPI 引用存在循环：#/components/schemas/Loop");
    expect(() =>
      resolveOpenApiReference({ $ref: "#/components/schemas/Missing" }, root),
    ).toThrow("无法解析 OpenAPI 引用：#/components/schemas/Missing");
    expect(() =>
      resolveOpenApiReference({ $ref: "#/components/schemas/Payload/nope" }, root),
    ).toThrow("无法解析 OpenAPI 引用：#/components/schemas/Payload/nope");
  });

  it("treats explicitly undefined optional fields as absent", () => {
    const contract = parseOpenApiContract({
      openapi: "3.0.3",
      info: { title: "审查接口", version: "1.2.3", description: undefined },
      paths: {
        "/cases": {
          get: {
            summary: undefined,
            required: undefined,
            responses: { "200": {} },
          },
        },
      },
    });
    expect(contract.description).toBe("");
    expect(contract.operations[0]?.summary).toBe("GET /cases");
  });

  it("keeps falsy parameter examples and accepts parameters without schemas", () => {
    const contract = parseOpenApiContract({
      openapi: "3.0.3",
      info: { title: "审查接口", version: "1.2.3" },
      paths: {
        "/cases": {
          get: {
            parameters: [
              { name: "q", in: "query", required: undefined },
              { name: "zero", in: "query", example: 0 },
              { name: "schema-zero", in: "query", schema: { type: "integer", example: 0 } },
            ],
            responses: { "200": {} },
          },
        },
      },
    });
    const [plain, zero, schemaZero] = contract.operations[0]?.parameters ?? [];
    expect(plain?.schema).toBeNull();
    expect(plain?.example).toBeNull();
    expect(zero?.example).toBe(0);
    expect(schemaZero?.example).toBe(0);
  });
});
