import PlatformLayout from "@/components/PlatformLayout"
import { ToolsCatalogView } from "@/components/tools/ToolsCatalogView"
import { useEffect, useState } from "react"
import { Cookies } from "typescript-cookie"

export default function ToolsPage() {
  const [mode, setMode] = useState<"private" | "public">("public")

  useEffect(() => {
    setMode(Cookies.get("oc_client_state") === "true" ? "private" : "public")
  }, [])

  return (
    <PlatformLayout descriptor="Tools" basePath="/tools" baseTitle="Tools" allowPublicAccess>
      <ToolsCatalogView mode={mode} />
    </PlatformLayout>
  )
}
