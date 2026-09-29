import { forwardRef } from "react";
import { MessageComposer, type MessageComposerProps } from "./message-composer";

export { CancelResponseButton } from "./message-composer";

export type MessageInputProps = MessageComposerProps;

export const MessageInput = forwardRef<HTMLTextAreaElement, MessageInputProps>(
  function MessageInput(props, ref) {
    return (
      <MessageComposer
        ref={ref}
        {...props}
        placeholder={props.placeholder ?? "Send message to Msgmate.io"}
      />
    );
  }
);
