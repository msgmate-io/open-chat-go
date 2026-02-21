import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  FileDropOverlay,
  MAX_ATTACHMENT_COUNT,
  MAX_UPLOAD_SIZE,
  MAX_UPLOAD_SIZE_LABEL,
  SUPPORTED_UPLOAD_TYPES_LABEL,
  UploadErrorsNotice,
  collectDroppedItems,
  defaultFileNameForMime,
  filesFromDataTransfer,
  formatFileSize,
  getUploadRejection,
  hasDirectoryDrag,
  hasFileDrag,
  isAllowedUpload,
  isSupportedUploadType,
  partitionUploadBatch,
  uploadRejectionMessage,
  type UploadRejectionReason,
} from "./FileUpload";

function fakeDataTransfer(types: string[], files: unknown[] = []): DataTransfer {
  return { types, files } as unknown as DataTransfer;
}

function fakeFile(overrides: Partial<{ size: number; type: string; name: string }> = {}): File {
  return { size: 1024, type: "image/png", name: "a.png", ...overrides } as unknown as File;
}

/**
 * Mimics the shape Chrome/Firefox hand a drop event for a dragged folder: a
 * `file` entry whose `webkitGetAsEntry()` reports a directory and whose file
 * object is a zero-byte, typeless placeholder.
 */
