import NodesPlattform from "@/components/nodes/NodesPlattform";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

export default function Page() {

    const { data: nodes } = useSWR(`/api/v1/federation/nodes/list`, fetcher)

    return (
        <NodesPlattform descriptor="Overview">
            <div>
                Here you can find all your nodes
                {JSON.stringify(nodes)}
            </div>
        </NodesPlattform>
    )
}