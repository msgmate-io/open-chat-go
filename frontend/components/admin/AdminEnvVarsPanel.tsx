import { useMemo, useState } from "react";
import useSWR from "swr";
import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Text, TextTypes } from "@open-chat-go/ui";

type RuntimeConfigValue = {
  value: string;
  sensitive: boolean;
};

type RuntimeConfigResponse = {
  values: Record<string, RuntimeConfigValue>;
};

const fetcher = (...args: [RequestInfo, RequestInit?]) =>
  fetch(...args, { credentials: "include" }).then((res) => {
    if (!res.ok) throw new Error(`Request failed with status ${res.status}`);
    return res.json();
  });

export function AdminEnvVarsPanel() {
  const { data, isLoading, error, mutate } = useSWR<RuntimeConfigResponse>("/api/v1/admin/env-vars", fetcher);
  const [password, setPassword] = useState("");
  const [revealing, setRevealing] = useState(false);
  const [revealError, setRevealError] = useState("");
  const [revealSuccess, setRevealSuccess] = useState("");

  const keys = useMemo(() => Object.keys(data?.values ?? {}).sort(), [data?.values]);

  const revealValues = async () => {
    setRevealError("");
    setRevealSuccess("");
    if (!password.trim()) {
      setRevealError("Please enter your password to reveal sensitive values.");
      return;
    }
    setRevealing(true);
    try {
      const response = await fetch("/api/v1/admin/env-vars/reveal", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });
      if (!response.ok) {
        const text = await response.text().catch(() => "Failed to reveal environment variables.");
        throw new Error(text || "Failed to reveal environment variables.");
      }
      const payload = (await response.json()) as RuntimeConfigResponse;
      await mutate(payload, false);
      setRevealSuccess("Sensitive values revealed for this session view.");
      setPassword("");
    } catch (err) {
      setRevealError(err instanceof Error ? err.message : "Failed to reveal environment variables.");
    } finally {
      setRevealing(false);
    }
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Runtime Environment Variables</CardTitle>
          <CardDescription>
            Values are admin-only. Sensitive values remain hidden until you confirm with your password.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-col gap-2 md:flex-row md:items-end">
            <div className="flex-1 space-y-1">
              <Text type={TextTypes.Body6}>Confirm admin password</Text>
              <Input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="Enter password to reveal sensitive values"
              />
            </div>
            <Button type="button" onClick={revealValues} disabled={revealing}>
              {revealing ? "Revealing..." : "Reveal sensitive values"}
            </Button>
          </div>
          {revealError ? (
            <Text type={TextTypes.Body6} color="destructive">
              {revealError}
            </Text>
          ) : null}
          {revealSuccess ? (
            <Text type={TextTypes.Body6} color="muted">
              {revealSuccess}
            </Text>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Current values</CardTitle>
          <CardDescription>Shows currently loaded runtime config values from the server process.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          {isLoading ? <Text type={TextTypes.Body6}>Loading environment variables...</Text> : null}
          {error ? (
            <Text type={TextTypes.Body6} color="destructive">
              {error.message}
            </Text>
          ) : null}
          {!isLoading && !error && keys.length === 0 ? (
            <Text type={TextTypes.Body6} color="muted">
              No runtime variables available.
            </Text>
          ) : null}
          {keys.map((key) => {
            const value = data?.values?.[key];
            return (
              <div key={key} className="rounded-md border bg-muted/20 p-2">
                <div className="flex flex-wrap items-center gap-2">
                  <code className="text-xs font-medium">{key}</code>
                  <Badge variant={value?.sensitive ? "secondary" : "outline"}>
                    {value?.sensitive ? "sensitive" : "public"}
                  </Badge>
                </div>
                <Text type={TextTypes.Body7} color="muted" className="mt-1 break-all">
                  {value?.value || "(not set)"}
                </Text>
              </div>
            );
          })}
        </CardContent>
      </Card>
    </div>
  );
}
