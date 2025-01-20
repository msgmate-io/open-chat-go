import { CinematicLogo } from "@/components/CinematicLogo"
import { useContext } from "react"
import logoUrl from "@/assets/logo.png"

export const ShinyText = ({ children }: { children: React.ReactNode }) => {
    return (
        <span className="shiny-text" data-content={children}>
            {children}
        </span>
    );
};

export function PendingMessageItem({
    text = "Reasoning..."
}) {


    return <div key={"pending"} className="flex flex-row w-full relativ max-w-full">
        <div className="flex p-2 hidden md:flex">
            <img alt="logo" className="h-9 w-9 m-2 rounded-full ring-2 ring-base-300 dark:ring-gray-500 filter grayscale" src={logoUrl} />
        </div>
        <div className="w-full flex flex-col flex-grow relative">
            <div className="article prose w-full pt-3 overflow-x-hidden text-bold font-bold">
                <ShinyText>{text}</ShinyText>
            </div>
        </div>
    </div>
}