export type MobileShareAttachment = {
  fileId: string;
  fileName: string;
  displayName?: string;
  mimeType?: string;
  size?: number;
  previewUrl?: string;
};

export type PendingMobileShare = {
  attachments: MobileShareAttachment[];
  note: string;
};

let pendingShare: PendingMobileShare | null = null;

export function setPendingMobileShare(payload: PendingMobileShare): void {
  pendingShare = {
    attachments: Array.isArray(payload.attachments) ? payload.attachments : [],
    note: String(payload.note || ""),
  };
}

export function getPendingMobileShare(): PendingMobileShare | null {
  return pendingShare;
}

export function clearPendingMobileShare(): void {
  pendingShare = null;
}
