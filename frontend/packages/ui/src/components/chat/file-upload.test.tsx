import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  FileDropOverlay,
  MAX_UPLOAD_SIZE,
  defaultFileNameForMime,
  filesFromDataTransfer,
  hasFileDrag,
  isAllowedUpload,
} from "./FileUpload";

function fakeDataTransfer(types: string[], files: unknown[] = []): DataTransfer {
  return { types, files } as unknown as DataTransfer;
}

function fakeFile(overrides: Partial<{ size: number; type: string; name: string }> = {}): File {
  return { size: 1024, type: "image/png", name: "a.png", ...overrides } as unknown as File;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("isAllowedUpload", () => {
  it("accepts a supported file within the size limit", () => {
    const alert = vi.fn();
    vi.stubGlobal("alert", alert);
    expect(isAllowedUpload(fakeFile())).toBe(true);
    expect(alert).not.toHaveBeenCalled();
  });

  it("rejects a file over the size limit", () => {
    const alert = vi.fn();
    vi.stubGlobal("alert", alert);
    expect(isAllowedUpload(fakeFile({ size: MAX_UPLOAD_SIZE + 1 }))).toBe(false);
    expect(alert).toHaveBeenCalledWith(expect.stringContaining("too large"));
  });

  it("rejects an unsupported mime type", () => {
    const alert = vi.fn();
    vi.stubGlobal("alert", alert);
    expect(isAllowedUpload(fakeFile({ type: "application/zip" }))).toBe(false);
    expect(alert).toHaveBeenCalledWith(expect.stringContaining("not allowed"));
  });
});

describe("defaultFileNameForMime", () => {
  it("maps known mime types to an extension", () => {
    expect(defaultFileNameForMime("application/pdf")).toMatch(/\.pdf$/);
    expect(defaultFileNameForMime("image/png")).toMatch(/\.png$/);
  });

  it("falls back to .bin for unknown types", () => {
    expect(defaultFileNameForMime("application/zip")).toMatch(/\.bin$/);
  });
});

describe("hasFileDrag", () => {
  it("detects file drags", () => {
    expect(hasFileDrag(fakeDataTransfer(["Files"]))).toBe(true);
  });

  it("ignores text drags and empty payloads", () => {
    expect(hasFileDrag(fakeDataTransfer(["text/plain"]))).toBe(false);
    expect(hasFileDrag(null)).toBe(false);
    expect(hasFileDrag(undefined)).toBe(false);
  });
});

describe("filesFromDataTransfer", () => {
  it("returns the dropped files as an array", () => {
    const files = [fakeFile({ name: "one" }), fakeFile({ name: "two" })];
    expect(filesFromDataTransfer(fakeDataTransfer(["Files"], files))).toEqual(files);
  });

  it("returns an empty array without a payload", () => {
    expect(filesFromDataTransfer(null)).toEqual([]);
  });
});

describe("FileDropOverlay", () => {
  it("renders the drop affordance when visible", () => {
    const html = renderToStaticMarkup(<FileDropOverlay visible />);
    expect(html).toContain("Drop files to attach");
    expect(html).toContain("message-composer-dropzone");
  });

  it("renders nothing when hidden", () => {
    expect(renderToStaticMarkup(<FileDropOverlay visible={false} />)).toBe("");
  });
});
