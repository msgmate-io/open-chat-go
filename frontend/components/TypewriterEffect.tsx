import React, { useEffect, useState } from 'react';

const TEXTS = [{
    model: "gpt-4o",
    prompt: "Was",
    completion: "a great day"
}]

export const Typewriter = ({ texts = TEXTS, typingSpeed = 100, delay = 2000 }) => {
    const [displayedText, setDisplayedText] = useState('');
    const [currentTextIndex, setCurrentTextIndex] = useState(0);
    const [isBlinking, setIsBlinking] = useState(false);
    const [isTyping, setIsTyping] = useState(true);
    const [isSliding, setIsSliding] = useState(false);
    const [model, setModel] = useState(TEXTS[0].model);
    const [prompt, setPrompt] = useState(TEXTS[0].prompt);

    useEffect(() => {
        let typingInterval;
        let timeout;

        const type = () => {
            setDisplayedText((prev) => {
                const fullText = texts[currentTextIndex].completion;
                const nextText = fullText.substring(0, prev.length + 1);
                if (nextText === fullText) {
                    clearInterval(typingInterval);
                    setIsBlinking(true);
                    timeout = setTimeout(() => {
                        setIsTyping(false);
                        setIsBlinking(false);
                        setIsSliding(true);
                    }, delay);
                }
                return nextText;
            });
        };

        if (isTyping) {
            typingInterval = setInterval(type, typingSpeed);
        } else if (isSliding) {
            timeout = setTimeout(() => {
                setIsSliding(false);
                setDisplayedText('');
                const nextTextIndex = (currentTextIndex + 1) % texts.length;
                setModel(texts[nextTextIndex].model);
                setPrompt(texts[nextTextIndex].prompt);
                setCurrentTextIndex((prev) => (prev + 1) % texts.length);

                setIsTyping(true);
            }, 1000); // Duration for the slide-up effect
        }

        return () => {
            clearInterval(typingInterval);
            clearTimeout(timeout);
        };
    }, [texts, currentTextIndex, isTyping, isSliding, typingSpeed, delay, model, prompt]);

    return (
        <div className="typewriter-container w-[800px]">
            <div className={`typewriter ${isSliding ? 'slide-up' : ''} w-full`}>
                <div className="typewriter-text font-bold text-lg">
                    {model}
                </div>
                <div className="typewriter-text text-lg">
                    {prompt}
                </div>
                <h1 className="typewriter-content text-xl">
                    {displayedText}
                    <span className={`typewriter-cursor ${isBlinking ? 'blink' : ''}`}>|</span>
                </h1>
            </div>
        </div>
    );
};