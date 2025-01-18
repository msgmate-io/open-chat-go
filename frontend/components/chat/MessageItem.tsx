import Markdown from "react-markdown"
import logoUrl from "@/assets/logo.png"
import Prism from 'prismjs'
import { useEffect } from 'react'
// Import prism themes - choose one you prefer
import 'prismjs/themes/prism-tomorrow.css'
// Import additional languages you want to support
import 'prismjs/components/prism-python'
import 'prismjs/components/prism-javascript'
import 'prismjs/components/prism-typescript'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-json'

// Add this custom code block component
const CodeBlock = ({className, children}: {className?: string, children: any}) => {
  // className will contain the language as "language-python" etc
  const language = className?.replace('language-', '') || 'text'
  
  useEffect(() => {
    Prism.highlightAll()
  }, [children])

  const handleCopy = () => {
    const code = typeof children === 'string' ? children : children.toString()
    navigator.clipboard.writeText(code)
  }
  
  return (
    <pre className="bg-base-300 p-4 rounded-lg overflow-x-auto relative">
      <button 
        onClick={handleCopy}
        className="absolute top-2 right-2 p-1 rounded hover:bg-base-100 text-xs"
        title="Copy code"
      >
        📋
      </button>
      <code className={className}>
        {children}
      </code>
    </pre>
  )
}

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
                <Markdown components={{
                  // Customize code blocks
                  pre: noPre,
                  code: CodeBlock
                }}>
                  {message.text}
                </Markdown>
            </div>
        </div>
    </div>
}
export function noPre({children}: {children: any}){
    return <>{children}</>
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
        return <div key={message.uuid} className="flex flex-row w-full relativ max-w-full">
            <div className="flex grow content-center items-end justify-end">
                <div className="article prose w-95 overflow-x-auto p-2 px-4 rounded-2xl bg-base-200">
                    <Markdown components={{
                      pre: noPre,
                      code: CodeBlock
                    }}>
                      {message.text}
                    </Markdown>
                </div>
            </div>
        </div>
    }else{
        return <div key={message.uuid} className="flex flex-row w-full relativ max-w-full">
            <div className="flex p-2 hidden md:flex">
                <img alt="logo" className="h-9 w-9 m-2 rounded-full ring-2 ring-base-300 dark:ring-gray-500 filter grayscale" src={logoUrl} />
            </div>
            <div className="w-full flex flex-col flex-grow relative">
                <div className="article prose w-[90%] pt-3 pl-1 overflow-x-auto">
                    <Markdown components={{
                      pre: noPre,
                      code: CodeBlock
                    }}>
                      {message.text}
                    </Markdown>
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