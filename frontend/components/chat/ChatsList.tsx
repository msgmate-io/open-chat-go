import { ChatItemCompact } from "@/components/chat/ChatItem"
import { ProfileCard } from "@/components/chat/ProfileCard"
import { LoadingSpinner } from "@/components/LoadingSpinner"
import { isToday, isYesterday, isWithinLast7Days } from "@/components/utils"
import useSWR from 'swr'
import { DefaultChats } from "./DefaultChats"
import { NewChatCard } from "./NewChatCard"

const fetcher = (...args: [RequestInfo, RequestInit?]) => fetch(...args).then(res => res.json())

export const ExploreChatsIcon = () => (
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" fill="none" viewBox="0 0 24 24" className="icon-md">
        <path
            fill="currentColor"
            fillRule="evenodd"
            d="M6.75 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 4.5a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 6.75a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M6.75 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M2.5 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0M17.25 15a2.25 2.25 0 1 0 0 4.5 2.25 2.25 0 0 0 0-4.5M13 17.25a4.25 4.25 0 1 1 8.5 0 4.25 4.25 0 0 1-8.5 0"
            clipRule="evenodd"
        />
    </svg>
);

export function ChatsList({
    chatUUID,
    leftPannelCollapsed,
    onToggleCollapse,
    navigateTo
}: {
    chatUUID: string | null,
    leftPannelCollapsed: boolean;
    onToggleCollapse: () => void;    
    navigateTo: (to: string) => void
}) {
    const { data: chats, isLoading } = useSWR('/api/v1/chats/list', fetcher)

    const ChatItm = ChatItemCompact

    const renderDivider = (label: string) => (
        <div className="text-gray-500 font-bold my-2" key={label}>{label}</div>
    );

    const renderChatItems = () => {
        if (!chats) {
            return <div className='flex flex-grow w-full h-full items-center content-center justify-center'>
                <LoadingSpinner />
            </div>
        }

        let lastDivider: string | null = null

        return chats?.rows.flatMap((chat: any) => {
            const chatDate = new Date(); // TODO: chat.newest_message.created use the tie of the newest message
            let divider = null;

            if (isToday(chatDate)) {
                if (lastDivider !== 'Today') {
                    divider = renderDivider('Today');
                    lastDivider = 'Today';
                }
            } else if (isYesterday(chatDate)) {
                if (lastDivider !== 'Yesterday') {
                    divider = renderDivider('Yesterday');
                    lastDivider = 'Yesterday';
                }
            } else if (isWithinLast7Days(chatDate)) {
                if (lastDivider !== 'Previous 7 Days') {
                    divider = renderDivider('Previous 7 Days');
                    lastDivider = 'Previous 7 Days';
                }
            }

            return [divider, <ChatItm chat={chat} key={`chat_${chat.uuid}`} isSelected={chat.uuid === chatUUID} navigateTo={navigateTo} />].filter(Boolean);
        });
    };

    return (
        <div className="flex flex-col gap-0 h-full relative w-full">
            <NewChatCard 
                leftPannelCollapsed={leftPannelCollapsed}
                onToggleCollapse={onToggleCollapse}
                navigateTo={navigateTo}
            />
            <div className="flex flex-col flex-grow gap-1 overflow-y-auto pl-2 pr-2 relative w-full max-w-full">
                <DefaultChats navigateTo={navigateTo} />
                {!isLoading && renderChatItems()}
            </div>
            <ProfileCard navigateTo={navigateTo} />
        </div>
    );
}
