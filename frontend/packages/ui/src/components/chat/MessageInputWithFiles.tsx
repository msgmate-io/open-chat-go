import { forwardRef, useCallback, useState, type ClipboardEvent, type ReactNode } from "react";
import { Search } from "lucide-react";
import {
  AttachFileMenuItem,
  AttachImageMenuItem,
  FileDropOverlay,
  FilePickerInput,
  IMAGE_UPLOAD_ACCEPT,
  FILE_UPLOAD_ACCEPT,
  UploadErrorsNotice,
  UploadedFilesPreview,
  uploadRejectionMessage,
  useFileDropzone,
  useFileUpload,
  type UploadRejection,
  type UploadedFileRecord,
} from "./FileUpload";
import { ComposerOptionsMenu } from "./ComposerOptionsMenu";
import { MessageComposer, type MessageComposerProps } from "./message-composer";
import type { BotReplyDeliveryMode } from "../../lib/delivery-mode";

export type MessageInputWithFilesProps = Omit<
  MessageComposerProps,
  "attachmentsPreview" | "hasAttachments" | "onSendMessage" | "footerOptions" | "onSendNow"
> & {
  onSendMessage?: (
    attachments?: Array<{ file_id: string; display_name?: string }>,
    deliveryMode?: BotReplyDeliveryMode
  ) => void;
  /** Extra items rendered inside the composer options menu (attach file is always included). */
  footerMenuItems?: ReactNode;
};

export const MessageInputWithFiles = forwardRef<HTMLTextAreaElement, MessageInputWithFilesProps>(
  function MessageInputWithFiles(
    {
      onSendMessage = () => {},
      botConfig = null,
      setText,
      footerMenuItems,
      onPaste,
      ...props
    },
    ref
  ) {
    const [uploadedFiles, setUploadedFiles] = useState<UploadedFileRecord[]>([]);
    const [uploadErrors, setUploadErrors] = useState<UploadRejection[]>([]);

    const handleFileUploaded = useCallback((file: UploadedFileRecord) => {
      setUploadedFiles((prev) => [...prev, file]);
    }, []);

    // Rejections (too large, unsupported type, folder, cap reached, upload
    // failure) are rendered inline instead of via `alert`, so a dropped batch
    // reports per-file while the files that *are* fine still attach.
    const handleRejected = useCallback((rejections: UploadRejection[]) => {
      setUploadErrors((prev) => [...prev, ...rejections]);
    }, []);

    const reuploadToOpenAI = botConfig?.backend === "openai";

    // Two independent pickers: "Attach file" accepts every supported type and
    // "Attach image" is restricted to images so mobile can surface the gallery
    // and camera directly. Both are hidden inputs rendered at the composer root
    // (not inside the dropdown portal) so the native host's file-chooser
    // callback still finds a mounted input when the menu closes.
    // `attachmentCount` keeps the per-message cap honest across both pickers
    // and the dropzone.
    const uploadOptions = {
      onFileUploaded: handleFileUploaded,
      reuploadToOpenAI,
      attachmentCount: uploadedFiles.length,
      onRejected: handleRejected,
    };
    const fileUpload = useFileUpload(uploadOptions);
    const imageUpload = useFileUpload(uploadOptions);

    // Web drag-and-drop: dropping files anywhere on the composer uploads them
    // through the same multi-file path as the pickers. Dropped folders are
    // rejected with the same inline notice (a folder is never uploadable).
    // Deliberately *not* disabled while an upload is in flight: an ignored drop
    // is indistinguishable from a broken one, and `useFileUpload` already counts
    // batches, so a second drop during an upload simply queues behind the first.
    const dropzone = useFileDropzone({
      onFilesDropped: fileUpload.uploadFiles,
      onFoldersDropped: (folderNames) =>
        handleRejected(
          folderNames.map((folderName) => ({
            fileName: folderName,
            reason: "folder" as const,
            message: uploadRejectionMessage("folder", folderName),
          }))
        ),
      // Attachments can be added while an upload runs — the drag affordance
      // simply keeps announcing where a drop would land.
    });

    const handlePaste = async (event: ClipboardEvent<HTMLTextAreaElement>) => {
      onPaste?.(event);

      const items = event.clipboardData?.items;
      if (!items || items.length === 0) return;

      const files: File[] = [];
      for (const item of Array.from(items)) {
        if (item.kind !== "file") continue;
        const file = item.getAsFile();
        if (file) files.push(file);
      }

      if (files.length === 0) return;

      event.preventDefault();
      await fileUpload.uploadFiles(files);
    };

    const handleFileRemoved = (fileId: string) => {
      setUploadedFiles((prev) => prev.filter((file) => file.fileId !== fileId));
    };

    // Both send paths clear the composer identically; kept in one place so
    // "Send now" can never leave text or attachments behind.
    const clearComposer = useCallback(() => {
      setText("");
      setUploadedFiles([]);
      setUploadErrors([]);
    }, [setText]);

    const handleSendMessage = () => {
      const attachments = uploadedFiles.map((file) => ({
        file_id: file.fileId,
        display_name: file.displayName,
      }));

      onSendMessage(attachments.length > 0 ? attachments : undefined);
      clearComposer();
    };

    // ci#656: "Send now" keeps the same attachment handling but asks the server
    // to interrupt the running reply instead of queueing behind it.
    const handleSendNow = () => {
      const attachments = uploadedFiles.map((file) => ({
        file_id: file.fileId,
        display_name: file.displayName,
      }));

      onSendMessage(attachments.length > 0 ? attachments : undefined, "interrupt");
      clearComposer();
    };

    const toolsEnabled = Boolean(botConfig?.tools && botConfig.tools.length > 0);

    return (
      <>
        <FilePickerInput
          inputRef={fileUpload.fileInputRef}
          accept={FILE_UPLOAD_ACCEPT}
          onFileSelect={fileUpload.handleFileSelect}
        />
        <FilePickerInput
          inputRef={imageUpload.fileInputRef}
          accept={IMAGE_UPLOAD_ACCEPT}
          onFileSelect={imageUpload.handleFileSelect}
        />
        <div className="relative w-full shrink-0" {...dropzone.dropzoneProps}>
          <MessageComposer
            ref={ref}
            {...props}
            setText={setText}
            botConfig={botConfig}
            onSendMessage={handleSendMessage}
            onSendNow={handleSendNow}
            onPaste={handlePaste}
            hasAttachments={uploadedFiles.length > 0}
            placeholder={props.placeholder ?? "Send message to Msgmate.io"}
            attachmentsPreview={
              <>
                <UploadErrorsNotice
                  errors={uploadErrors}
                  onDismiss={() => setUploadErrors([])}
                />
                <UploadedFilesPreview uploadedFiles={uploadedFiles} onFileRemoved={handleFileRemoved} />
              </>
            }
            footerOptions={
              <ComposerOptionsMenu>
                <AttachFileMenuItem
                  onSelect={fileUpload.openFilePicker}
                  uploading={fileUpload.isUploading}
                />
                <AttachImageMenuItem
                  onSelect={imageUpload.openFilePicker}
                  uploading={imageUpload.isUploading}
                />
                {toolsEnabled ? (
                  <div className="flex items-center gap-2 px-2 py-1 text-xs text-muted-foreground">
                    <Search className="size-3.5" />
                    Tools enabled
                  </div>
                ) : null}
                {footerMenuItems}
              </ComposerOptionsMenu>
            }
          />
          <FileDropOverlay visible={dropzone.isDragging} />
        </div>
      </>
    );
  }
);
