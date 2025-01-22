import Markdown from "react-markdown"
import logoUrl from "@/assets/logo.png"
import Prism from 'prismjs'
import { useEffect } from 'react'
import { cn } from "@/lib/utils"
// Import prism themes - choose one you prefer
import 'prismjs/themes/prism-tomorrow.css'
// Import additional languages you want to support
import 'prismjs/components/prism-python'
import 'prismjs/components/prism-javascript'
import 'prismjs/components/prism-typescript'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-json'
import React from 'react'
import { PendingMessageItem, ShinyText } from "./PendingMessageItem"
import {
    Collapsible,
    CollapsibleContent,
    CollapsibleTrigger,
  } from "@/components/ui/collapsible"
  

// Add this custom wrapper component for code blocks
const CodeWrapper = ({children}: {children: any}) => {
  // This ensures code blocks are rendered properly outside of paragraphs
  const codeBlock = React.Children.toArray(children).find(
    (child: any) => child?.props?.className?.includes('language-')
  )
  
  if (codeBlock) {
    return <div className="">{children}</div>
  }
  
  return <div className="">{children}</div>
}

// Add this custom code block component
const CodeBlock = ({className, children}: {className?: string, children: any}) => {
  const language = className?.replace('language-', '') || 'text'
  
  useEffect(() => {
    if (typeof children === 'string' && children.includes('\n')) {
      Prism.highlightElement(document.querySelector(`code.language-${language}`))
    }
  }, [children, language])

  const handleCopy = () => {
    const code = typeof children === 'string' ? children : children.toString()
    navigator.clipboard.writeText(code)
  }

  // Check if this is a multiline code block
  const isMultiLine = typeof children === 'string' && children.includes('\n')

  return (
    isMultiLine ?
    <pre className="relative light:bg-secondary/50 dark:bg-secondary/30 p-4 rounded-lg overflow-x-auto my-4">
      <button 
        onClick={handleCopy}
        className="absolute top-2 right-2 p-1 rounded hover:bg-secondary text-xs"
        title="Copy code"
        >
          📋
        </button>
      <code className={cn(
        className,
        "block",
        isMultiLine && "whitespace-pre"
      )} data-language={language}>
        {children}
      </code>
      </pre> :
    <code data-language={language} className="text-foreground">
      {children}
    </code>
  )
}

export function MessageLink({children, props}: {children: any, props: any}) {
    return <a className="text-foreground" {...props}>{children}</a>
}

class MessageMarkdownErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean }
> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(_: Error) {
    return { hasError: true };
  }

  render() {
    if (this.state.hasError) {
      return <div className="text-foreground">couldn't render</div>;
    }

    return this.props.children;
  }
}

export function MessageMarkdown({children}: {children: any}) {
    return (
      <MessageMarkdownErrorBoundary>
        <Markdown components={{
            blockquote: ({children, ...props}) => <blockquote className="text-foreground" {...props}>{children}</blockquote>,
            h1: ({children, ...props}) => <h1 className="text-foreground" {...props}>{children}</h1>,
            h2: ({children, ...props}) => <h2 className="text-foreground" {...props}>{children}</h2>,
            h3: ({children, ...props}) => <h3 className="text-foreground" {...props}>{children}</h3>,
            h4: ({children, ...props}) => <h4 className="text-foreground" {...props}>{children}</h4>,
            h5: ({children, ...props}) => <h5 className="text-foreground" {...props}>{children}</h5>,
            h6: ({children, ...props}) => <h6 className="text-foreground" {...props}>{children}</h6>,
            strong: ({children, ...props}) => <strong className="text-foreground" {...props}>{children}</strong>,
            a: ({children, ...props}) => <MessageLink props={props}>{children}</MessageLink>,
            p: ({children, ...props}) => <>{children}</>,
            code: ({children, ...props}) => <CodeBlock {...props}>{children}</CodeBlock>,
            pre: ({children, ...props}) => <CodeWrapper {...props}>{children}</CodeWrapper>
        }}>{children}</Markdown>
      </MessageMarkdownErrorBoundary>
    );
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
                <MessageMarkdown>
                    {message.text}
                </MessageMarkdown>
            </div>
        </div>
    </div>
}

