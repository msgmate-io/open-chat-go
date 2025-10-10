import { useEffect, useMemo, useState } from "react";
import { navigate } from "vike/client/router";
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Text,
  TextTypes,
} from "@open-chat-go/ui";
import {
  applyPendingMobileJsonImportAsDefault,
  clearPendingMobileJsonImports,
  isMobileAppRuntime,
  readPendingMobileJsonImports,
  registerPendingMobileJsonImportAsServer,
  type PendingMobileJsonImport,
} from "@open-chat-go/ui";

export default function Page() {
  const [items, setItems] = useState<PendingMobileJsonImport[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [status, setStatus] = useState("");
  const [previewOpen, setPreviewOpen] = useState(false);

  const refresh = () => {
    const result = readPendingMobileJsonImports();
    if (!result.ok) {
      setStatus(result.message || "No pending imports");
      setItems([]);
      return;
    }

    setItems(result.items);
    setSelectedIndex((prev) => {
      if (result.items.length === 0) {
        return 0;
      }
      return Math.min(prev, result.items.length - 1);
    });
    setStatus(result.items.length === 0 ? "No pending JSON imports" : "");
  };

  useEffect(() => {
    if (!isMobileAppRuntime()) {
      navigate("/mobile/config");
      return;
    }
    refresh();
  }, []);

  const selected = useMemo(() => {
    return items[selectedIndex] || items[0] || null;
  }, [items, selectedIndex]);

  const cancel = () => {
    const result = clearPendingMobileJsonImports();
    if (!result.ok) {
      setStatus(result.message || "Failed to dismiss import");
      return;
    }
    navigate("/login");
  };

  const setAsDefault = () => {
    if (!selected) {
      return;
    }
    const result = applyPendingMobileJsonImportAsDefault(selected.index);
    setStatus(result.message || (result.ok ? "Default server config updated" : "Failed to set default config"));
    if (result.ok) {
      refresh();
      navigate("/mobile/config");
    }
  };

  const registerAsServer = () => {
    if (!selected) {
      return;
    }
    const result = registerPendingMobileJsonImportAsServer(selected.index);
    setStatus(result.message || (result.ok ? "Server registered" : "Failed to register server"));
    if (result.ok) {
      refresh();
      navigate("/mobile/config");
    }
  };

  return (
    <div className="min-h-full bg-background text-foreground">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 px-4 py-6 sm:py-10">
        <div className="rounded-3xl border border-border bg-card p-5 shadow-sm sm:p-6">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Import JSON config
          </Text>
          <Text type={TextTypes.Body6} color="muted" className="mt-1">
            Review selected file names, inspect JSON, and apply to default config or register a new server.
          </Text>

          {items.length > 0 ? (
            <div className="mt-4 space-y-3">
              <div className="space-y-2">
                {items.map((item, index) => {
                  const isActive = selected?.index === item.index;
                  return (
                    <button
                      key={`${item.fileName}-${item.index}`}
                      type="button"
                      onClick={() => setSelectedIndex(index)}
                      className={[
                        "w-full rounded-xl border px-3 py-2 text-left",
                        isActive ? "border-primary bg-primary/10" : "border-border bg-muted/20",
                      ].join(" ")}
                    >
                      <Text type={TextTypes.Body6} bold>
                        {item.fileName}
                      </Text>
                      <Text type={TextTypes.Body7} color="muted" className="mt-0.5">
                        {item.hostUrl ? `Host: ${item.hostUrl}` : "Host not resolved"}
                      </Text>
                    </button>
                  );
                })}
              </div>

              {selected && (
                <>
                  <div className="rounded-xl border border-border bg-muted/20 p-3">
                    <Text type={TextTypes.Body7} color="muted">
                      {selected.parseError ? `Validation error: ${selected.parseError}` : "Validation: OK"}
                    </Text>
                    {!selected.canSetAsDefault && (
                      <Text type={TextTypes.Body7} color="muted" className="mt-1">
                        Default server config is only available for the local backend target (for example `http://localhost:1984` or `HOST=127.0.0.1` with `PORT=1984`).
                      </Text>
                    )}
                  </div>

                  <Button type="button" variant="outline" className="w-full" onClick={() => setPreviewOpen(true)}>
                    View JSON contents
                  </Button>

                  <div className="border-t border-border pt-3">
                    <Button
                      type="button"
                      className="w-full"
                      disabled={!selected.canApply || !selected.canSetAsDefault}
                      onClick={setAsDefault}
                    >
                      Set as default server config
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      className="mt-2 w-full"
                      disabled={!selected.canApply}
                      onClick={registerAsServer}
                    >
                      Register as new server
                    </Button>
                    <Button type="button" variant="ghost" className="mt-2 w-full" onClick={cancel}>
                      Not now
                    </Button>
                  </div>
                </>
              )}
            </div>
          ) : (
            <div className="mt-4 rounded-xl border border-border bg-muted/20 p-4">
              <Text type={TextTypes.Body6} color="muted">
                No pending JSON imports.
              </Text>
              <Button type="button" variant="outline" className="mt-3" onClick={() => navigate("/mobile/config")}>
                Back to mobile config
              </Button>
            </div>
          )}

          {status && (
            <Text type={TextTypes.Body7} color="muted" className="mt-3">
              {status}
            </Text>
          )}
        </div>
      </div>

      <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
        <DialogContent className="h-[95vh] w-[98vw] max-w-[98vw] overflow-hidden p-0">
          <div className="flex h-full min-h-0 flex-col">
            <DialogHeader className="border-b border-border px-5 pb-3 pt-5">
              <DialogTitle>{selected?.fileName || "JSON preview"}</DialogTitle>
              <DialogDescription>
                Fullscreen JSON preview{selected?.wasPreviewTruncated ? " (truncated for performance)." : "."}
              </DialogDescription>
            </DialogHeader>
            <div className="min-h-0 flex-1 px-5 pb-4 pt-3">
              <textarea
                readOnly
                className="h-full w-full rounded-md border border-input bg-background p-3 font-mono text-xs"
                value={selected?.previewContent || ""}
              />
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
