import React, { useCallback, useRef, useState } from "react";
import { Button } from "../button";
import { DropdownMenuItem } from "../dropdown-menu";
import { CircleAlert, File, FileText, Image as ImageIcon, Loader2, Paperclip, X } from "lucide-react";
import { cn } from "../../lib/utils";

export interface UploadedFileRecord {
  fileId: string;
  fileName: string;
  displayName?: string;
  mimeType?: string;
  size?: number;
}

interface FileUploadBaseProps {
  onFileUploaded: (record: UploadedFileRecord) => void;
  reuploadToOpenAI?: boolean;
}

interface UploadedFileResponse {
  file_id: string;
  file_name: string;
  size: number;
  mime_type: string;
  uploaded_at: string;
  openai_file_id?: string;
}

export const MAX_UPLOAD_SIZE = 5 * 1024 * 1024;

export const ALLOWED_UPLOAD_TYPES = [
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "application/pdf",
  "text/plain",
  "application/msword",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.ms-excel",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
];

// The image-only picker restricts to the raster types the upload endpoint
// accepts so mobile galleries/cameras cannot hand us an unsupported format.
export const IMAGE_UPLOAD_ACCEPT = "image/jpeg,image/png,image/gif,image/webp";
export const FILE_UPLOAD_ACCEPT =
  ".jpg,.jpeg,.png,.gif,.webp,.pdf,.txt,.doc,.docx,.xls,.xlsx";

export function isImageMimeType(mimeType?: string): boolean {
  return typeof mimeType === "string" && mimeType.startsWith("image/");
}

/** Per-message attachment cap so a multi-file drop cannot flood a chat. */
export const MAX_ATTACHMENT_COUNT = 10;

export const SUPPORTED_UPLOAD_TYPES_LABEL = "images, PDF, text, Word or Excel files";

/** Human-readable byte size (`4.6 MB`), used in the rejection messages. */
export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "unknown size";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export const MAX_UPLOAD_SIZE_LABEL = formatFileSize(MAX_UPLOAD_SIZE);

export type UploadRejectionReason =
  | "too-large"
  | "unsupported-type"
  | "folder"
  | "too-many-files"
  | "upload-failed";

export interface UploadRejection {
  fileName: string;
  reason: UploadRejectionReason;
  message: string;
}

export function uploadRejectionMessage(
  reason: UploadRejectionReason,
  fileName: string,
  fileSize?: number
): string {
  const label = fileName?.trim() || "That file";
  switch (reason) {
    case "too-large":
      return `“${label}” is ${
        typeof fileSize === "number" ? formatFileSize(fileSize) : "too large"
      } — the maximum size is ${MAX_UPLOAD_SIZE_LABEL}.`;
    case "unsupported-type":
      return `“${label}” isn’t a supported file type — attach ${SUPPORTED_UPLOAD_TYPES_LABEL}.`;
    case "folder":
      return "Folders can’t be attached — drop or pick the files inside instead.";
    case "too-many-files":
      return `You can attach at most ${MAX_ATTACHMENT_COUNT} files to a message.`;
    case "upload-failed":
      return `“${label}” couldn’t be uploaded. Please try again.`;
  }
}

export function isSupportedUploadType(mimeType?: string): boolean {
  // Browsers omit the type for some files; the upload endpoint validates those.
  if (!mimeType) return true;
  return ALLOWED_UPLOAD_TYPES.includes(mimeType);
}

/**
 * Non-alerting single-file validation. Returns the rejection to show in the
 * composer's inline error surface, or `null` when the file is fine. Keeping the
 * messages in data (rather than an `alert`) is what lets drops and picker
 * selections fail loudly *and* stay readable.
 */
export function getUploadRejection(file: File): UploadRejection | null {
  if (file.size > MAX_UPLOAD_SIZE) {
    return {
      fileName: file.name,
      reason: "too-large",
      message: uploadRejectionMessage("too-large", file.name, file.size),
    };
  }

  if (!isSupportedUploadType(file.type)) {
    return {
      fileName: file.name,
      reason: "unsupported-type",
      message: uploadRejectionMessage("unsupported-type", file.name),
    };
  }

  return null;
}

/**
 * Split a drop/picker batch into the files that can be uploaded and the
 * per-file rejections to surface. Validates size and type and enforces the
 * per-message cap, so an oversized multi-file drop degrades into "these three
 * were attached, that one was too big" instead of silently uploading a subset.
 */
