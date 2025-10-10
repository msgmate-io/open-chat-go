import type { Meta, StoryObj } from "@storybook/react-vite";
import { FileAttachmentDisplay, FileAttachmentsList } from "./FileAttachment";

const meta = {
  title: "Chat/FileAttachment",
  component: FileAttachmentDisplay,
} satisfies Meta<typeof FileAttachmentDisplay>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Pdf: Story = {
  args: {
    fileId: "file-1",
    fileName: "spec.pdf",
    displayName: "Product spec",
    mimeType: "application/pdf",
    size: 245_760,
  },
};

export const ImageList: Story = {
  render: () => (
    <FileAttachmentsList
      attachments={[
        {
          fileId: "img-1",
          fileName: "screenshot.png",
          mimeType: "image/png",
          size: 102_400,
        },
      ]}
    />
  ),
};
