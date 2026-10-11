import type { Meta, StoryObj } from "@storybook/react-vite"
import { expect } from "storybook/test"
import { useState } from "react"
import { ChatSettings } from "./ChatSettings"
import { Button } from "../button"
import { mockChat } from "./story-data"

// Small harness that owns the controlled open state and a minimal trigger, so
// the story exercises the same render-prop contract ChatItem uses.
function ChatSettingsHarness({ compact }: { compact?: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <ChatSettings chat={mockChat} open={open} setOpen={setOpen} compact={compact}>
      {({ Trigger }) => (
        <Trigger asChild>
          <Button variant="ghost" size="icon" aria-label="Chat settings">
            ⋯
          </Button>
        </Trigger>
      )}
    </ChatSettings>
  )
}

const meta = {
  title: "Chat/ChatSettings",
  component: ChatSettings,
  parameters: { layout: "padded" },
  // Satisfy the required props; each story renders its own controlled harness.
  args: {
    chat: mockChat,
    open: false,
    setOpen: () => {},
    children: () => null,
  },
} satisfies Meta<typeof ChatSettings>

export default meta
type Story = StoryObj<typeof meta>

// On very small / short screens the edit-title surface collapses to the icon
// trigger opening a modal instead of the wide dropdown.
export const CompactOpensModal: Story = {
  render: () => <ChatSettingsHarness compact />,
  play: async ({ canvas, userEvent, canvasElement }) => {
    await userEvent.click(canvas.getByRole("button", { name: /chat settings/i }))
    const body = canvasElement.ownerDocument.body
    await expect(body.querySelector('[data-testid="chat-settings-modal"]')).toBeTruthy()
    await expect(body.querySelector('[data-testid="chat-settings-dropdown"]')).toBeNull()
    await expect(body.textContent).toMatch(/chat settings/i)
    await expect(body.textContent).toMatch(/extra name/i)
  },
}

// On regular screens the surface stays a dropdown.
export const DesktopOpensDropdown: Story = {
  render: () => <ChatSettingsHarness compact={false} />,
  play: async ({ canvas, userEvent, canvasElement }) => {
    await userEvent.click(canvas.getByRole("button", { name: /chat settings/i }))
    const body = canvasElement.ownerDocument.body
    await expect(body.querySelector('[data-testid="chat-settings-dropdown"]')).toBeTruthy()
    await expect(body.querySelector('[data-testid="chat-settings-modal"]')).toBeNull()
    await expect(body.textContent).toMatch(/chat settings/i)
  },
}
