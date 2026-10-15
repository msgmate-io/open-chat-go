/**
 * Message grouping for the chat transcript.
 *
 * A chat-backend integration can opt into merging consecutive assistant turns
 * into a single bubble (see `ChatUIExtension.groupConsecutiveMessages`). The
 * msgmate coder uses it because one interaction pauses after every confirmed
 * mutating tool call, producing a run of back-to-back assistant messages that
 * would otherwise each render their own repeated sender header.
 *
 * The grouping is pure so it can be unit-tested independently of React.
 */

export interface GroupableMessage {
  uuid: string;
  sender_uuid?: string;
  sender_is_automated?: boolean;
  data_type?: string;
  meta_data?: { event_type?: string } | null;
}

export type MessageGroupNode<T> =
  | { kind: "single"; key: string; message: T; rerunSourceMessageUUID?: string }
  | { kind: "group"; key: string; messages: T[]; rerunSourceMessageUUID?: string };

export interface BuildMessageGroupsOptions {
  /** Merge consecutive assistant messages from the same sender into one group. */
  groupConsecutive?: boolean;
  /**
   * Treat `confirmable_action_execute` lifecycle events as transparent: they do
   * not break a group and are not rendered. Used when the extension renders
   * those lifecycles inline and the core already hides their bubbles.
   */
  suppressConfirmableEvents?: boolean;
}

function isSuppressedEvent(message: GroupableMessage, suppress: boolean): boolean {
  if (!suppress) {
    return false;
  }
  return (
    String(message?.data_type || "") === "event" &&
    message?.meta_data?.event_type === "confirmable_action_execute"
  );
}

/**
 * Builds the ordered list of transcript nodes from rows supplied oldest-first.
 * Every node carries the `rerunSourceMessageUUID` (the last user message before
 * it) so the regenerate action can target the right turn.
 */
export function buildMessageGroups<T extends GroupableMessage>(
  messagesOldestFirst: T[],
  options: BuildMessageGroupsOptions = {},
): MessageGroupNode<T>[] {
  const { groupConsecutive = false, suppressConfirmableEvents = false } = options;
  const nodes: MessageGroupNode<T>[] = [];
  let currentGroup: T[] = [];
  let currentGroupRerun: string | undefined;
  let lastUserMessageUUID: string | undefined;

  const flushGroup = () => {
    if (currentGroup.length === 0) {
      return;
    }
    if (currentGroup.length === 1) {
      nodes.push({
        kind: "single",
        key: `msg_${currentGroup[0].uuid}`,
        message: currentGroup[0],
        rerunSourceMessageUUID: currentGroupRerun,
      });
    } else {
      nodes.push({
        kind: "group",
        key: `group_${currentGroup[0].uuid}`,
        messages: currentGroup,
        rerunSourceMessageUUID: currentGroupRerun,
      });
    }
    currentGroup = [];
    currentGroupRerun = undefined;
  };

  for (const message of messagesOldestFirst) {
    if (isSuppressedEvent(message, suppressConfirmableEvents)) {
      continue;
    }

    const isAutomatedSender = Boolean(message?.sender_is_automated);
    const isEventMessage = String(message?.data_type || "") === "event";

    let rerunSourceMessageUUID: string | undefined;
    if (isAutomatedSender) {
      rerunSourceMessageUUID = lastUserMessageUUID;
    } else if (!isEventMessage) {
      lastUserMessageUUID = message.uuid;
    }

    const canGroup = groupConsecutive && isAutomatedSender && !isEventMessage;
    if (canGroup) {
      if (
        currentGroup.length > 0 &&
        currentGroup[currentGroup.length - 1].sender_uuid !== message.sender_uuid
      ) {
        flushGroup();
      }
      if (currentGroup.length === 0) {
        currentGroupRerun = rerunSourceMessageUUID;
      }
      currentGroup.push(message);
      continue;
    }

    flushGroup();
    nodes.push({
      kind: "single",
      key: `msg_${message.uuid}`,
      message,
      rerunSourceMessageUUID,
    });
  }

  flushGroup();
  return nodes;
}
