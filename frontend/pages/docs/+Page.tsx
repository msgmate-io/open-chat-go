import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Button, Text, TextTypes } from "@open-chat-go/ui";
import { ExternalLink } from "lucide-react";
import { DocsLayout } from "@/components/docs/DocsLayout";
import { docs } from "./docs";
import { useCurrentUser } from "@open-chat-go/ui";

const getInitialDocPath = () => {
  if (typeof window === "undefined") {
    return docs[0]?.path ?? "";
  }
  const hashDocPath = window.location.hash.replace("#", "").split("?")[0];
  return docs.find((doc) => doc.path === hashDocPath)?.path ?? docs[0]?.path ?? "";
};

export default function Page() {
  const { data: user } = useCurrentUser();
  const visibleDocs = useMemo(
    () => docs.filter((doc) => !doc.adminOnly || !!user?.is_admin),
    [user?.is_admin],
  );

  const [activeDocPath, setActiveDocPath] = useState(getInitialDocPath);
  const activeDoc = visibleDocs.find((doc) => doc.path === activeDocPath) ?? visibleDocs[0];
  const ActiveDoc = activeDoc?.component;

  const selectDoc = useCallback((path: string) => {
    setActiveDocPath(path);
    if (typeof window !== "undefined") {
      window.location.hash = path;
    }
  }, []);

  useEffect(() => {
    const syncFromHash = () => {
      const hashDocPath = window.location.hash.replace("#", "").split("?")[0];
      const nextDoc = visibleDocs.find((doc) => doc.path === hashDocPath) ?? visibleDocs[0];
      if (nextDoc) {
        setActiveDocPath(nextDoc.path);
      }
    };

    syncFromHash();
    window.addEventListener("hashchange", syncFromHash);
    return () => window.removeEventListener("hashchange", syncFromHash);
  }, [visibleDocs]);

  // Vike intercepts same-origin anchor clicks and rewrites the URL with
  // history.pushState, which does not fire `hashchange`. Intercept in-docs
  // anchors ourselves (capture phase, before Vike's document-level listener)
  // so that switching between documentation pages stays a client-side state
  // change and never triggers a dead navigation.
  useEffect(() => {
    const onClick = (event: MouseEvent) => {
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      ) {
        return;
      }
      const target = event.target as HTMLElement | null;
      const anchor = target?.closest?.("a");
      if (!anchor || !anchor.closest(".docs-content")) {
        return;
      }
      const href = anchor.getAttribute("href");
      if (!href || !href.includes("#")) {
        return;
      }
      const [pathPart, hashAndQuery] = href.split("#");
      if (pathPart && pathPart !== "/docs" && pathPart !== ".") {
        return;
      }
      const docPath = hashAndQuery.split("?")[0];
      if (!docPath || !visibleDocs.some((doc) => doc.path === docPath)) {
        return;
      }
      event.preventDefault();
      event.stopPropagation();
      selectDoc(docPath);
    };

    document.addEventListener("click", onClick, true);
    return () => document.removeEventListener("click", onClick, true);
  }, [visibleDocs, selectDoc]);

  const mdxComponents = useMemo(
    () => ({
      h1: ({ color: _color, ...props }: React.ComponentProps<"h1">) => (
        <Text type={TextTypes.Heading4} tag="h1" className="docs-h1" {...props} />
      ),
      h2: ({ color: _color, ...props }: React.ComponentProps<"h2">) => (
        <Text type={TextTypes.Heading5} tag="h2" className="docs-h2" bold {...props} />
      ),
      h3: ({ color: _color, ...props }: React.ComponentProps<"h3">) => (
        <Text type={TextTypes.Heading6} tag="h3" className="docs-h3" bold {...props} />
      ),
      p: ({ color: _color, ...props }: React.ComponentProps<"p">) => (
        <Text type={TextTypes.Body5} tag="p" color="muted" className="docs-p" {...props} />
      ),
      ul: (props: React.ComponentProps<"ul">) => <ul className="docs-ul" {...props} />,
      ol: (props: React.ComponentProps<"ol">) => <ol className="docs-ol" {...props} />,
      li: ({ color: _color, ...props }: React.ComponentProps<"li">) => (
        <Text type={TextTypes.Body5} tag="li" color="muted" className="docs-li" {...props} />
      ),
      a: (props: React.ComponentProps<"a">) => <a className="docs-a" {...props} />,
      code: (props: React.ComponentProps<"code">) => <code className="docs-code" {...props} />,
      pre: (props: React.ComponentProps<"pre">) => <pre className="docs-pre" {...props} />,
      table: (props: React.ComponentProps<"table">) => <table className="docs-table" {...props} />,
      blockquote: (props: React.ComponentProps<"blockquote">) => (
        <blockquote className="docs-blockquote" {...props} />
      ),
      hr: (props: React.ComponentProps<"hr">) => <hr className="docs-hr" {...props} />,
    }),
    [],
  );

  return (
    <DocsLayout activeDocPath={activeDocPath} onSelect={selectDoc}>
      {activeDoc ? (
        <article className="docs-content">
          <header className="docs-article-header">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0">
                <Text type={TextTypes.Heading4} tag="h1" className="docs-h1">
                  {activeDoc.title}
                </Text>
                {activeDoc.description ? (
                  <Text type={TextTypes.Body5} tag="p" color="muted" className="docs-p">
                    {activeDoc.description}
                  </Text>
                ) : null}
              </div>
              {activeDoc.sourceUrl ? (
                <Button asChild variant="outline" size="sm">
                  <a href={activeDoc.sourceUrl} target="_blank" rel="noreferrer">
                    <ExternalLink className="mr-2 size-4" />
                    MDX Source
                  </a>
                </Button>
              ) : null}
            </div>
          </header>
          <ActiveDoc components={mdxComponents} />
        </article>
      ) : (
        <Text type={TextTypes.Body5} color="muted">
          No visible documentation found.
        </Text>
      )}
    </DocsLayout>
  );
}
