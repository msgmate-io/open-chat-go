import React, { useContext } from 'react';
import logoUrl from "@/assets/logo.png"

const CHAT_INTROS = [
    {
        title: "Let Hal Mixtral write you a simple script",
        description: "Hey Hal, can you write me a simple script to automate my daily tasks?"
    },
    {
        title: "Chat with Bots",
        description: "Explore and chat with bots. You can also create your own bot and share it with others."
    },
    {
        title: "Chat with Users",
        description: "Start chatting with other users. You can also create your own bot and share it with others."
    }
]

export function NewBotChatCard() {
    return <>
        <div className='flex flex-col relative w-full h-full content-center items-center justify-center'>
            <img
                src={logoUrl}
                className="w-[100px] md:w-[200px] lg:w-[300px] object-contain"
                alt="About services"
            />
            <div className="flex content-center items-center justify-center w-full gap-2">
                {CHAT_INTROS.map((intro, i) => <div key={i} className="flex flex-col w-full p-2 h-[120px] border-content border-[0px] rounded-2xl hover:bg-base-200">
                    <h2 className="text-sm font-bold py-2">
                        {intro.title}
                    </h2>
                    <p className="text-sm">
                        {intro.description}
                    </p>
                </div>)}
            </div>
        </div>
    </>
}
