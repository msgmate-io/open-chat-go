import { ChatItem, ChatItemCompact } from "@/components/chat/ChatItem"
import { LoadingSpinner } from "@/components/LoadingSpinner"
import useSWR from 'swr'
const fetcher = (...args) => fetch(...args).then(res => res.json())

function isToday(date: Date) {
    const today = new Date();
    return date.getDate() === today.getDate() &&
        date.getMonth() === today.getMonth() &&
        date.getFullYear() === today.getFullYear();
}

function isYesterday(date: Date) {
    const yesterday = new Date();
    yesterday.setDate(yesterday.getDate() - 1);
    return date.getDate() === yesterday.getDate() &&
        date.getMonth() === yesterday.getMonth() &&
        date.getFullYear() === yesterday.getFullYear();
}

function isWithinLast7Days(date: Date) {
    const sevenDaysAgo = new Date();
    sevenDaysAgo.setDate(sevenDaysAgo.getDate() - 7);
    return date >= sevenDaysAgo;
}

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
    leftPannelCollapsed,
    onToggleCollapse
}: {
    leftPannelCollapsed: boolean;
    onToggleCollapse: () => void;    
}) {
    const chatId = "abc"
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

        return chats?.rows.flatMap((chat) => {
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

            return [divider, <ChatItm chat={chat} key={`chat_${chat.uuid}`} isSelected={chat.uuid === chatId} />].filter(Boolean);
        });
    };

    return (
        <div className="flex flex-col gap-0 h-full relative w-full">
            {/**<NewChatCard onToggleCollapse={onToggleCollapse} leftPannelCollapsed={leftPannelCollapsed} />**/}
            <div className="flex flex-col flex-grow gap-1 overflow-y-auto pl-2 pr-2 relative w-full max-w-full">
                {/**<DefaultChatButtons />*/}
                {!isLoading && renderChatItems()}
            </div>
            {/**<ProfileCard />*/}
        </div>
    );
}
