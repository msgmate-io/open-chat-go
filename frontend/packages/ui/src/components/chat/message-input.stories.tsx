import type { Meta, StoryObj } from "@storybook/react-vite";
import { useRef, useState } from "react";
import { MessageInput } from "./MessageInput";
import { MessageInputWithFiles } from "./MessageInputWithFiles";

const meta = {
  title: "Chat/MessageInput",
  component: MessageInput,
  parameters: { layout: "padded" },
} satisfies Meta<typeof MessageInput>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
  render: function Render() {
    const [text, setText] = useState("");
    const ref = useRef<HTMLTextAreaElement>(null);
    return (
      <div className="mx-auto w-full max-w-3xl">
        <MessageInput
          ref={ref}
          text={text}
          setText={setText}
          onSendMessage={() => setText("")}
          botConfig={{ reasoning: true, tools: ["search"] }}
        />
      </div>
    );
  },
};

export const WithText: Story = {
  render: function Render() {
    const [text, setText] = useState("Draft message for the team…");
    const ref = useRef<HTMLTextAreaElement>(null);
    return (
      <div className="mx-auto w-full max-w-3xl">
        <MessageInput ref={ref} text={text} setText={setText} />
      </div>
    );
  },
};

export const BotResponding: Story = {
  render: function Render() {
    const [text, setText] = useState("");
    const ref = useRef<HTMLTextAreaElement>(null);
    return (
      <div className="mx-auto w-full max-w-3xl">
        <MessageInput
          ref={ref}
          text={text}
          setText={setText}
          isBotResponding
          stopBotResponse={() => {}}
          botConfig={{ reasoning: true, tools: ["search"] }}
        />
      </div>
    );
  },
};

export const WithFiles: Story = {
  render: function Render() {
    const [text, setText] = useState("Review this deployment log");
    const ref = useRef<HTMLTextAreaElement>(null);
    return (
      <div className="mx-auto w-full max-w-3xl">
        <MessageInputWithFiles
          ref={ref}
          text={text}
          setText={setText}
          onSendMessage={() => setText("")}
          botConfig={{ backend: "openai", reasoning: true, tools: ["search"] }}
        />
      </div>
    );
  },
};
