import React from "react";
import { Text, TextTypes } from "@open-chat-go/ui";

export default function TestPage() {
  return (
    <div className="space-y-3 p-5">
      <Text type={TextTypes.Heading4} tag="h1" bold>
        Theme Test
      </Text>
      <Text type={TextTypes.Body5} tag="p" className="rounded-md bg-primary p-3 text-primary-foreground">
        Primary
      </Text>
      <Text type={TextTypes.Body5} tag="p" className="rounded-md bg-secondary p-3 text-secondary-foreground">
        Secondary
      </Text>
      <Text type={TextTypes.Body5} tag="p" className="rounded-md bg-accent p-3 text-accent-foreground">
        Accent
      </Text>
      <Text type={TextTypes.Body5} tag="p" color="muted" className="rounded-md bg-muted p-3">
        Muted
      </Text>
    </div>
  );
}
