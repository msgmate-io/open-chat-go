import { useEffect } from "react";

function resolveBackendOrigin(): string {
  const saved = window.localStorage.getItem("mcp_auth_backend_origin") || "";
  if (saved.trim()) {
    return saved.trim();
  }
  if (window.location.hostname === "localhost") {
    return "http://localhost:1984";
  }
  return window.location.origin;
}

export default function Page() {
  useEffect(() => {
    const backendOrigin = resolveBackendOrigin();
    const params = new URLSearchParams(window.location.search);
    const code = (params.get("code") || "").trim();
    const state = (params.get("state") || "").trim();
    const error = (params.get("error") || "").trim();
    const target = new URL("/api/v1/integrations/mcp/auth/callback", backendOrigin);

    if (code) {
      target.searchParams.set("code", code);
    }
    if (state) {
      target.searchParams.set("state", state);
    }
    if (error) {
      target.searchParams.set("error", error);
    }

    window.localStorage.removeItem("mcp_auth_backend_origin");
    window.location.replace(target.toString());
  }, []);

  return (
    <div className="min-h-screen flex items-center justify-center text-sm text-gray-700">
      Finishing sign-in...
    </div>
  );
}
