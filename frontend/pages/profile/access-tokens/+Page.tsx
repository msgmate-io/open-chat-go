import { useMemo, useState } from "react";
import useSWR, { mutate } from "swr";
import PlatformLayout from "@/components/PlatformLayout";
import { fetcher } from "@/lib/utils";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Text,
  TextTypes,
} from "@open-chat-go/ui";

type PermissionsResponse = { rows: string[] };
type AccessTokenListItem = {
  uuid: string;
  name: string;
  token_prefix: string;
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
};
type TokensResponse = {
  page: number;
  limit: number;
  total_pages: number;
  rows: AccessTokenListItem[];
};

export default function AccessTokensPage() {
  const [page, setPage] = useState(1);
  const [name, setName] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [newToken, setNewToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const { data: permissions } = useSWR<PermissionsResponse>("/api/v1/user/permissions", fetcher);
  const canCreateTokens = !!permissions?.rows?.includes("create_api_tokens");

  const listUrl = canCreateTokens ? `/api/v1/user/access-tokens/list?page=${page}&limit=10` : null;
  const { data: tokens, isLoading } = useSWR<TokensResponse>(listUrl, fetcher);

  const activeCount = useMemo(
    () => (tokens?.rows || []).filter((row) => !row.revoked_at && (!row.expires_at || new Date(row.expires_at) > new Date())).length,
    [tokens?.rows]
  );

  const handleCreateToken = async () => {
    if (!name.trim()) return;
    setCreating(true);
    setCreateError("");
    setNewToken(null);
    try {
      const response = await fetch("/api/v1/user/access-tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name.trim(), expires_at: expiresAt || undefined }),
      });
      if (!response.ok) {
        setCreateError(await response.text());
        return;
      }
      const payload = await response.json();
      setNewToken(payload?.token || null);
      setName("");
      setExpiresAt("");
      mutate(listUrl || "");
    } catch (error) {
      setCreateError(error instanceof Error ? error.message : "Failed to create token");
    } finally {
      setCreating(false);
    }
  };

  return (
    <PlatformLayout descriptor="API Tokens" basePath="/profile" baseTitle="Profile">
      <div className="space-y-4">
        <div>
          <Text type={TextTypes.Heading5} tag="h1" bold>
            API Access Tokens
          </Text>
          <Text type={TextTypes.Body5} color="muted">
            Create and manage bearer tokens for API clients.
          </Text>
        </div>

        {!canCreateTokens ? (
          <Card>
            <CardHeader>
              <CardTitle>Permission required</CardTitle>
              <CardDescription>You need the `create_api_tokens` permission to manage access tokens.</CardDescription>
            </CardHeader>
          </Card>
        ) : (
          <>
            <Card>
              <CardHeader>
                <CardTitle>Create token</CardTitle>
                <CardDescription>
                  Regular users can have up to 5 active tokens. Current page active count: {activeCount}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-3">
                <input
                  className="w-full rounded-md border bg-background p-2 text-sm"
                  placeholder="Token name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                />
                <input
                  className="w-full rounded-md border bg-background p-2 text-sm"
                  placeholder="Expires at (RFC3339, optional)"
                  value={expiresAt}
                  onChange={(event) => setExpiresAt(event.target.value)}
                />
                {createError ? (
                  <Text type={TextTypes.Body7} color="destructive">
                    {createError}
                  </Text>
                ) : null}
                <Button onClick={handleCreateToken} disabled={creating || !name.trim()}>
                  {creating ? "Creating..." : "Create token"}
                </Button>
                {newToken ? (
                  <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-sm">
                    <Text type={TextTypes.Body6} bold>
                      Copy this token now (it is shown only once):
                    </Text>
                    <code className="mt-1 block break-all text-xs">{newToken}</code>
                    <Button
                      variant="outline"
                      size="sm"
                      className="mt-2"
                      onClick={async () => {
                        try {
                          if (!navigator.clipboard?.writeText) {
                            throw new Error("Clipboard unavailable");
                          }
                          await navigator.clipboard.writeText(newToken);
                          setCopied(true);
                          setTimeout(() => setCopied(false), 1500);
                        } catch {
                          setCopied(false);
                        }
                      }}
                    >
                      {copied ? "Copied" : "Copy token"}
                    </Button>
                  </div>
                ) : null}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Existing tokens</CardTitle>
                <CardDescription>Use bearer auth header: `Authorization: Bearer &lt;token&gt;`</CardDescription>
              </CardHeader>
              <CardContent className="space-y-2">
                {isLoading ? <Text type={TextTypes.Body6}>Loading...</Text> : null}
                {(tokens?.rows || []).map((row) => (
                  <div key={row.uuid} className="rounded-md border p-3">
                    <div className="flex items-center justify-between gap-2">
                      <Text type={TextTypes.Body6} bold>{row.name}</Text>
                      <Badge variant={row.revoked_at ? "outline" : "secondary"}>{row.revoked_at ? "revoked" : "active"}</Badge>
                    </div>
                    <Text type={TextTypes.Body7} color="muted">Prefix: {row.token_prefix}</Text>
                  </div>
                ))}
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))}>Prev</Button>
                  <Text type={TextTypes.Body7}>Page {tokens?.page || page} / {tokens?.total_pages || 1}</Text>
                  <Button variant="outline" size="sm" onClick={() => setPage((p) => p + 1)} disabled={(tokens?.total_pages || 1) <= page}>Next</Button>
                </div>
              </CardContent>
            </Card>
          </>
        )}
      </div>
    </PlatformLayout>
  );
}