export function partitionUploadBatch(
  files: File[],
  { maxFiles = MAX_ATTACHMENT_COUNT, currentCount = 0 }: { maxFiles?: number; currentCount?: number } = {}
): { accepted: File[]; rejections: UploadRejection[] } {
  const accepted: File[] = [];
  const rejections: UploadRejection[] = [];
  let slots = Math.max(0, maxFiles - currentCount);

  for (const file of files) {
    const rejection = getUploadRejection(file);
    if (rejection) {
      rejections.push(rejection);
      continue;
    }

    if (slots <= 0) {
      rejections.push({
        fileName: file.name,
        reason: "too-many-files",
        message: uploadRejectionMessage("too-many-files", file.name),
      });
      continue;
    }

    slots -= 1;
    accepted.push(file);
  }

  return { accepted, rejections };
}

export interface DroppedItems {
  files: File[];
  folders: string[];
}

/**
 * Files vs. folders in a drop payload. A dropped folder can never be uploaded,
 * and browsers surface it as a zero-byte, typeless entry that would otherwise
 * be POSTed as an empty file, so it is filtered out and reported separately.
 */
export function collectDroppedItems(
  dataTransfer: DataTransfer | null | undefined
): DroppedItems {
  const folders: string[] = [];
  const items = Array.from(dataTransfer?.items ?? []);

  for (const item of items) {
    if (item.kind !== "file") continue;
    const entry = (
      item as DataTransferItem & {
        webkitGetAsEntry?: () => { isDirectory?: boolean } | null;
      }
    ).webkitGetAsEntry?.();
    if (entry?.isDirectory) {
      folders.push(item.getAsFile?.()?.name ?? "folder");
    }
  }

  const files = filesFromDataTransfer(dataTransfer).filter(
    (file) => !(file.size === 0 && !file.type && folders.includes(file.name))
  );

  return { files, folders };
}

/** Does the drag payload carry any folder (as opposed to only files)? */
export function hasDirectoryDrag(dataTransfer: DataTransfer | null | undefined): boolean {
  return collectDroppedItems(dataTransfer).folders.length > 0;
}

/**
 * Pure helper: does this drag payload carry files (as opposed to a text
 * selection or a link)? Only file drags should open the drop affordance and be
 * accepted on drop, so dragging selected text over the composer is left alone.
 */
export function hasFileDrag(dataTransfer: DataTransfer | null | undefined): boolean {
  if (!dataTransfer) return false;
  return Array.from(dataTransfer.types ?? []).includes("Files");
}

/** Pure helper: the files carried by a drop event, as a plain array. */
export function filesFromDataTransfer(dataTransfer: DataTransfer | null | undefined): File[] {
  if (!dataTransfer) return [];
  return Array.from(dataTransfer.files ?? []);
}

/**
 * Single-file check with an `alert` fallback, kept for hosts that render no
 * inline error surface. New code should prefer `getUploadRejection` /
 * `partitionUploadBatch`, which return messages instead of interrupting.
 */
export function isAllowedUpload(file: File): boolean {
  const rejection = getUploadRejection(file);
  if (!rejection) return true;

  alert(
    rejection.reason === "too-large"
      ? "File too large. Maximum size is 5MB."
      : "File type not allowed."
  );
  return false;
}

const MIME_EXTENSIONS: Record<string, string> = {
  "image/jpeg": "jpg",
  "image/png": "png",
  "image/gif": "gif",
  "image/webp": "webp",
  "application/pdf": "pdf",
  "text/plain": "txt",
  "application/msword": "doc",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
  "application/vnd.ms-excel": "xls",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
};

export function defaultFileNameForMime(mimeType: string): string {
  const extension = MIME_EXTENSIONS[mimeType] ?? "bin";
  return `pasted-file-${Date.now()}.${extension}`;
}

export async function uploadFileToServer(
  file: File,
  reuploadToOpenAI = false
): Promise<UploadedFileResponse> {
  const formData = new FormData();
  const fileName = file.name?.trim() ? file.name : defaultFileNameForMime(file.type);
  formData.append("file", file, fileName);

  const url = new URL("/api/v1/files/upload", window.location.origin);
  if (reuploadToOpenAI) {
    url.searchParams.set("reupload_to_openai", "true");
  }

  const response = await fetch(url.toString(), {
    method: "POST",
    body: formData,
    credentials: "include",
  });

  if (!response.ok) {
    throw new Error(`Upload failed: ${response.statusText}`);
  }

  return (await response.json()) as UploadedFileResponse;
}