function fakeFolderDataTransfer(folderNames: string[], files: File[] = []): DataTransfer {
  const items = folderNames.map((name) => ({
    kind: "file",
    webkitGetAsEntry: () => ({ isDirectory: true }),
    getAsFile: () => fakeFile({ name, size: 0, type: "" }),
  }));
  return { types: ["Files"], files, items } as unknown as DataTransfer;
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

describe("formatFileSize", () => {
  it("renders bytes, kilobytes and megabytes", () => {
    expect(formatFileSize(512)).toBe("512 B");
    expect(formatFileSize(2048)).toBe("2.0 KB");
    expect(formatFileSize(5 * 1024 * 1024)).toBe("5.0 MB");
  });

  it("falls back for a nonsensical size", () => {
    expect(formatFileSize(Number.NaN)).toBe("unknown size");
    expect(formatFileSize(-1)).toBe("unknown size");
  });
});

describe("getUploadRejection", () => {
  it("returns null for a supported, small file", () => {
    expect(getUploadRejection(fakeFile())).toBeNull();
  });

  it("reports the offending size in the message", () => {
    const rejection = getUploadRejection(
      fakeFile({ name: "huge.pdf", type: "application/pdf", size: 6 * 1024 * 1024 })
    );
    expect(rejection?.reason).toBe("too-large");
    expect(rejection?.message).toContain("huge.pdf");
    expect(rejection?.message).toContain("6.0 MB");
    expect(rejection?.message).toContain(MAX_UPLOAD_SIZE_LABEL);
  });

  it("lists the supported types for an unsupported file", () => {
    const rejection = getUploadRejection(fakeFile({ name: "archive.zip", type: "application/zip" }));
    expect(rejection?.reason).toBe("unsupported-type");
    expect(rejection?.message).toContain(SUPPORTED_UPLOAD_TYPES_LABEL);
  });

  it("lets a typeless file through — the upload endpoint validates it", () => {
    expect(isSupportedUploadType("")).toBe(true);
    expect(isSupportedUploadType(undefined)).toBe(true);
    expect(getUploadRejection(fakeFile({ type: "" }))).toBeNull();
  });
});

describe("partitionUploadBatch", () => {
  it("accepts a mixed batch and reports only the offenders", () => {
    const good = fakeFile({ name: "ok.png" });
    const huge = fakeFile({ name: "huge.png", size: MAX_UPLOAD_SIZE + 1 });
    const zipped = fakeFile({ name: "bad.zip", type: "application/zip" });

    const { accepted, rejections } = partitionUploadBatch([good, huge, zipped]);

    expect(accepted).toEqual([good]);
    expect(rejections.map((rejection) => [rejection.fileName, rejection.reason])).toEqual([
      ["huge.png", "too-large"],
      ["bad.zip", "unsupported-type"],
    ]);
  });

  it("enforces the per-message cap, counting already staged attachments", () => {
    const files = Array.from({ length: 4 }, (_, index) => fakeFile({ name: `f${index}.png` }));

    const { accepted, rejections } = partitionUploadBatch(files, {
      maxFiles: 3,
      currentCount: 2,
    });

    expect(accepted).toHaveLength(1);
    expect(rejections).toHaveLength(3);
    expect(rejections.every((rejection) => rejection.reason === "too-many-files")).toBe(true);
    expect(rejections[0].message).toContain(`at most ${MAX_ATTACHMENT_COUNT} files`);
  });

  it("rejects everything once the cap is already reached", () => {
    const { accepted, rejections } = partitionUploadBatch([fakeFile()], {
      maxFiles: 2,
      currentCount: 2,
    });
    expect(accepted).toEqual([]);
    expect(rejections).toHaveLength(1);
  });

  it("accepts an empty batch", () => {
    expect(partitionUploadBatch([])).toEqual({ accepted: [], rejections: [] });
  });
});

describe("uploadRejectionMessage", () => {
  it("has a message for every reason", () => {
    const reasons: UploadRejectionReason[] = [
      "too-large",
      "unsupported-type",
      "folder",
      "too-many-files",
      "upload-failed",
    ];
    for (const reason of reasons) {
      const message = uploadRejectionMessage(reason, "notes.txt");
      expect(message.length).toBeGreaterThan(0);
    }
  });

  it("falls back to a generic subject without a file name", () => {
    expect(uploadRejectionMessage("too-large", "")).toContain("That file");
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

describe("collectDroppedItems", () => {
  it("separates a dropped folder from the files beside it", () => {
    const dropped = fakeFile({ name: "notes.txt", type: "text/plain" });
    const { files, folders } = collectDroppedItems(fakeFolderDataTransfer(["photos"], [dropped]));

    expect(folders).toEqual(["photos"]);
    expect(files).toEqual([dropped]);
  });

  it("keeps a zero-byte file that is not the folder placeholder", () => {
    const { files, folders } = collectDroppedItems(
      fakeDataTransfer(["Files"], [fakeFile({ name: "empty.txt", size: 0, type: "text/plain" })])
    );
    expect(folders).toEqual([]);
    expect(files[0].name).toBe("empty.txt");
  });

  it("tolerates a payload without items", () => {
    expect(collectDroppedItems(fakeDataTransfer(["Files"]))).toEqual({ files: [], folders: [] });
    expect(collectDroppedItems(null)).toEqual({ files: [], folders: [] });
  });

  it("flags folder drags via hasDirectoryDrag", () => {
    expect(hasDirectoryDrag(fakeFolderDataTransfer(["docs"]))).toBe(true);
    expect(hasDirectoryDrag(fakeDataTransfer(["Files"], [fakeFile()]))).toBe(false);
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

describe("FileDropOverlay", () => {
  it("renders the drop affordance with the accepted types and cap when visible", () => {
    const html = renderToStaticMarkup(<FileDropOverlay visible />);
    expect(html).toContain("Drop files to attach");
    expect(html).toContain("message-composer-dropzone");
    expect(html).toContain("message-composer-dropzone-hint");
    expect(html).toContain(MAX_UPLOAD_SIZE_LABEL);
  });

  it("renders nothing when hidden", () => {
    expect(renderToStaticMarkup(<FileDropOverlay visible={false} />)).toBe("");
  });
});

describe("UploadErrorsNotice", () => {
  it("lists every rejection in an announced error surface", () => {
    const html = renderToStaticMarkup(
      <UploadErrorsNotice
        errors={[
          { fileName: "big.pdf", reason: "too-large", message: "“big.pdf” is too big." },
          { fileName: "bad.zip", reason: "unsupported-type", message: "“bad.zip” isn’t supported." },
        ]}
        onDismiss={() => {}}
      />
    );

    expect(html).toContain('role="alert"');
    expect(html).toContain("message-composer-errors");
    expect(html).toContain("big.pdf");
    expect(html).toContain("bad.zip");
    expect(html).toContain("Dismiss attachment errors");
  });

  it("renders nothing without rejections", () => {
    expect(renderToStaticMarkup(<UploadErrorsNotice errors={[]} onDismiss={() => {}} />)).toBe("");
  });
});
