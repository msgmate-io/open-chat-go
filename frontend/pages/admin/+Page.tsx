import React from "react";
import useSWR from "swr";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/Card";
import { Button } from "@/components/Button";

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export default function Page() {
  const { data: tables } = useSWR(`/api/v1/admin/tables`, fetcher)
  
  return (
    <div className="container mx-auto p-8">
      <Card>
        <CardHeader>
          <CardTitle className="text-3xl">Admin Dashboard</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {tables?.map((table: any) => (
              <Button
                key={table.name}
                variant="outline"
                className="h-24 w-full flex flex-col items-center justify-center text-lg hover:bg-accent"
                asChild
              >
                <a href={`/admin/${table.name}`}>
                  {table.name}
                </a>
              </Button>
            ))}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
