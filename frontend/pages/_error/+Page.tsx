import React from "react";
import { usePageContext } from "vike-react/usePageContext";
import { Text, TextTypes } from "@open-chat-go/ui";

export default function Page() {
  const { is404 } = usePageContext();
  if (is404) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-2 p-8">
        <Text type={TextTypes.Heading4} tag="h1" bold>
          404 Page Not Found
        </Text>
        <Text type={TextTypes.Body5} color="muted">
          This page could not be found.
        </Text>
      </div>
    );
  }
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-2 p-8">
      <Text type={TextTypes.Heading4} tag="h1" bold>
        500 Internal Server Error
      </Text>
      <Text type={TextTypes.Body5} color="muted">
        Something went wrong.
      </Text>
    </div>
  );
}
