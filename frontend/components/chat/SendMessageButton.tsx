import { Button } from "@/components/Button";

export const SendMessageButton = ({ 
        onClick, 
        isLoading 
    }: {
        onClick: () => void,
        isLoading: boolean
    }) => {

    return <Button
        variant="ghost"
        onClick={onClick}
        disabled={isLoading}
        className="bg-background text-foreground w-12 h-12 rounded-full flex items-center justify-center p-0"
    >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-6 w-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" /></svg>
    </Button>
}