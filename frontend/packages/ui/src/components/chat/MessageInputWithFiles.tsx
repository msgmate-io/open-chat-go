import { forwardRef, useState, type ReactNode } from "react";
import { Search } from "lucide-react";
import { AttachFileMenuItem, UploadedFilesPreview } from "./FileUpload";
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
      ...props
    },
    ref
  ) {
    const [uploadedFiles, setUploadedFiles] = useState<
      Array<{ fileId: string; fileName: string; displayName?: string }>
    >([]);

    const handleFileUploaded = (fileId: string, fileName: string) => {
      setUploadedFiles((prev) => [...prev, { fileId, fileName }]);
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
      <MessageComposer
        ref={ref}
        {...props}
        setText={setText}
        botConfig={botConfig}
        onSendMessage={handleSendMessage}
        hasAttachments={uploadedFiles.length > 0}
        placeholder={props.placeholder ?? "Send message to Msgmate.io"}
        attachmentsPreview={
          <UploadedFilesPreview uploadedFiles={uploadedFiles} onFileRemoved={handleFileRemoved} />
        }
        footerOptions={
          <ComposerOptionsMenu>
            <AttachFileMenuItem
              onFileUploaded={handleFileUploaded}
              reuploadToOpenAI={botConfig?.backend === "openai"}
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
    );
  }
);
