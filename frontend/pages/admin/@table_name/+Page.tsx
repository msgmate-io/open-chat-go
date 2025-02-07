import React from "react";
import { ColumnDef } from "@tanstack/react-table"
import useSWR from "swr";
import { usePageContext } from "vike-react/usePageContext";
import { DataTable } from "./DataTable";
import { Button } from "@/components/Button";
import { Settings, ChevronLeft } from "lucide-react";
import { navigate } from "vike/client/router";

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

const routeBase = "/admin"
export async function onBeforePrerenderStart() {
  return [routeBase, `${routeBase}/{table_name}`]
}

const PaginationUI = ({ page, totalPages, setPage, children }: { page: number, totalPages: number, setPage: (page: number) => void, children: React.ReactNode }) => {
    return <div className="flex items-center justify-between sticky top-0 p-2 bg-base-100 z-10 shadow-sm">
      <div className="text-sm text-gray-500">
        {children}
      </div>
      <div className="space-x-2">
        {page > 1 && <Button 
          onClick={() => setPage(1)}
          disabled={page === 1}
          variant="outline"
          size="sm"
        >
          0
        </Button>}
        {page > 0 && <Button 
          onClick={() => setPage(p => Math.max(1, p - 1))}
          disabled={page === 1}
          variant="outline"
          size="sm"
        >
          {page - 1}
        </Button>}
        <Button
          variant="default"
          size="sm"
        >
          {page}
        </Button>
        <Button
          onClick={() => setPage(p => Math.min(totalPages, p + 1))}
          disabled={page === totalPages}
          variant="outline"
          size="sm"
        >
          {page + 1}
        </Button>
        <Button
          onClick={() => setPage(totalPages)}
          disabled={page === totalPages}
          variant="outline"
          size="sm"
        >
          Last ({totalPages})
        </Button>
      </div>
    </div>
}

export default function Page() {
  const pageContext = usePageContext()
  
  // Get initial values from URL search params
  const searchParams = new URLSearchParams(typeof window !== 'undefined' ? window?.location?.search || '' : '');
  const initialPage = parseInt(searchParams?.get('page') || '1');
  const initialLimit = parseInt(searchParams?.get('limit') || '10');
  
  const [page, setPage] = React.useState(initialPage)
  const [limit, setLimit] = React.useState(initialLimit)
  const [settingsOpen, setSettingsOpen] = React.useState(false)
  
  // Update URL when page/limit changes
  React.useEffect(() => {
    const params = new URLSearchParams(window?.location?.search || '');
    params.set('page', page.toString());
    params.set('limit', limit.toString());
    navigate(`${window?.location?.pathname}?${params.toString()}`, {
      keepScrollPosition: true,
      overwriteLastHistoryEntry: true
    });
  }, [page, limit]);

  const table_name = pageContext.routeParams.table_name
  const { data: table } = useSWR(`/api/v1/admin/table/${table_name}`, fetcher)
  const { data: tableData } = useSWR(
    `/api/v1/admin/table/${table_name}/data?page=${page}&limit=${limit}`,
    fetcher
  )
  
  const columns = table?.fields?.map((field: any) => ({
    header: field.name,
    accessorKey: field.name_raw,
  }))

  const totalPages = tableData?.total_pages || 1

  return <div className="min-h-screen flex flex-col">
    <PaginationUI 
      page={page}
      totalPages={totalPages}
      setPage={setPage}
    >
      <div className="flex items-center gap-4">
        <Button variant="ghost" onClick={() => navigate(`/admin/`)}>
          <ChevronLeft className="h-4 w-4 mr-2" />
          Back
        </Button>
        <h1 className="text-2xl font-bold">{table?.name}</h1>
        <Button variant="outline" onClick={() => setSettingsOpen(true)}>
          <Settings className="h-4 w-4 mr-2" />
          Settings
        </Button>
      </div>
    </PaginationUI>
    
    <div className="mt-2 flex-1 overflow-auto">
      {tableData && <DataTable columns={columns} data={tableData?.rows || []} />}
    </div>

    {/* TODO: Add TableSettingsModal component here */}
    {/* <TableSettingsModal open={settingsOpen} onOpenChange={setSettingsOpen} table={table} /> */}
  </div>;
}
