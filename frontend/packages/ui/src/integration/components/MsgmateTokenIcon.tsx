import { useId } from "react";
import { cn } from "./utils";

// MsgmateTokenIcon is the small, self-contained indicator for "msgmate tokens".
// It is abstracted from the msgmate logo: a round amber-to-pink coin carrying a
// hash glyph. It is dark-mode safe because the gradient is self-contained and
// the glyph reads against the coin rather than the page background.
export const MsgmateTokenIcon = ({
    size = 20,
    className,
    title = "Msgmate tokens",
}: {
    size?: number;
    className?: string;
    title?: string;
}) => {
    const gradientId = useId();

    return (
        <svg
            xmlns="http://www.w3.org/2000/svg"
            width={size}
            height={size}
            viewBox="0 0 24 24"
            role="img"
            aria-label={title}
            className={cn("inline-block shrink-0", className)}
        >
            <title>{title}</title>
            <defs>
                <linearGradient id={gradientId} x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0%" stopColor="#F59E0B" />
                    <stop offset="100%" stopColor="#EC4899" />
                </linearGradient>
            </defs>
            <circle cx="12" cy="12" r="11" fill={`url(#${gradientId})`} />
            <circle cx="12" cy="12" r="8.5" fill="none" stroke="#ffffff" strokeOpacity="0.35" strokeWidth="0.75" />
            <g fill="none" stroke="#ffffff" strokeWidth="1.5" strokeLinecap="round">
                <line x1="9.75" y1="8" x2="8.75" y2="16" />
                <line x1="14.25" y1="8" x2="13.25" y2="16" />
                <line x1="8" y1="10.5" x2="16.5" y2="10.5" />
                <line x1="7.5" y1="13.5" x2="16" y2="13.5" />
            </g>
        </svg>
    );
};
