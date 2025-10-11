import PlatformLayout from "@/components/PlatformLayout"
import { ModelsCatalogView } from "@open-chat-go/ui"

export default function ModelsPage() {
  return (
    <PlatformLayout descriptor="Models" basePath="/models" baseTitle="Models">
      <ModelsCatalogView mode="private" />
    </PlatformLayout>
  )
}
