declare module "*.mdx" {
  import type { ComponentType } from "react";

  const MDXComponent: ComponentType<{
    components?: Record<string, ComponentType<any>>;
  }>;

  export const metadata: Record<string, string | number | undefined>;
  export default MDXComponent;
}
