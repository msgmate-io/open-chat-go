import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Button } from "./button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "./dialog";

const meta = {
  component: Dialog,
} satisfies Meta<typeof Dialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline">Open dialog</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete chat?</DialogTitle>
          <DialogDescription>
            This action cannot be undone. The chat history will be removed.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline">Cancel</Button>
          <Button variant="destructive">Delete</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  ),
  play: async ({ canvas, userEvent, canvasElement }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: /open dialog/i })
    );
    const body = canvasElement.ownerDocument.body;
    await expect(
      body.querySelector('[data-slot="dialog-content"]')
    ).toBeTruthy();
    await expect(body.textContent).toMatch(/delete chat/i);
  },
};
