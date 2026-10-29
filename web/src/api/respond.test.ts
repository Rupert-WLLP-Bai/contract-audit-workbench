import { describe, expect, it } from "vitest";
import { ApiError } from "./http";
import { enumValue, invalidResponse, responseReader } from "./respond";

describe("invalidResponse", () => {
  it("throws one non-retryable invalid-response error with the given status", () => {
    let caught: unknown;
    try {
      invalidResponse("坏响应 cases", 503);
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect(caught).toMatchObject({
      name: "ApiError",
      message: "坏响应 cases",
      status: 503,
      code: "invalid_response",
      retryable: false,
      details: null,
    });
    expect(() => invalidResponse("默认状态")).toThrow(
      expect.objectContaining({ status: 200 }),
    );
  });
});

describe("enumValue", () => {
  const allowed = ["a", "b"] as const;
  it("returns a member or null without throwing", () => {
    expect(enumValue("a", allowed)).toBe("a");
    expect(enumValue("b", allowed)).toBe("b");
    expect(enumValue("c", allowed)).toBeNull();
    expect(enumValue(7, allowed)).toBeNull();
    expect(enumValue(null, allowed)).toBeNull();
    expect(enumValue(undefined, allowed)).toBeNull();
    expect(enumValue("", allowed)).toBeNull();
  });
});

describe("responseReader", () => {
  const reader = responseReader((path) => `坏响应 ${path}`, 422);
  const message = (path: string) => `坏响应 ${path}`;

  it("binds the family message and status into every invalid error", () => {
    expect(() => reader.invalid("x")).toThrow(
      expect.objectContaining({
        message: "坏响应 x",
        status: 422,
        code: "invalid_response",
      }),
    );
  });

  it("object accepts only plain records", () => {
    const item = { a: 1 };
    expect(reader.object(item, "x")).toBe(item);
    expect(() => reader.object(null, "x")).toThrow(message("x"));
    expect(() => reader.object([], "x")).toThrow(message("x"));
    expect(() => reader.object("s", "x")).toThrow(message("x"));
    expect(() => reader.object(3, "x")).toThrow(message("x"));
  });

  it("record checks known and required keys", () => {
    expect(reader.record({ a: 1, b: 2 }, "x", ["a", "b"])).toEqual({
      a: 1,
      b: 2,
    });
    expect(reader.record({ a: 1 }, "x", ["a", "b"], ["a"])).toEqual({ a: 1 });
    expect(() => reader.record({ a: 1, c: 2 }, "x", ["a", "b"])).toThrow(
      message("x"),
    );
    expect(() => reader.record({ a: 1 }, "x", ["a", "b"])).toThrow(
      message("x"),
    );
    expect(() => reader.record({}, "x", ["a"], ["a", "b"])).toThrow(
      message("x"),
    );
    expect(() => reader.record(null, "x", ["a"])).toThrow(message("x"));
  });

  it("text readers keep undefined, null and empty strings distinct", () => {
    expect(reader.text("", "x")).toBe("");
    expect(reader.text("v", "x")).toBe("v");
    expect(() => reader.text(null, "x")).toThrow(message("x"));
    expect(() => reader.text(1, "x")).toThrow(message("x"));
    expect(reader.optionalText(undefined, "x")).toBeNull();
    expect(reader.optionalText(null, "x")).toBeNull();
    expect(reader.optionalText("v", "x")).toBe("v");
    expect(() => reader.optionalText(1, "x")).toThrow(message("x"));
    expect(reader.nullableText(null, "x")).toBeNull();
    expect(reader.nullableText("v", "x")).toBe("v");
    expect(() => reader.nullableText(undefined, "x")).toThrow(message("x"));
    expect(reader.maybeText(undefined, "x")).toBeUndefined();
    expect(reader.maybeText("v", "x")).toBe("v");
    expect(() => reader.maybeText(null, "x")).toThrow(message("x"));
  });

  it("integer accepts only safe integers above an optional minimum", () => {
    expect(reader.integer(0, "x")).toBe(0);
    expect(reader.integer(-3, "x")).toBe(-3);
    expect(reader.integer(3, "x", 3)).toBe(3);
    expect(() => reader.integer(2, "x", 3)).toThrow(message("x"));
    expect(() => reader.integer(1.5, "x")).toThrow(message("x"));
    expect(() => reader.integer(Number.MAX_SAFE_INTEGER + 1, "x")).toThrow(
      message("x"),
    );
    expect(() => reader.integer("1", "x")).toThrow(message("x"));
    expect(() => reader.integer(Number.NaN, "x")).toThrow(message("x"));
  });

  it("bool and nullableBool preserve null as a value", () => {
    expect(reader.bool(true, "x")).toBe(true);
    expect(reader.bool(false, "x")).toBe(false);
    expect(() => reader.bool(0, "x")).toThrow(message("x"));
    expect(reader.nullableBool(null, "x")).toBeNull();
    expect(reader.nullableBool(true, "x")).toBe(true);
    expect(() => reader.nullableBool(undefined, "x")).toThrow(message("x"));
  });

  it("array maps entries with indexed error paths", () => {
    expect(reader.array([1, 2], "x", (entry, path) => {
      if (typeof entry !== "number") return reader.invalid(path);
      return entry * 2;
    })).toEqual([2, 4]);
    expect(() => reader.array([1, "b"], "x", (entry, path) => {
      if (typeof entry !== "number") return reader.invalid(path);
      return entry;
    })).toThrow(message("x[1]"));
    expect(() => reader.array(null, "x", (entry) => entry)).toThrow(
      message("x"),
    );
  });

  it("nullableArray and optionalArray keep null and undefined distinct", () => {
    const identity = (entry: unknown) => entry;
    expect(reader.nullableArray(null, "x", identity)).toBeNull();
    expect(reader.nullableArray([1], "x", identity)).toEqual([1]);
    expect(() => reader.nullableArray(undefined, "x", identity)).toThrow(
      message("x"),
    );
    expect(reader.optionalArray(undefined, "x", identity)).toBeUndefined();
    expect(reader.optionalArray([1], "x", identity)).toEqual([1]);
    expect(() => reader.optionalArray(null, "x", identity)).toThrow(
      message("x"),
    );
  });
});
