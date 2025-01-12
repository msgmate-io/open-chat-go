import React from "react";
import useSWR from "swr";
import { usePageContext } from "vike-react/usePageContext";
import { Button } from "@/components/Button";
import { ChevronLeft, Trash2 } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table";
import { navigate } from "vike/client/router";

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export default function Page() {
  const pageContext = usePageContext();
  const { table_name, id } = pageContext.routeParams;

  const { data: tableInfo } = useSWR(
    `/api/v1/admin/table/${table_name}`,
    fetcher
  );

  const { data: item } = useSWR(
    `/api/v1/admin/table/${table_name}/${id}`,
    fetcher
  );

  const handleDelete = async () => {
    if (confirm("Are you sure you want to delete this item?")) {
      try {
        const response = await fetch(`/api/v1/admin/table/${table_name}/${id}`, {
          method: 'DELETE',
        });
        
        if (!response.ok) {
          throw new Error(`Error: ${response.statusText}`);
        }
        
        // Navigate back to the table list
        navigate(`/admin/${table_name}`);
      } catch (error) {
        console.error('Failed to delete item:', error);
        alert('Failed to delete item');
      }
    }
  };

  if (!item || !tableInfo) {
    return <div className="p-4">Loading...</div>;
  }

  return (
    <div className="p-4 min-h-screen">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-4">
          <Button variant="ghost" onClick={() => navigate(`/admin/${table_name}`)}>
            <ChevronLeft className="h-4 w-4 mr-2" />
            Back
          </Button>
          <h1 className="text-2xl font-bold">
            {tableInfo.name} #{id}
          </h1>
        </div>
        <Button variant="destructive" onClick={handleDelete}>
          <Trash2 className="h-4 w-4 mr-2" />
          Delete
        </Button>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableBody>
            {tableInfo.fields.map((field) => (
              <TableRow key={field.name_raw}>
                <TableCell className="font-medium w-1/3">
                  {field.name}
                </TableCell>
                <TableCell>
                  {item[field.name_raw] === null 
                    ? <span className="text-gray-400">null</span>
                    : String(item[field.name_raw])
                  }
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}