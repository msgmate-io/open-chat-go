import Markdown from "react-markdown"
import logoUrl from "@/assets/logo.png"
import Prism from 'prismjs'
import { useEffect } from 'react'
import { cn } from "@/lib/utils"
import { useThemeStore } from "@/components/ThemeToggle"
// Import prism themes - choose one you prefer
//import 'prismjs/themes/prism-tomorrow.css'
// Import additional languages you want to support
import 'prismjs/components/prism-python'
import 'prismjs/components/prism-javascript'
import 'prismjs/components/prism-typescript'
import 'prismjs/components/prism-bash'
import 'prismjs/components/prism-json'
import React from 'react'

// Import all themes we want to support
import 'prismjs/themes/prism-tomorrow.css'
import 'prismjs/themes/prism-twilight.css'
import 'prismjs/themes/prism-okaidia.css'
import 'prismjs/themes/prism-solarizedlight.css'

// Create a style element to manage active theme
const createThemeStyle = () => {
  const style = document.createElement('style')
  style.setAttribute('data-prism-theme', 'active')
  document.head.appendChild(style)
  return style
}

// Function to switch between themes
const loadPrismTheme = (theme: string) => {
  let style = document.querySelector('style[data-prism-theme="active"]') as HTMLStyleElement
  if (!style) {
    style = createThemeStyle()
  }

  // Disable all themes first
  style.textContent = `
    pre[class*="language-"],
    code[class*="language-"] {
      all: revert;
    }
  `

  // Enable selected theme
  switch (theme) {
    case 'dark':
      style.textContent += `
        pre[class*="language-"],
        code[class*="language-"] {
          background: #2d2d2d !important;
          color: #ccc !important;
        }
        /* Copy theme-specific token styles from prism-tomorrow.css */
        .token.comment,
        .token.block-comment,
        .token.prolog,
        .token.doctype,
        .token.cdata {
          color: #999 !important;
        }
        .token.punctuation {
          color: #ccc !important;
        }
        .token.tag,
        .token.attr-name,
        .token.namespace,
        .token.deleted {
          color: #e2777a !important;
        }
        .token.function-name {
          color: #6196cc !important;
        }
        /* Add more token styles as needed */
      `
      break
    case 'light':
      style.textContent += `
        pre[class*="language-"],
        code[class*="language-"] {
          background: #fff !important;
          color: #000 !important;
        }
        /* Copy theme-specific token styles from prism default theme */
        .token.comment,
        .token.prolog,
        .token.doctype,
        .token.cdata {
          color: #998 !important;
          font-style: italic !important;
        }
        .token.function,
        .token.class-name {
          color: #900 !important;
        }
        /* Add more token styles as needed */
      `
      break
    // Add more themes as needed
    default:
      style.textContent += `
        pre[class*="language-"],
        code[class*="language-"] {
          background: #2d2d2d !important;
          color: #ccc !important;
        }
        /* Default theme token styles */
      `
  }
}

// Add this custom wrapper component for code blocks
const CodeWrapper = ({children}: {children: any}) => {
  // This ensures code blocks are rendered properly outside of paragraphs
  const codeBlock = React.Children.toArray(children).find(
    (child: any) => child?.props?.className?.includes('language-')
  )
  
  if (codeBlock) {
    return <div className="relative">{children}</div>
  }
  
  return <div className="">{children}</div>
}

// Add this custom code block component
const CodeBlock = ({className, children}: {className?: string, children: any}) => {
  const theme = useThemeStore(state => state.theme)
  const language = className?.replace('language-', '') || 'text'
  
  useEffect(() => {
    // Load appropriate theme when component mounts or theme changes
    loadPrismTheme(theme)
    
    if (typeof children === 'string' && children.includes('\n')) {
      Prism.highlightElement(document.querySelector(`code.language-${language}`))
    }
  }, [children, language, theme])

  const handleCopy = () => {
    const code = typeof children === 'string' ? children : children.toString()
    navigator.clipboard.writeText(code)
  }

  // Check if this is a multiline code block
  const isMultiLine = typeof children === 'string' && children.includes('\n')

  return (
    isMultiLine ?
    <pre className="relative light:bg-secondary/50 dark:bg-secondary/30 p-4 max-w-full rounded-lg overflow-x-auto my-4">
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

export function MessageMarkdown({children}: {children: any}) {
    return <Markdown components={{
        strong: ({children, ...props}) => <strong className="text-foreground" {...props}>{children}</strong>,
        a: ({children, ...props}) => <MessageLink props={props}>{children}</MessageLink>,
        p: ({children, ...props}) => <>{children}</>,
        code: ({children, ...props}) => <CodeBlock {...props}>{children}</CodeBlock>,
        pre: ({children, ...props}) => <CodeWrapper {...props}>{children}</CodeWrapper>
    }}>{children}</Markdown>
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
        return <div key={message.uuid} className="flex flex-row w-full relativ max-w-full">
            <div className="flex p-2 hidden md:flex">
                <img alt="logo" className="h-9 w-9 m-2 rounded-full ring-2 ring-base-300 dark:ring-gray-500 filter grayscale" src={logoUrl} />
            </div>
            <div className="w-full flex flex-col flex-grow relative">
                <div className="article prose w-[90%] pt-3 pl-1 overflow-x-auto text-foreground">
                    <MessageMarkdown>
                        {message.text}
                    </MessageMarkdown>
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