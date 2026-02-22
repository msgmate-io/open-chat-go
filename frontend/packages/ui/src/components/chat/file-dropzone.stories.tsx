import type { Meta, StoryObj } from "@storybook/react-vite";
import { FileDropOverlay, UploadErrorsNotice, type UploadRejection } from "./FileUpload";

/**
 * The two transient states of the composer's drag-and-drop surface: the
 * drag-over affordance that covers the composer while files are dragged onto it,
 * and the inline notice that explains files the drop (or the pickers) rejected.
 *
 * Both are presentational — the drop target itself lives on
 * `MessageInputWithFiles` — so they can be reviewed here without a live drag or
 * an upload round-trip.
 */
const meta = {
  title: "Chat/FileDropzone",
  component: FileDropOverlay,
  parameters: { layout: "padded" },
} satisfies Meta<typeof FileDropOverlay>;

export default meta;
type Story = StoryObj<typeof meta>;

/** The dashed "Drop files to attach" affordance, with the accepted types + cap. */
export const DragOver: Story = {
  args: { visible: true },
};

/** Nothing is rendered when no file drag is in progress. */
export const Hidden: Story = {
  args: { visible: false },
};

const rejected: UploadRejection[] = [
  {
    fileName: "quarterly-report.pdf",
    reason: "too-large",
    message: "“quarterly-report.pdf” is 8.4 MB — the maximum size is 5.0 MB.",
  },
  {
    fileName: "archive.zip",
    reason: "unsupported-type",
    message:
      "“archive.zip” isn’t a supported file type — attach images, PDF, text, Word or Excel files.",
  },
  {
    fileName: "screenshots",
    reason: "folder",
    message: "Folders can’t be attached — drop or pick the files inside instead.",
  },
];

/**
 * The inline rejection notice, shown for a dropped batch with mixed files: the
 * supported, small ones attach while the rest are explained here.
 */
export const RejectedFiles: Story = {
  args: { visible: false },
  render: () => <UploadErrorsNotice errors={rejected} onDismiss={() => {}} />,
};
