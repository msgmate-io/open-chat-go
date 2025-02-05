import { Button } from "@/components/Button";

export const SendMessageButton = ({ 
        onClick, 
        isLoading 
    }: {
        onClick: () => void,
        isLoading: boolean
    }) => {

    return <Button
        onClick={onClick}
        disabled={isLoading}
        className="ml-2 bg-base-300 text-white p-2 rounded-full flex items-center justify-center"
    >
        <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" /></svg>
    </Button>
}