export interface FileUploadOptions {
  onFileUploaded: (record: UploadedFileRecord) => void;
  reuploadToOpenAI?: boolean;
  /** Per-message attachment cap; files beyond it are rejected, not uploaded. */
  maxFiles?: number;
  /** Attachments already staged, so the cap counts those too. */
  attachmentCount?: number;
  /** Inline error surface; without it rejections fall back to `alert`. */
  onRejected?: (rejections: UploadRejection[]) => void;
}

export function useFileUpload({
  onFileUploaded,
  reuploadToOpenAI = false,
  maxFiles = MAX_ATTACHMENT_COUNT,
  attachmentCount = 0,
  onRejected,
}: FileUploadOptions) {
  const [uploadingCount, setUploadingCount] = useState(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const reportRejections = useCallback(
    (rejections: UploadRejection[]) => {
      if (rejections.length === 0) return;
      if (onRejected) {
        onRejected(rejections);
        return;
      }
      // Legacy hosts (FileAttachButton / FileUpload) render no error surface.
      alert(rejections.map((rejection) => rejection.message).join("\n"));
    },
    [onRejected]
  );

  const uploadFiles = useCallback(
    async (files: File[]) => {
      if (files.length === 0) return;

      const { accepted, rejections } = partitionUploadBatch(files, {
        maxFiles,
        currentCount: attachmentCount,
      });
      reportRejections(rejections);
      if (accepted.length === 0) return;

      setUploadingCount((count) => count + accepted.length);
      try {
        // Upload concurrently but keep the caller's order by awaiting each
        // result and reporting as it resolves; a failed file never blocks the
        // others.
        await Promise.all(
          accepted.map(async (file) => {
            try {
              const uploadedFile = await uploadFileToServer(file, reuploadToOpenAI);
              onFileUploaded({
                fileId: uploadedFile.file_id,
                fileName: uploadedFile.file_name,
                mimeType: uploadedFile.mime_type,
                size: uploadedFile.size,
              });
            } catch (error) {
              console.error("File upload error:", error);
              reportRejections([
                {
                  fileName: file.name,
                  reason: "upload-failed",
                  message: uploadRejectionMessage("upload-failed", file.name),
                },
              ]);
            }
          })
        );
      } finally {
        setUploadingCount((count) => Math.max(0, count - accepted.length));
      }
    },
    [attachmentCount, maxFiles, onFileUploaded, reportRejections, reuploadToOpenAI]
  );

  const uploadFile = useCallback((file: File) => uploadFiles([file]), [uploadFiles]);

  const handleFileSelect = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = event.target.files;
      if (!files || files.length === 0) return;

      // Support multi-file selection: upload every chosen file, not just the
      // first. Clear the input so the same selection can be picked again.
      const selected = Array.from(files);
      event.target.value = "";
      await uploadFiles(selected);
    },
    [uploadFiles]
  );

  return {
    isUploading: uploadingCount > 0,
    fileInputRef,
    handleFileSelect,
    uploadFile,
    uploadFiles,
    openFilePicker: () => fileInputRef.current?.click(),
  };
}

/**
 * Drag-and-drop source for the composer. Returns a small set of props to
 * spread onto the drop target and an `isDragging` flag for the visual
 * affordance. A depth counter keeps the highlight stable while the pointer
 * moves across the target's own children (each child fires dragenter/leave).
 */
