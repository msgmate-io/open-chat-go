import React from "react";
import useSWR from "swr";
import { usePageContext } from "vike-react/usePageContext";
import { Button } from "@/components/Button";
import { ChevronLeft } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table";

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

  if (!item || !tableInfo) {
    return <div className="p-4">Loading...</div>;
  }

  return (
    <div className="p-4 min-h-screen">
      <div className="flex items-center gap-4 mb-6">
        <Button variant="ghost" onClick={() => window.history.back()}>
          <ChevronLeft className="h-4 w-4 mr-2" />
          Back
        </Button>
        <h1 className="text-2xl font-bold">
          {tableInfo.name} #{id}
        </h1>
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