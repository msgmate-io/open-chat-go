import { forwardRef, useEffect, useState } from "react";
import { Card } from "@/components/Card";
import { Textarea } from "@/components/Textarea";
import { SendMessageButton } from "@/components/chat/SendMessageButton";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger
} from "@/components/DropdownMenu";
import { Button } from "@/components/Button";
import imgSrc from "@/assets/logo.png"

export const ToggleInputModeButton = () => {
    const [open, setOpen] = useState(false);

    return <DropdownMenu open={open} onOpenChange={setOpen}>
        <DropdownMenuTrigger className=""></DropdownMenuTrigger>
        {<img className="h-9 m-3 hover:ring-base-100 rounded-full ring-2 ring-base-300 dark:ring-gray-500" src={imgSrc} alt="Bordered avatar" onClick={() => {
            setOpen(!open);
        }} />}
        <DropdownMenuContent className="w-56 pointer-events-none border-0 shadow-xl">
            <DropdownMenuLabel className="h-6">Chat Settings</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>
                hello
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
        </DropdownMenuContent>
    </DropdownMenu>

}

interface MessageViewInputProps {
    isLoading?: boolean,
    onSendMessage?: () => void,
    isBotResponding?: boolean,
    stopBotResponse?: () => void,
    text: string,
    setText: (text: string) => void,
    maxHeight?: number,
    minHeight?: number
}

export const CancelResponseButton = ({ onClick }: { onClick: () => void }) => {
    return <Button
        onClick={onClick}
        className="ml-2 bg-base-300 text-white p-2 rounded-full flex items-center justify-center"
    >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
    </Button>
}

export const MessageInput = forwardRef<
    HTMLTextAreaElement,
    MessageViewInputProps
>(({
    text, setText,
    isLoading = false,
    onSendMessage = () => { },
    isBotResponding = false,
    stopBotResponse = () => { },
    maxHeight = 300,
    minHeight = 30
}, ref: any) => {


    useEffect(() => {
        if (ref?.current) {
            const scrollHeight = ref.current.scrollHeight;
            let updatedHeight = Math.max(minHeight, Math.min(scrollHeight, maxHeight));
            if (text != "") {
                ref.current.style.height = 'inherit';
                ref.current.style.height = `${updatedHeight}px`;
                ref.current.style.overflowY = scrollHeight > maxHeight ? 'auto' : 'hidden';
            }
        }
    }, [ref, text, maxHeight, minHeight]);

    const handleTextChange = (e: any) => {
        setText(e.target.value);
    };

    const handleKeyPress = (e: any) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSendMessage();
        }
    };

    const resetInput = () => {
        if (ref?.current) {
            ref.current.style.height = 'auto';
            ref.current.style.height = `${minHeight}px`;
            ref.current.style.overflowY = 'hidden';
        }
    }

    useEffect(() => {
        // if ther is no newline character in the text, reset the input
        if (!text.includes('\n'))
            resetInput();
    }, [text]);

    const handleSendMessage = () => {
        onSendMessage();
        setText(''); // Clear the input text
        resetInput();
    };

    return <div className='flex flex-col content-center items-center justify-center'>
        <Card className="bg-base-200 pr-4 md:px-4 flex items-center rounded-3xl border-0 max-w-[900px] md:min-w-[800px] mb-2" key={"chatListHeader"}>
            <div className="flex pr-4">
                <ToggleInputModeButton />
            </div>
            <Textarea
                value={text}
                placeholder="Send message to Msgmate.io"
                onChange={handleTextChange}
                onKeyPress={handleKeyPress}
                className={`bg-base-200 rounded-2xl text-lg resize-none border-0 focus:border-0 outline-none focus:outline-none shadow-none focus:shadow-none h-[${minHeight}px]`}
                style={{
                    overflowY: 'hidden',
                    height: `${minHeight}px`,
                    maxHeight: `${maxHeight}px`,
                    minHeight: `${minHeight}px`
                }}
                ref={ref}
            />
            {!isBotResponding ? <SendMessageButton onClick={handleSendMessage} isLoading={isLoading} /> : <CancelResponseButton onClick={stopBotResponse} />}
        </Card>
        <div className='flex grow items-center content-center justify-center text-sm hidden md:flex'>
            msgmate.io uses magic, be sceptical and verify information!
        </div>
    </div>
});