export function useFileDropzone({
  onFilesDropped,
  onFoldersDropped,
  disabled = false,
}: {
  onFilesDropped: (files: File[]) => void | Promise<void>;
  /** Called with the folder names when the payload carries a folder. */
  onFoldersDropped?: (folderNames: string[]) => void;
  disabled?: boolean;
}) {
  const [isDragging, setIsDragging] = useState(false);
  const dragDepth = useRef(0);

  const reset = useCallback(() => {
    dragDepth.current = 0;
    setIsDragging(false);
  }, []);

  const onDragEnter = useCallback(
    (event: React.DragEvent<HTMLElement>) => {
      if (disabled || !hasFileDrag(event.dataTransfer)) return;
      event.preventDefault();
      dragDepth.current += 1;
      setIsDragging(true);
    },
    [disabled]
  );

  const onDragOver = useCallback(
    (event: React.DragEvent<HTMLElement>) => {
      if (disabled || !hasFileDrag(event.dataTransfer)) return;
      // Preventing the default is what marks the element as a valid drop
      // target; without it the browser refuses the drop.
      event.preventDefault();
      event.dataTransfer.dropEffect = "copy";
    },
    [disabled]
  );

  const onDragLeave = useCallback(
    (event: React.DragEvent<HTMLElement>) => {
      if (disabled || !hasFileDrag(event.dataTransfer)) return;
      dragDepth.current = Math.max(0, dragDepth.current - 1);
      if (dragDepth.current === 0) setIsDragging(false);
    },
    [disabled]
  );

  const onDrop = useCallback(
    (event: React.DragEvent<HTMLElement>) => {
      if (disabled || !hasFileDrag(event.dataTransfer)) return;
      event.preventDefault();
      const { files, folders } = collectDroppedItems(event.dataTransfer);
      reset();
      if (folders.length > 0) onFoldersDropped?.(folders);
      if (files.length > 0) {
        void onFilesDropped(files);
      }
    },
    [disabled, onFilesDropped, onFoldersDropped, reset]
  );

  return {
    isDragging,
    dropzoneProps: { onDragEnter, onDragOver, onDragLeave, onDrop },
  };
}

/**
 * Visual affordance shown over the composer while files are dragged onto it.
 * Purely presentational; the drop target itself lives on the wrapper.
 */
export function FileDropOverlay({ visible }: { visible: boolean }) {
  if (!visible) return null;

  return (
    <div className="message-composer-dropzone" role="presentation" aria-hidden="true">
      <Paperclip className="size-5" />
      <span>Drop files to attach</span>
      <span className="message-composer-dropzone-hint">
        {SUPPORTED_UPLOAD_TYPES_LABEL} · up to {MAX_UPLOAD_SIZE_LABEL} each
      </span>
    </div>
  );
}

export function getFileIcon(mimeType?: string) {
  if (isImageMimeType(mimeType)) {
    return <ImageIcon className="size-4 shrink-0" />;
  }
  if (mimeType === "application/pdf") {
    return <FileText className="size-4 shrink-0" />;
  }
  return <File className="size-4 shrink-0" />;
}

/**
 * A hidden, stable file input. It is intentionally rendered outside any
 * dropdown/portal: on the native WebView the host's file chooser callback can
 * fire asynchronously, so the input must not be unmounted when the options menu
 * closes right after the item is selected.
 */
export function FilePickerInput({
  inputRef,
  accept,
  multiple = true,
  onFileSelect,
}: {
  inputRef: React.RefObject<HTMLInputElement | null>;
  accept: string;
  multiple?: boolean;
  onFileSelect: (event: React.ChangeEvent<HTMLInputElement>) => void;
}) {
  return (
    <input
      ref={inputRef}
      type="file"
      accept={accept}
      multiple={multiple}
      onChange={onFileSelect}
      className="hidden"
      tabIndex={-1}
      aria-hidden="true"
    />
  );
}

export function FileAttachButton({
  onFileUploaded,
  reuploadToOpenAI = false,
  className,
}: FileUploadBaseProps & { className?: string }) {
  const { isUploading, fileInputRef, handleFileSelect, openFilePicker } = useFileUpload({
    onFileUploaded,
    reuploadToOpenAI,
  });

  return (
    <>
      <FilePickerInput
        inputRef={fileInputRef}
        accept={FILE_UPLOAD_ACCEPT}
        onFileSelect={handleFileSelect}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        onClick={openFilePicker}
        disabled={isUploading}
        aria-label="Attach file"
        className={cn("message-composer-attach", className)}
      >
        {isUploading ? <Loader2 className="size-5 animate-spin" /> : <Paperclip className="size-5" />}
      </Button>
    </>
  );
}

/**
 * Presentational menu item for the composer options dropdown. The actual file
 * input lives at the composer root (see `FilePickerInput`), so selecting the
 * item only has to trigger the already-mounted input.
 */
