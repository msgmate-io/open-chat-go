import { Button } from "@/components/ui/button"
import React from "react"
import { cn } from "@/lib/utils"

export const PaginationUI = ({ page, totalPages, setPage, children }: { page: number, totalPages: number, setPage: (page: number) => void, children: React.ReactNode }) => {
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

