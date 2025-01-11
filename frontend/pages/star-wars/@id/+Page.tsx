import React from "react";
import { useData } from "vike-react/useData";
import { usePageContext } from "vike-react/usePageContext";
import { PageContext } from "vike/types";

const routeBase = "/star-wars"

export async function onBeforePrerenderStart() {
  return [routeBase, `${routeBase}/{id}`]
}

const useRouteParam = (param: string, path: string) => {
  if (typeof window === "undefined") {
    return null
  }
  // Convert the path template to a regex pattern
  // e.g., "/star-wars/{id}" -> "^/star-wars/([^/]+)$"
  const regexPattern = path
    .replace(/\//g, '\\/') // Escape forward slashes
    .replace(/\{([^}]+)\}/g, '([^/]+)') // Convert {param} to capture group
  const regex = new RegExp(`^${regexPattern}$`)

  // Get the current path and match against the pattern
  const windowPath = window.location.pathname
  const match = windowPath.match(regex)

  // Find the parameter position in the original path
  const paramNames = [...path.matchAll(/\{([^}]+)\}/g)].map(m => m[1])
  const paramIndex = paramNames.indexOf(param.replace(/[{}]/g, ''))

  // Return the matched parameter value or null if no match
  return match && paramIndex !== -1 ? match[paramIndex + 1] : null
}

export default function Page() {
  const id = useRouteParam("{id}", "/star-wars/{id}")
  console.log("MY ROUTE PARAM", id)
  const pageContext = usePageContext()
  console.log("PAGE CONTEXT", pageContext.routeParams)

  return (
    <div>
      <h1>Star Wars BUT IN COOOLER</h1>
      {id}
      {JSON.stringify(pageContext.routeParams)}
    </div>
  );
}
