import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ToolCallDetail, ToolValueText } from "./ToolCallDetail";

describe("ToolCallDetail small-screen rendering", () => {
  it("wraps a long single-line command instead of forcing horizontal overflow", () => {
    const longCommand = `bash -lc ${"echo very-long-argument ".repeat(20)}`;
    const html = renderToStaticMarkup(<ToolCallDetail arguments={{ command: longCommand }} />);
    // Long values render in the wrapping block rather than an inline code chip.
    expect(html).toContain("break-words");
    expect(html).toContain("whitespace-pre-wrap");
    // The value block scrolls vertically; it must not rely on horizontal scroll.
    expect(html).not.toContain("overflow-x-auto");
  });

  it("renders structured results as a stacked key/value overview on small screens", () => {
    const html = renderToStaticMarkup(
      <ToolCallDetail result={{ path: "src/very/long/path/to/file.ts", exit_code: 0 }} />,
    );
    // The key/value rows stack (flex-col) and only become a two-column row at sm.
    expect(html).toContain("flex-col");
    expect(html).toContain("sm:flex-row");
    expect(html).toContain("min-w-0");
  });

  it("breaks long unbroken tokens anywhere", () => {
    const html = renderToStaticMarkup(<ToolValueText value={"a".repeat(400)} />);
    expect(html).toContain("break-words");
  });
});
