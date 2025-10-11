"use client"

import { Fragment, useMemo } from "react"
import { ChevronRight } from "lucide-react"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
  Text,
  TextTypes,
  cn,
} from "@open-chat-go/ui"
import { docs } from "@/pages/docs/docs"
import { useCurrentUser } from "@open-chat-go/ui"
import { ConnectedThemeSelector } from "@open-chat-go/ui"

type DocsNavContentProps = {
  activeDocPath: string
  onSelect: (path: string) => void
  header?: React.ReactNode
}

export function DocsNavContent({ activeDocPath, onSelect, header }: DocsNavContentProps) {
  const { data: user } = useCurrentUser()
  const visibleDocs = useMemo(
    () => docs.filter((doc) => !doc.adminOnly || !!user?.is_admin),
    [user?.is_admin],
  )

  const childrenByParent = useMemo(() => {
    const map = new Map<string, typeof visibleDocs>()
    visibleDocs.forEach((doc) => {
      if (!doc.parent) return
      const existing = map.get(doc.parent) ?? []
      existing.push(doc)
      map.set(doc.parent, existing)
    })
    return map
  }, [visibleDocs])

  const topLevelDocs = useMemo(
    () => visibleDocs.filter((doc) => !doc.parent || !visibleDocs.some((candidate) => candidate.path === doc.parent)),
    [visibleDocs],
  )

  const sectionedDocs = useMemo(() => {
    const map = new Map<string, typeof topLevelDocs>()
    topLevelDocs.forEach((doc) => {
      const key = doc.section || "General"
      const list = map.get(key) ?? []
      list.push(doc)
      map.set(key, list)
    })
    return Array.from(map.entries())
  }, [topLevelDocs])

  const normalize = (value: string) => value.trim().toLowerCase()

  const renderRow = (docPath: string, title: string, { child = false }: { child?: boolean } = {}) => (
    <button
      key={docPath}
      type="button"
      onClick={() => onSelect(docPath)}
      className={cn(
        "docs-nav-row",
        child && "docs-nav-row--child",
        activeDocPath === docPath && "docs-nav-row--selected",
      )}
    >
      <Text
        type={TextTypes.Body6}
        tag="span"
        className="min-w-0 flex-1 truncate"
        color={activeDocPath === docPath ? "foreground" : "muted"}
      >
        {title}
      </Text>
    </button>
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      {header ?? (
        <div className="chat-list-header">
          <Text type={TextTypes.Body5} tag="span" bold className="min-w-0 flex-1 truncate">
            Documentation
          </Text>
        </div>
      )}
      <div className="chat-list-scroll docs-nav-scroll">
        {sectionedDocs.map(([section, entries]) => {
          const isOpen = entries.some((entry) => {
            if (entry.path === activeDocPath) return true
            return (childrenByParent.get(entry.path) ?? []).some((child) => child.path === activeDocPath)
          })
          return (
            <Collapsible key={section} defaultOpen={isOpen} className="group/collapsible">
              <CollapsibleTrigger className="docs-nav-section">
                <Text type={TextTypes.Body7} tag="span" bold className="flex-1 text-left uppercase tracking-wide">
                  {section}
                </Text>
                <ChevronRight className="size-3.5 transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
              </CollapsibleTrigger>
              <CollapsibleContent className="mt-1 space-y-1">
                {entries.map((doc) => {
                  const children = childrenByParent.get(doc.path) ?? []
                  const isSectionLanding = normalize(doc.title) === normalize(section)

                  if (children.length > 0 && isSectionLanding) {
                    return (
                      <Fragment key={doc.path}>
                        {renderRow(doc.path, doc.title)}
                        {children.map((child) => renderRow(child.path, child.title, { child: true }))}
                      </Fragment>
                    )
                  }

                  if (children.length === 0) {
                    return renderRow(doc.path, doc.title)
                  }

                  const childOpen = activeDocPath === doc.path || children.some((child) => child.path === activeDocPath)
                  return (
                    <Collapsible key={doc.path} defaultOpen={childOpen} className="group/subcollapsible">
                      <CollapsibleTrigger asChild>
                        {renderRow(doc.path, doc.title)}
                      </CollapsibleTrigger>
                      <CollapsibleContent className="space-y-1">
                        {children.map((child) => renderRow(child.path, child.title, { child: true }))}
                      </CollapsibleContent>
                    </Collapsible>
                  )
                })}
              </CollapsibleContent>
            </Collapsible>
          )
        })}
      </div>
      <div className="chat-list-footer">
        <ConnectedThemeSelector />
      </div>
    </div>
  )
}