export function AttachFileMenuItem({
  onSelect,
  uploading = false,
  disabled = false,
  className,
}: {
  onSelect: () => void;
  uploading?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <DropdownMenuItem
      disabled={disabled || uploading}
      onSelect={() => onSelect()}
      className={cn("gap-2", className)}
    >
      {uploading ? (
        <Loader2 className="size-4 animate-spin" />
      ) : (
        <Paperclip className="size-4" />
      )}
      {uploading ? "Uploading…" : "Attach file"}
    </DropdownMenuItem>
  );
}

/** Menu item that opens the image-only picker (gallery or camera on mobile). */
export function AttachImageMenuItem({
  onSelect,
  uploading = false,
  disabled = false,
  className,
}: {
  onSelect: () => void;
  uploading?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <DropdownMenuItem
      disabled={disabled || uploading}
      onSelect={() => onSelect()}
      className={cn("gap-2", className)}
    >
      {uploading ? (
        <Loader2 className="size-4 animate-spin" />
      ) : (
        <ImageIcon className="size-4" />
      )}
      {uploading ? "Uploading…" : "Attach image"}
    </DropdownMenuItem>
  );
}

export function UploadedFilesPreview({
  uploadedFiles,
  onFileRemoved,
}: {
  uploadedFiles: UploadedFileRecord[];
  onFileRemoved: (fileId: string) => void;
}) {
  if (uploadedFiles.length === 0) {
    return null;
  }

  return (
    <div className="message-composer-files">
      {uploadedFiles.map((file) => {
        const isImage = isImageMimeType(file.mimeType);
        return (
          <div
            key={file.fileId}
            className="flex max-w-full items-center gap-2 rounded-lg border border-border/60 bg-muted/50 px-2 py-1.5 text-sm text-foreground"
          >
            {isImage ? (
              <img
                src={`/api/v1/files/${file.fileId}`}
                alt={file.displayName || file.fileName}
                className="size-8 shrink-0 rounded object-cover"
              />
            ) : (
              getFileIcon(file.mimeType)
            )}
            <span className="truncate">{file.displayName || file.fileName}</span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => onFileRemoved(file.fileId)}
              className="size-6 shrink-0 text-muted-foreground hover:text-foreground"
              aria-label={`Remove ${file.displayName || file.fileName}`}
            >
              <X className="size-3.5" />
            </Button>
          </div>
        );
      })}
    </div>
  );
}

/**
 * Inline error surface for rejected files (too large, unsupported, folder, cap
 * reached, upload failure). `role="alert"` makes it announce itself, which is
 * the accessible counterpart of the drag affordance for keyboard and screen
 * reader users — and the only feedback a mobile user gets, since a WebView has
 * no native drag source.
 */
export function UploadErrorsNotice({
  errors,
  onDismiss,
}: {
  errors: UploadRejection[];
  onDismiss: () => void;
}) {
  if (errors.length === 0) return null;

  return (
    <div className="message-composer-errors" role="alert">
      <CircleAlert className="size-4 shrink-0 text-destructive" />
      <ul className="message-composer-error-list">
        {errors.map((error, index) => (
          <li key={`${error.reason}-${error.fileName}-${index}`} className="message-composer-error-item">
            {error.message}
          </li>
        ))}
      </ul>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        onClick={onDismiss}
        aria-label="Dismiss attachment errors"
        className="size-6 shrink-0 text-muted-foreground hover:text-foreground"
      >
        <X className="size-3.5" />
      </Button>
    </div>
  );
}

interface FileUploadProps extends FileUploadBaseProps {
  onFileRemoved: (fileId: string) => void;
  uploadedFiles: UploadedFileRecord[];
}

export const FileUpload: React.FC<FileUploadProps> = ({
  onFileUploaded,
  onFileRemoved,
  uploadedFiles,
  reuploadToOpenAI = false,
}) => {
  const { isUploading, fileInputRef, handleFileSelect, openFilePicker } = useFileUpload({
    onFileUploaded,
    reuploadToOpenAI,
  });

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <FilePickerInput
          inputRef={fileInputRef}
          accept={FILE_UPLOAD_ACCEPT}
          onFileSelect={handleFileSelect}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={openFilePicker}
          disabled={isUploading}
          className="flex items-center gap-2"
        >
          <Paperclip className="size-4" />
          {isUploading ? "Uploading…" : "Attach File"}
        </Button>
      </div>

      <UploadedFilesPreview uploadedFiles={uploadedFiles} onFileRemoved={onFileRemoved} />
    </div>
  );
};
