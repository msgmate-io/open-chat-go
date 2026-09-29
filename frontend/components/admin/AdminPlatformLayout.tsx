import type { ReactNode } from "react";
import PlatformLayout from "@/components/PlatformLayout";
import { AdminAccessGate } from "@open-chat-go/ui";

export function AdminPlatformLayout({
  descriptor,
  parent,
  children,
}: {
  descriptor: string;
  parent?: { title: string; href: string };
  children: ReactNode;
}) {
  return (
    <PlatformLayout
      descriptor={descriptor}
      basePath="/integrations/admin"
      baseTitle="Admin"
      parent={parent}
    >
      <AdminAccessGate>{children}</AdminAccessGate>
    </PlatformLayout>
  );
}
