import React from "react";
import { ColumnDef } from "@tanstack/react-table"
import useSWR from "swr";
import { usePageContext } from "vike-react/usePageContext";
import { DataTable } from "./DataTable";
import { Button } from "@/components/Button";
import { Settings, ChevronLeft } from "lucide-react";
import { navigate } from "vike/client/router";

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export default function Page() {
  const pageContext = usePageContext()
  
  // Get initial values from URL search params
  const searchParams = new URLSearchParams(window.location.search);
  const initialPage = parseInt(searchParams.get('page') || '1');
  const initialLimit = parseInt(searchParams.get('limit') || '10');
  
  const [page, setPage] = React.useState(initialPage)
  const [limit, setLimit] = React.useState(initialLimit)
  const [settingsOpen, setSettingsOpen] = React.useState(false)
  
  // Update URL when page/limit changes
  React.useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    params.set('page', page.toString());
    params.set('limit', limit.toString());
    navigate(`${window.location.pathname}?${params.toString()}`, {
      keepScrollPosition: true,
      overwriteLastHistoryEntry: true
    });
  }, [page, limit]);

  const { data: table } = useSWR(`/api/v1/admin/table/${pageContext.routeParams.table_name}`, fetcher)
  const { data: tableData } = useSWR(
    `/api/v1/admin/table/${pageContext.routeParams.table_name}/data?page=${page}&limit=${limit}`,
    fetcher
  )
  
  const columns = table?.fields?.map((field: any) => ({
    header: field.name,
    accessorKey: field.name_raw,
  }))

  const totalPages = tableData?.total_pages || 1

  return <div className="p-4 min-h-screen">
    <div className="flex items-center justify-between mb-4">
      <div className="flex items-center gap-4">
        <Button variant="ghost" onClick={() => window.history.back()}>
          <ChevronLeft className="h-4 w-4 mr-2" />
          Back
        </Button>
        <h1 className="text-2xl font-bold">{table?.name}</h1>
      </div>
      <Button variant="outline" onClick={() => setSettingsOpen(true)}>
        <Settings className="h-4 w-4 mr-2" />
        Settings
      </Button>
    </div>

    {tableData && <DataTable columns={columns} data={tableData?.rows || []} />}
    
    <div className="flex items-center justify-between mt-4">
      <div className="text-sm text-gray-500">
        Page {page} of {totalPages}
      </div>
      <div className="space-x-2">
        <Button 
          onClick={() => setPage(p => Math.max(1, p - 1))}
          disabled={page === 1}
        >
          Previous
        </Button>
        <Button
          onClick={() => setPage(p => Math.min(totalPages, p + 1))}
          disabled={page === totalPages}
        >
          Next
        </Button>
      </div>
    </div>

    {/* TODO: Add TableSettingsModal component here */}
    {/* <TableSettingsModal open={settingsOpen} onOpenChange={setSettingsOpen} table={table} /> */}
  </div>;
}
