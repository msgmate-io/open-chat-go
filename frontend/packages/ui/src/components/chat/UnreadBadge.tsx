import { Badge } from "../badge"
import { Icon } from "../icon"

export function UnreadBadge({ unreadCount }: { unreadCount: number }) {
  if (unreadCount <= 0) {
    return null
  }

  return (
    <Badge className="bg-transparent flex flex-row items-center gap-0.5 content-center justify-center text-foreground h-6 min-w-6 px-1 hover:bg-transparent">
      <Icon name="mail" size="xs" />
      <span className="text-xs font-medium">{unreadCount}</span>
    </Badge>
  )
}
