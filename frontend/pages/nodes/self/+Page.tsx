import NodesPlattform from "@/components/nodes/NodesPlattform";
import { usePageContext } from "vike-react/usePageContext";
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

export default function Page() {
    const pageContext = usePageContext()
    const { data: nodes } = useSWR(`/api/v1/federation/nodes/list`, fetcher)
    const { data: identity } = useSWR(`/api/v1/federation/identity`, fetcher)
    
    return (
        <NodesPlattform descriptor="Selected Node">
            <div>
            <h1 className="text-3xl font-bold text-foreground">Your Node:</h1>
            <h2 className="text-2xl font-bold text-foreground">Id: {identity?.id}</h2>
            <h2 className="text-2xl font-bold text-foreground">Addresses:</h2>
            <ul>
                <li>
                    {identity?.connect_multiadress?.map((address: any) => (
                        <div key={address}>{address}</div>
                    ))}
                </li>
            </ul>
            </div>
        </NodesPlattform>
    )
}