function wrapInBlockquote(children: any) {
    return <blockquote className="text-foreground text-sm">{children}</blockquote>
}

import { ThumbsUp, ThumbsDown, Volume2, Clipboard, Pen, RefreshCcw } from "lucide-react";

const BotMessageToolbar = ({message}: {message: any}) => {
    let tokensPerSecond = 0.0
    let parsedTotalTime = 0.0
    if(message?.meta_data?.total_time && message?.meta_data?.token_usage?.completion_tokens){
        parsedTotalTime = parseFloat(message?.meta_data?.total_time)
        tokensPerSecond = parseFloat((message?.meta_data?.token_usage?.completion_tokens / parsedTotalTime).toFixed(2))
    }
  return (
    <div className="flex items-center gap-3 p-2 rounded-lg text-foreground">
      <Clipboard className="cursor-pointer hover:text-foreground" size={20} />
      {/*   <ThumbsUp className="cursor-pointer hover:text-foreground" size={20} />
      <ThumbsDown className="cursor-pointer hover:text-foreground" size={20} />
      <Volume2 className="cursor-pointer hover:text-foreground" size={20} />*/}
      {/*<Pen className="cursor-pointer hover:text-white" size={20} />*/}
      <RefreshCcw className="cursor-pointer hover:text-foreground" size={20} />
      {message?.is_generating && <span className="text-foreground text-sm"> {message?.meta_data?.total_time} generating...</span>}
      {(!message?.is_generating && !message?.meta_data?.cancelled && message?.text !== "") && <span className="text-foreground text-sm"> (prompt: {message?.meta_data?.token_usage?.prompt_tokens} completion: {message?.meta_data?.token_usage?.completion_tokens} total: {message?.meta_data?.token_usage?.total_tokens}) in {message?.meta_data?.total_time} ({tokensPerSecond} tokens/s)</span>}
      {message?.meta_data?.cancelled && <span className="text-foreground text-sm">Cancelled</span>}
    </div>
  );
};

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
                <div className="article prose w-95 overflow-x-auto p-2 px-4 rounded-2xl bg-background text-foreground">
                    <MessageMarkdown>
                        {message.text}
                    </MessageMarkdown>
                </div>
            </div>
        </div>
    }else{
        console.log("message thoughts", message?.thoughts)
        return <div key={message.uuid} className="flex flex-row w-full relativ max-w-full">
            <div className="flex p-2 hidden md:flex">
                <img alt="logo" className="h-9 w-9 m-2 rounded-full ring-2 ring-base-300 dark:ring-gray-500 filter grayscale" src={logoUrl} />
            </div>
            <div className="w-full flex flex-col flex-grow relative">
                <div className="article prose w-[90%] pt-3 pl-1 overflow-x-auto text-foreground">
                    {message?.text === "" && message.thoughts.length === 0 && <ShinyText>Booting AI...</ShinyText>}
                    {message?.text === "" && message.thoughts.length > 0 && <Collapsible open={true} id="thoughts">
                        <CollapsibleTrigger><ShinyText>Thinking... ({message?.meta_data?.thinking_time})</ShinyText></CollapsibleTrigger>
                        <CollapsibleContent>
                            {message?.thoughts?.map((thought: any, index: number) => (
                                <div key={index}>{wrapInBlockquote(thought)}</div>
                            ))}
                        </CollapsibleContent>
                    </Collapsible>}
                    {message?.text !== "" && message?.thoughts?.length > 0 && <Collapsible id="thoughts">
                      <CollapsibleTrigger>Thoughts for {message?.meta_data?.thinking_time}</CollapsibleTrigger>
                      <CollapsibleContent>
                        {message?.thoughts?.map((thought: any, index: number) => (
                            <div key={index}>{wrapInBlockquote(thought)}</div>
                        ))}
                      </CollapsibleContent>
                    </Collapsible>}
                    <MessageMarkdown>
                        {message.text}
                    </MessageMarkdown>
                    <BotMessageToolbar message={message} />
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