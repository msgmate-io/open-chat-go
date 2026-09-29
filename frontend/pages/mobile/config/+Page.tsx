import { Button, Text, TextTypes } from "@open-chat-go/ui";
import { useState } from "react";
import { navigate } from "vike/client/router";
import { MobileServerSelector } from "@/components/mobile/MobileServerSelector";

export default function Page() {
  const [status, setStatus] = useState("");

  return (
    <div className="min-h-full bg-secondary text-secondary-foreground">
      <div className="mx-auto flex w-full max-w-3xl flex-col gap-4 px-4 py-8 sm:py-12">
        <div className="rounded-3xl border border-secondary-foreground/20 bg-secondary/70 p-6 shadow-xl backdrop-blur">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Server settings
          </Text>
          <Text type={TextTypes.Body5} color="muted" className="mt-2">
            Manage mobile server profiles and switch active runtime target before login.
          </Text>

          <div className="mt-4">
            <MobileServerSelector onStatus={setStatus} />
          </div>

          <div className="mt-4 flex gap-2">
            <Button type="button" variant="outline" onClick={() => navigate("/login")}>
              Back to login
            </Button>
          </div>

          {status && (
            <Text type={TextTypes.Body6} color="muted" className="mt-3">
              {status}
            </Text>
          )}
        </div>
      </div>
    </div>
  );
}
