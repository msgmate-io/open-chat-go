import React, { useRef, useState } from "react";
import { Button } from "../button";
import { DropdownMenuItem } from "../dropdown-menu";
import { File, FileText, Image, Loader2, Paperclip, X } from "lucide-react";
import { cn } from "../../lib/utils";

interface UploadedFileRecord {
  fileId: string;
  fileName: string;
  displayName?: string;
}

interface FileUploadBaseProps {
  onFileUploaded: (fileId: string, fileName: string) => void;
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

export function useFileUpload({ onFileUploaded, reuploadToOpenAI = false }: FileUploadBaseProps) {  const [isUploading, setIsUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleFileSelect = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = event.target.files;
    if (!files || files.length === 0) return;

    const file = files[0];

    if (file.size > 5 * 1024 * 1024) {
      alert("File too large. Maximum size is 5MB.");
      return;
    }

    const allowedTypes = [
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

    if (!allowedTypes.includes(file.type)) {
      alert("File type not allowed.");
      return;
    }

    setIsUploading(true);

    try {
      const formData = new FormData();
      formData.append("file", file);

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

      const uploadedFile: UploadedFileResponse = await response.json();
      onFileUploaded(uploadedFile.file_id, uploadedFile.file_name);
    } catch (error) {
      console.error("File upload error:", error);
      alert("Failed to upload file. Please try again.");
    } finally {
      setIsUploading(false);
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  };

  return {
    isUploading,
    fileInputRef,
    handleFileSelect,
    openFilePicker: () => fileInputRef.current?.click(),
  };
}

function getFileIcon(mimeType: string) {
  if (mimeType.startsWith("image/")) {
    return <Image className="size-4 shrink-0" />;
  }
  if (mimeType === "application/pdf") {
    return <FileText className="size-4 shrink-0" />;
  }
  return <File className="size-4 shrink-0" />;
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
      <input
        ref={fileInputRef}
        type="file"
        onChange={handleFileSelect}
        className="hidden"
        accept=".jpg,.jpeg,.png,.gif,.webp,.pdf,.txt,.doc,.docx,.xls,.xlsx"
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

export function AttachFileMenuItem({
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
      <input
        ref={fileInputRef}
        type="file"
        onChange={handleFileSelect}
        className="hidden"
        accept=".jpg,.jpeg,.png,.gif,.webp,.pdf,.txt,.doc,.docx,.xls,.xlsx"
      />
      <DropdownMenuItem
        disabled={isUploading}
        onSelect={() => openFilePicker()}
        className={cn("gap-2", className)}
      >
        {isUploading ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <Paperclip className="size-4" />
        )}
        {isUploading ? "Uploading…" : "Attach file"}
      </DropdownMenuItem>
    </>
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
      {uploadedFiles.map((file) => (
        <div
          key={file.fileId}
          className="flex max-w-full items-center gap-2 rounded-lg border border-border/60 bg-muted/50 px-3 py-1.5 text-sm text-foreground"
        >
          {getFileIcon("application/octet-stream")}
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
      ))}
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
        <input
          ref={fileInputRef}
          type="file"
          onChange={handleFileSelect}
          className="hidden"
          accept=".jpg,.jpeg,.png,.gif,.webp,.pdf,.txt,.doc,.docx,.xls,.xlsx"
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
