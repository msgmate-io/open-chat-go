import * as React from "react";
import { useEffect, useState } from "react";

import { cn } from "../lib/utils";
import { typewriterDemoTexts, type TypewriterSlide } from "../tokens/typewriter-texts";
import { Text, TextTypes } from "./text";

export type { TypewriterSlide };

export interface TypewriterProps {
  texts?: TypewriterSlide[];
  typingSpeed?: number;
  /** Pause after a slide finishes typing, before the exit animation. */
  delay?: number;
  /** Duration of the slide-up exit animation before the next slide. */
  slideDuration?: number;
  className?: string;
  fullHeight?: boolean;
  persistKey?: string;
}

type TypewriterSnapshot = {
  slidesKey: string;
  displayedText: string;
  currentTextIndex: number;
  isBlinking: boolean;
  isTyping: boolean;
  isSliding: boolean;
  model: string;
  prompt: string;
};

const persistedTypewriterState = new Map<string, TypewriterSnapshot>();

function buildSlidesKey(slides: TypewriterSlide[]): string {
  return JSON.stringify(slides);
}

function Typewriter({
  texts = typewriterDemoTexts,
  typingSpeed = 100,
  delay = 2000,
  slideDuration = 1000,
  className,
  fullHeight = false,
  persistKey,
}: TypewriterProps) {
  const slides = texts.length > 0 ? texts : typewriterDemoTexts;
  const slidesKey = buildSlidesKey(slides);
  const initialSnapshot = persistKey ? persistedTypewriterState.get(persistKey) : undefined;
  const canRestore = Boolean(initialSnapshot && initialSnapshot.slidesKey == slidesKey);

  const [displayedText, setDisplayedText] = useState(
    canRestore ? initialSnapshot?.displayedText ?? "" : ""
  );
  const [currentTextIndex, setCurrentTextIndex] = useState(
    canRestore ? initialSnapshot?.currentTextIndex ?? 0 : 0
  );
  const [isBlinking, setIsBlinking] = useState(
    canRestore ? initialSnapshot?.isBlinking ?? false : false
  );
  const [isTyping, setIsTyping] = useState(
    canRestore ? initialSnapshot?.isTyping ?? true : true
  );
  const [isSliding, setIsSliding] = useState(
    canRestore ? initialSnapshot?.isSliding ?? false : false
  );
  const [model, setModel] = useState(
    canRestore ? initialSnapshot?.model ?? (slides[0]?.model ?? "") : (slides[0]?.model ?? "")
  );
  const [prompt, setPrompt] = useState(
    canRestore ? initialSnapshot?.prompt ?? (slides[0]?.prompt ?? "") : (slides[0]?.prompt ?? "")
  );

  useEffect(() => {
    const snapshot = persistKey ? persistedTypewriterState.get(persistKey) : undefined;
    if (snapshot && snapshot.slidesKey == slidesKey) {
      setDisplayedText(snapshot.displayedText);
      setCurrentTextIndex(snapshot.currentTextIndex);
      setIsBlinking(snapshot.isBlinking);
      setIsTyping(snapshot.isTyping);
      setIsSliding(snapshot.isSliding);
      setModel(snapshot.model);
      setPrompt(snapshot.prompt);
      return;
    }
    setDisplayedText("");
    setCurrentTextIndex(0);
    setIsBlinking(false);
    setIsTyping(true);
    setIsSliding(false);
    setModel(slides[0]?.model ?? "");
    setPrompt(slides[0]?.prompt ?? "");
  }, [persistKey, slides, slidesKey]);

  useEffect(() => {
    if (!persistKey) {
      return;
    }
    persistedTypewriterState.set(persistKey, {
      slidesKey,
      displayedText,
      currentTextIndex,
      isBlinking,
      isTyping,
      isSliding,
      model,
      prompt,
    });
  }, [persistKey, slidesKey, displayedText, currentTextIndex, isBlinking, isTyping, isSliding, model, prompt]);

  useEffect(() => {
    let typingInterval: ReturnType<typeof setInterval> | undefined;
    let timeout: ReturnType<typeof setTimeout> | undefined;

    const type = () => {
      setDisplayedText((prev) => {
        const fullText = slides[currentTextIndex]?.completion ?? "";
        const nextText = fullText.substring(0, prev.length + 1);
        if (nextText === fullText) {
          if (typingInterval) clearInterval(typingInterval);
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
        setDisplayedText("");
        const nextTextIndex = (currentTextIndex + 1) % slides.length;
        setModel(slides[nextTextIndex]?.model ?? "");
        setPrompt(slides[nextTextIndex]?.prompt ?? "");
        setCurrentTextIndex(nextTextIndex);
        setIsTyping(true);
      }, slideDuration);
    }

    return () => {
      if (typingInterval) clearInterval(typingInterval);
      if (timeout) clearTimeout(timeout);
    };
  }, [slides, currentTextIndex, isTyping, isSliding, typingSpeed, delay, slideDuration]);

  return (
    <div
      data-slot="typewriter"
      className={cn(
        "typewriter-root",
        fullHeight && "typewriter-root--full-height",
        className
      )}
    >
      <div className={cn("typewriter-slide", isSliding && "typewriter-slide--exit")}>
        <Text type={TextTypes.Body4} tag="div" bold className="w-full">
          {model}
        </Text>
        <Text type={TextTypes.Body4} tag="div" color="muted" className="mt-1 w-full">
          {prompt}
        </Text>
        <Text type={TextTypes.Heading6} tag="h1" className="typewriter-content mt-3">
          {displayedText}
          <span
            aria-hidden
            className={cn("typewriter-cursor", isBlinking && "typewriter-cursor--blink")}
          >
            |
          </span>
        </Text>
      </div>
    </div>
  );
}

export { Typewriter };
