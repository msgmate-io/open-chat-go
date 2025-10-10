"use client"

import React, { useRef } from "react";
import { DragHandleDots2Icon } from "@radix-ui/react-icons";
import * as ResizablePrimitive from "react-resizable-panels";
import { cn } from "../../lib/utils";

type PanelImperativeHandle = ResizablePrimitive.PanelImperativeHandle;

function safeIsCollapsed(panel: PanelImperativeHandle | null): boolean {
  if (!panel) return false;
  try {
    return panel.isCollapsed();
  } catch {
    return false;
  }
}

type ResizablePanelGroupProps = Omit<
  React.ComponentProps<typeof ResizablePrimitive.Group>,
  "orientation" | "onLayoutChanged"
> & {
  /** @deprecated Use `orientation` instead */
  direction?: "horizontal" | "vertical";
  /** @deprecated Use `onLayoutChanged` instead */
  onLayout?: (sizes: number[]) => void;
  /** @deprecated Use `groupRef` instead */
  panelGroupRef?: React.ComponentProps<typeof ResizablePrimitive.Group>["groupRef"];
};

const ResizablePanelGroup = ({
  className,
  direction,
  orientation,
  onLayout,
  onLayoutChanged,
  panelGroupRef,
  groupRef,
  ...props
}: ResizablePanelGroupProps) => (
  <ResizablePrimitive.Group
    className={cn(
      "flex h-full w-full data-[panel-group-direction=vertical]:flex-col",
      className
    )}
    orientation={orientation ?? direction ?? "horizontal"}
    groupRef={groupRef ?? panelGroupRef}
    onLayoutChanged={(layout) => {
      onLayoutChanged?.(layout);
      onLayout?.(Object.values(layout));
    }}
    {...props}
  />
);

type ResizablePanelProps = React.ComponentProps<typeof ResizablePrimitive.Panel> & {
  onCollapse?: () => void;
  onExpand?: () => void;
};

const ResizablePanel = React.forwardRef<PanelImperativeHandle, ResizablePanelProps>(
  function ResizablePanel(
    { onCollapse, onExpand, onResize, panelRef, ...props },
    forwardedRef
  ) {
    const wasCollapsedRef = useRef<boolean | null>(null);

    const setPanelRef = (instance: PanelImperativeHandle | null) => {
      if (typeof forwardedRef === "function") {
        forwardedRef(instance);
      } else if (forwardedRef) {
        forwardedRef.current = instance;
      }

      if (typeof panelRef === "function") {
        panelRef(instance);
      } else if (panelRef) {
        panelRef.current = instance;
      }
    };

    return (
      <ResizablePrimitive.Panel
        {...props}
        panelRef={setPanelRef}
        onResize={(panelSize, id, prevPanelSize) => {
          onResize?.(panelSize, id, prevPanelSize);

          const panel =
            typeof forwardedRef === "object" && forwardedRef
              ? forwardedRef.current
              : typeof panelRef === "object" && panelRef
                ? panelRef.current
                : null;

          const collapsed = safeIsCollapsed(panel);
          if (wasCollapsedRef.current === collapsed) return;

          if (wasCollapsedRef.current !== null) {
            if (collapsed) {
              onCollapse?.();
            } else {
              onExpand?.();
            }
          }

          wasCollapsedRef.current = collapsed;
        }}
      />
    );
  }
);

const ResizableHandle = ({
  withHandle,
  className,
  ...props
}: React.ComponentProps<typeof ResizablePrimitive.Separator> & {
  withHandle?: boolean;
}) => (
  <ResizablePrimitive.Separator
    className={cn(
      "relative flex w-px items-center justify-center bg-border transition-colors hover:bg-border/80 after:absolute after:inset-y-0 after:left-1/2 after:w-1 after:-translate-x-1/2 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:ring-offset-1 data-[panel-group-direction=vertical]:h-px data-[panel-group-direction=vertical]:w-full data-[panel-group-direction=vertical]:after:left-0 data-[panel-group-direction=vertical]:after:h-1 data-[panel-group-direction=vertical]:after:w-full data-[panel-group-direction=vertical]:after:-translate-y-1/2 data-[panel-group-direction=vertical]:after:translate-x-0 [&[data-panel-group-direction=vertical]>div]:rotate-90",
      className
    )}
    {...props}
  >
    {withHandle && (
      <div className="z-10 flex h-8 w-4 items-center justify-center rounded-md border border-border/60 bg-card shadow-sm">
        <DragHandleDots2Icon className="h-2.5 w-2.5" />
      </div>
    )}
  </ResizablePrimitive.Separator>
);

export { ResizableHandle, ResizablePanel, ResizablePanelGroup };
