import Markdown from "react-markdown"

export function UserMessageItem({
    message,
    chat,
    selfIsSender = false
}: {
    message: any,
    chat: any,
    selfIsSender?: boolean
}) {

    const isBotChat = chat?.partner?.is_bot
    return <div key={message.uuid} className="flex flex-row px-4 w-full relativ max-w-full">
        <div className="flex">
            <div className="w-8 m-2 hidden md:flex">
                {selfIsSender ? <div>🙂</div> : <div>👾</div>}
            </div>
        </div>
        <div className="w-full flex flex-col flex-grow relative">
            <div className="flex flex-row font-bold w-full">
                {selfIsSender ? "You" : `${chat?.partner?.name}`}
            </div>
            <div className="article prose w-95 overflow-x-auto">
                <Markdown>{message.text}</Markdown>
            </div>
        </div>
    </div>
}

export function BotMessageItem({
    message,
    chat,
    selfIsSender = false
}: {
    message: any,
    chat: any,
    selfIsSender?: boolean
}) {

    if(selfIsSender){
        return <div key={message.uuid} className="flex flex-row px-4 w-full relativ max-w-full">
            <div className="flex grow content-center items-end justify-end">
                <div className="article prose w-95 overflow-x-auto p-2 px-4 rounded-2xl bg-base-200">
                    <Markdown>{message.text}</Markdown>
                </div>
            </div>
        </div>
    }else{
        return <div key={message.uuid} className="flex flex-row px-4 w-full relativ max-w-full">
            <div className="flex p-2 hidden md:flex">
                <img alt="logo" className="h-9 w-9 m-3 rounded-full ring-2 ring-base-300 dark:ring-gray-500 filter grayscale" src="/logo.png" />
            </div>
            <div className="w-full flex flex-col flex-grow relative">
                <div className="article prose w-95 pt-3 pl-1 overflow-x-auto">
                    <Markdown>{message.text}</Markdown>
                </div>
            </div>
        </div>
    }
}

export function MessageItem({
    message,
    chat,
    selfIsSender = false,
    isBotChat = false,
}: {
    message: any,
    chat: any,
    selfIsSender?: boolean,
    isBotChat?: boolean
}) {
    if(isBotChat){
        return <BotMessageItem message={message} chat={chat} selfIsSender={selfIsSender} />
    }else{
        return <UserMessageItem message={message} chat={chat} selfIsSender={selfIsSender} />
    }
}