import React from 'react';
import { FileText, Image, File, Download, ExternalLink } from 'lucide-react';
import { Button } from "../button";

interface FileAttachmentProps {
  fileId: string;
  fileName: string;
  displayName?: string;
  mimeType?: string;
  size?: number;
}

export const FileAttachmentDisplay: React.FC<FileAttachmentProps> = ({
  fileId,
  fileName,
  displayName,
  mimeType,
  size
}) => {
  const getFileIcon = () => {
    if (mimeType?.startsWith('image/')) {
      return <Image className="h-5 w-5" />;
    }
    if (mimeType === 'application/pdf') {
      return <FileText className="h-5 w-5" />;
    }
    return <File className="h-5 w-5" />;
  };

  const formatFileSize = (bytes: number) => {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  const handleDownload = () => {
    window.open(`/api/v1/files/${fileId}`, '_blank');
  };

  const handlePreview = () => {
    if (mimeType?.startsWith('image/')) {
      // For images, open in new tab
      window.open(`/api/v1/files/${fileId}`, '_blank');
    } else {
      // For other files, download
      handleDownload();
    }
  };

  return (
    <div className="flex items-center gap-3 p-3 bg-muted rounded-lg border border-border">
      <div className="flex-shrink-0">
        {getFileIcon()}
      </div>
      
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-foreground truncate">
          {displayName || fileName}
        </div>
        {size && (
          <div className="text-xs text-muted-foreground">
            {formatFileSize(size)}
          </div>
        )}
      </div>
      
      <div className="flex-shrink-0 flex gap-1">
        {mimeType?.startsWith('image/') ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={handlePreview}
            className="h-8 w-8 p-0"
            title="Preview"
          >
            <ExternalLink className="h-4 w-4" />
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            onClick={handleDownload}
            className="h-8 w-8 p-0"
            title="Download"
          >
            <Download className="h-4 w-4" />
          </Button>
        )}
      </div>
    </div>
  );
};

// Component for displaying multiple file attachments
interface FileAttachmentsListProps {
  attachments: Array<{
    fileId: string;
    fileName: string;
    displayName?: string;
    mimeType?: string;
    size?: number;
  }>;
}

export const FileAttachmentsList: React.FC<FileAttachmentsListProps> = ({
  attachments
}) => {
  if (!attachments || attachments.length === 0) {
    return null;
  }

  const images = attachments.filter((attachment) => attachment.mimeType?.startsWith('image/'));
  const files = attachments.filter((attachment) => !attachment.mimeType?.startsWith('image/'));

  return (
    <div className="flex flex-col gap-2 mt-2">
      {images.length > 0 ? (
        <div className={images.length === 1 ? "flex" : "flex flex-wrap gap-2"}>
          {images.map((image, index) => (
            <a
              key={`${image.fileId}-${index}`}
              href={`/api/v1/files/${image.fileId}`}
              target="_blank"
              rel="noreferrer"
              className={images.length === 1 ? "block" : "block max-w-[12rem]"}
            >
              <img
                src={`/api/v1/files/${image.fileId}`}
                alt={image.displayName || image.fileName}
                loading="lazy"
                className={
                  images.length === 1
                    ? "max-w-full h-auto rounded-lg border"
                    : "h-32 w-full rounded-lg border object-cover"
                }
                style={images.length === 1 ? { maxHeight: '400px' } : undefined}
              />
            </a>
          ))}
        </div>
      ) : null}

      {files.map((attachment, index) => (
        <FileAttachmentDisplay
          key={`${attachment.fileId}-${index}`}
          fileId={attachment.fileId}
          fileName={attachment.fileName}
          displayName={attachment.displayName}
          mimeType={attachment.mimeType}
          size={attachment.size}
        />
      ))}
    </div>
  );
}; 