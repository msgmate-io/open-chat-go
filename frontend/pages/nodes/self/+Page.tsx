import NodesPlattform from "@/components/nodes/NodesPlattform";
import { usePageContext } from "vike-react/usePageContext";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

export default function Page() {
    const pageContext = usePageContext()
    const { data: nodes } = useSWR(`/api/v1/federation/nodes/list`, fetcher)
    
    return (
        <NodesPlattform descriptor="Selected Node">
            <div>
            SELF PAge
            </div>
        </NodesPlattform>
    )
}