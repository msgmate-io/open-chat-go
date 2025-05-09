import * as React from "react"
import useSWR from "swr";
import { fetcher } from "@/lib/utils";

import { cn } from "@/components/utils"
import {
    NavigationMenu,
    NavigationMenuContent,
    NavigationMenuItem,
    NavigationMenuLink,
    NavigationMenuList,
    NavigationMenuTrigger
} from "@/components/ui/navigation-menu"

export function BotSelector({
    contact,
    selectedModel,
    setSelectedModel,
}: {
    contact: any,
    selectedModel: string,
    setSelectedModel: (model: string) => void
}) {
    console.log("TBS contact", contact)
    return (
        <NavigationMenu>
            <NavigationMenuList>
                <NavigationMenuItem>
                    <NavigationMenuTrigger>{selectedModel}</NavigationMenuTrigger>
                    <NavigationMenuContent>
                        {!contact?.profile_data?.models || contact?.profile_data?.models?.length === 0 && <div>No models found</div>}
                        {contact?.profile_data?.models && contact?.profile_data?.models?.length > 0 && <ul className="grid w-[400px] gap-3 p-4 md:w-[500px] md:grid-cols-2 lg:w-[600px] ">
                            {contact?.profile_data?.models?.map((model: any) => (
                                <ListItem
                                    className={cn("hover:secondary hover:text-content-neutral", {
                                        "bg-secondary": selectedModel === model.title,
                                    })}
                                    onClick={() => setSelectedModel(model.title)}
                                    key={model.title}
                                    title={model.title}
                                    href={model.href}
                                >
                                    {model.description}
                                </ListItem>
                            ))}
                        </ul>}
                    </NavigationMenuContent>
                </NavigationMenuItem>
            </NavigationMenuList>
        </NavigationMenu>
    )
}

export const ListItem = React.forwardRef<
    React.ElementRef<"a">,
    React.ComponentPropsWithoutRef<"a">
>(({ className, title, children, ...props }, ref) => {
    return (
        <li>
            <NavigationMenuLink asChild>
                <div
                    ref={ref}
                    className={cn(
                        "block select-none space-y-1 rounded-md p-3 leading-none no-underline outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus:bg-secondary focus:text-accent-foreground",
                        className
                    )}
                    {...props}
                >
                    <div className="text-sm font-medium leading-none">{title}</div>
                    <p className="line-clamp-2 text-sm leading-snug text-muted-foreground">
                        {children}
                    </p>
                </div>
            </NavigationMenuLink>
        </li>
    )
})
ListItem.displayName = "ListItem"

export function BotDisplay({
    selectedModel,
}: {
    selectedModel: string
}) {
    return (
        <div className="inline-flex items-center justify-center rounded-md px-3 py-2 text-sm font-medium bg-secondary text-secondary-foreground font-bold">
            {selectedModel}
        </div>
    )
}
