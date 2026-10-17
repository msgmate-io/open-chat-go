import { forwardRef, useState, type ClipboardEvent, type ReactNode } from "react";
import { Search } from "lucide-react";
import {
  AttachFileMenuItem,
  AttachImageMenuItem,
  FileDropOverlay,
  FilePickerInput,
  IMAGE_UPLOAD_ACCEPT,
  FILE_UPLOAD_ACCEPT,
  UploadedFilesPreview,
  useFileDropzone,
  useFileUpload,
  type UploadedFileRecord,
} from "./FileUpload";
import { ComposerOptionsMenu } from "./ComposerOptionsMenu";
import { MessageComposer, type MessageComposerProps } from "./message-composer";

export type MessageInputWithFilesProps = Omit<
  MessageComposerProps,
  "attachmentsPreview" | "hasAttachments" | "onSendMessage" | "footerOptions"
> & {
  onSendMessage?: (attachments?: Array<{ file_id: string; display_name?: string }>) => void;
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

    const handleFileUploaded = (file: UploadedFileRecord) => {
      setUploadedFiles((prev) => [...prev, file]);
    };

    const reuploadToOpenAI = botConfig?.backend === "openai";

    // Two independent pickers: "Attach file" accepts every supported type and
    // "Attach image" is restricted to images so mobile can surface the gallery
    // and camera directly. Both are hidden inputs rendered at the composer root
    // (not inside the dropdown portal) so the native host's file-chooser
    // callback still finds a mounted input when the menu closes.
    const fileUpload = useFileUpload({ onFileUploaded: handleFileUploaded, reuploadToOpenAI });
    const imageUpload = useFileUpload({ onFileUploaded: handleFileUploaded, reuploadToOpenAI });

    // Web drag-and-drop: dropping files anywhere on the composer uploads them
    // through the same multi-file path as the pickers. Disabled while an upload
    // is already in flight to avoid stacking drop targets.
    const dropzone = useFileDropzone({
      onFilesDropped: fileUpload.uploadFiles,
      disabled: fileUpload.isUploading,
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

    const handleSendMessage = () => {
      const attachments = uploadedFiles.map((file) => ({
        file_id: file.fileId,
        display_name: file.displayName,
      }));

      onSendMessage(attachments.length > 0 ? attachments : undefined);
      setText("");
      setUploadedFiles([]);
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
            onPaste={handlePaste}
            hasAttachments={uploadedFiles.length > 0}
            placeholder={props.placeholder ?? "Send message to Msgmate.io"}
            attachmentsPreview={
              <UploadedFilesPreview uploadedFiles={uploadedFiles} onFileRemoved={handleFileRemoved} />
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
