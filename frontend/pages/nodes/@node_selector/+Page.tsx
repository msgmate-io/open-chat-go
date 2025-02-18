import NodesPlattform from "@/components/nodes/NodesPlattform";
import { usePageContext } from "vike-react/usePageContext";

export default function Page() {
    const pageContext = usePageContext()
    
    return (
        <NodesPlattform descriptor="Selected Node">
            <div>
             Page {pageContext.routeParams?.node_selector}
            </div>
        </NodesPlattform>
    )
}