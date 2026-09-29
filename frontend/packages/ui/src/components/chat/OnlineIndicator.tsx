import { Badge } from "../badge"
import { Icon } from "../icon"

export function OnlineIndicator({ isOnline }: { isOnline: boolean }) {
  return (
    <Badge className="bg-transparent flex items-center content-center justify-center h-6 w-6 hover:bg-transparent">
      <Icon
        name="circle"
        size="xs"
        className={
          isOnline
            ? "fill-success text-success"
            : "fill-muted-foreground/40 text-muted-foreground/40"
        }
        label={isOnline ? "Online" : "Offline"}
      />
    </Badge>
  )
}
