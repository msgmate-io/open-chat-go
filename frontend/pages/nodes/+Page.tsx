import NodesPlattform from "@/components/nodes/NodesPlattform";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";
import React from "react";
import { PaginationUI } from "@/components/PaginationUI";
import { DataTable } from "@/components/DataTable";
import { navigate } from "vike/client/router";

export default function Page() {
    const searchParams = new URLSearchParams(typeof window !== 'undefined' ? window?.location?.search || '' : '');
    const initialPage = parseInt(searchParams?.get('page') || '1');
    const initialLimit = parseInt(searchParams?.get('limit') || '10');
    
    const [page, setPage] = React.useState(initialPage);
    const [limit, setLimit] = React.useState(initialLimit);

    React.useEffect(() => {
        const params = new URLSearchParams(window?.location?.search || '');
        params.set('page', page.toString());
        params.set('limit', limit.toString());
        navigate(`${window?.location?.pathname}?${params.toString()}`, {
            keepScrollPosition: true,
            overwriteLastHistoryEntry: true
        });
    }, [page, limit]);

    const { data: nodes } = useSWR(
        `/api/v1/federation/nodes/list?page=${page}&limit=${limit}`,
        fetcher
    );
    
    const { data: self } = useSWR(`/api/v1/federation/identity`, fetcher)

    const columns = [
        { header: "Name", accessorKey: "node_name" },
        { header: "NodeId", accessorKey: "peer_id" },
        { header: "Latest Contact", accessorKey: "latest_contact" },
    ];

    const totalPages = nodes?.total_pages || 1;
    
    const modifyRows = (rows: any[]) => {
        return rows.map((row: any) => {
            const isSelf = row.peer_id === self?.id
            if (isSelf) {
                return {
                    ...row,
                    node_name: `${row.node_name} (self)`,
                    is_self: true
                }
            }
            return {
                ...row,
                is_self: false
            }
        })
    }

    return (
        <NodesPlattform descriptor="Overview">
            Here you can view all nodes in the federation, regardless of their status.
            <div className="min-h-screen flex flex-col">
                <PaginationUI 
                    page={page}
                    totalPages={totalPages}
                    setPage={setPage}
                >
                    <h1 className="text-2xl font-bold">Nodes</h1>
                </PaginationUI>
                
                <div className="mt-2 flex-1 overflow-auto">
                    {nodes && <DataTable columns={columns} data={modifyRows(nodes?.rows || [])} />}
                </div>
            </div>
        </NodesPlattform>
    );
}