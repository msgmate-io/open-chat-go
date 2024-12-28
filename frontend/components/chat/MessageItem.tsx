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

export function MessageItem({
    message,
    chat,
    selfIsSender = false
}: {
    message: any,
    chat: any,
    selfIsSender?: boolean
}) {
    // Hide if it's a data message with hide = True
    return <UserMessageItem message={message} chat={chat} selfIsSender={selfIsSender} />
}