import { describe, expect, it } from "vitest";
import { ago, bytes, count, duration, fields, levelName, plural, searchWords } from "./lib";
import { parseRoute } from "./router";

describe("format", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  it("ago", () => {
    expect(ago("2026-10-02T11:59:30Z", now)).toBe("just now");
    expect(ago("2026-10-02T09:00:00Z", now)).toBe("3h ago");
    expect(ago("2026-09-28T12:00:00Z", now)).toBe("4d ago");
  });
  it("plural", () => {
    expect(plural(1, "line")).toBe("1 line");
    expect(plural(3, "line")).toBe("3 lines");
  });
  it("sizes and counts", () => {
    expect(bytes(512)).toBe("512 B");
    expect(bytes(256 * 1024 * 1024)).toBe("256 MB");
    expect(bytes(1.5 * 1024 * 1024)).toBe("1.5 MB");
    expect(count(12_345)).toBe("12k");
    expect(duration(850)).toBe("850 ms");
    expect(duration(2500)).toBe("2.5 s");
    expect(duration(125_000)).toBe("2m 5s");
  });
  it("levels", () => {
    expect(levelName(-4)).toBe("debug");
    expect(levelName(8)).toBe("error");
  });
  it("fields", () => {
    expect(fields('{"status":404,"path":"/x"}')).toEqual([
      ["status", "404"],
      ["path", "/x"],
    ]);
    expect(fields("not json")).toEqual([["attrs", "not json"]]);
  });
  it("search words", () => {
    expect(searchWords("retry 3 failed: dial tcp 10.0.0.1:443")).toBe("retry failed dial tcp");
  });
});

describe("router", () => {
  it("parses routes", () => {
    expect(parseRoute("/")).toEqual({ page: "home" });
    expect(parseRoute("/about")).toEqual({ page: "about" });
    expect(parseRoute("/sources")).toEqual({ page: "sources" });
    expect(parseRoute("/trace/abc")).toEqual({ page: "trace", id: "abc" });
    const r = parseRoute("/logs", "?source=ledger&level=error");
    expect(r.page === "logs" && r.query.get("source")).toBe("ledger");
    expect(parseRoute("/nope")).toEqual({ page: "home" });
  });
});
