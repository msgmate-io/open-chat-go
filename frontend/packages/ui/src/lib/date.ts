export function isToday(date: Date) {
  const today = new Date();
  return (
    date.getDate() === today.getDate() &&
    date.getMonth() === today.getMonth() &&
    date.getFullYear() === today.getFullYear()
  );
}

export function isYesterday(date: Date) {
  const yesterday = new Date();
  yesterday.setDate(yesterday.getDate() - 1);
  return (
    date.getDate() === yesterday.getDate() &&
    date.getMonth() === yesterday.getMonth() &&
    date.getFullYear() === yesterday.getFullYear()
  );
}

export function isWithinLast7Days(date: Date) {
  const sevenDaysAgo = new Date();
  sevenDaysAgo.setDate(sevenDaysAgo.getDate() - 7);
  return date >= sevenDaysAgo;
}

export type ChatTimeBucket = {
  /** Stable key used to merge consecutive rows into one divider. */
  key: string;
  /** Human readable divider label, e.g. "Today", "Last 3 hours", "Monday". */
  label: string;
};

const startOfDay = (date: Date) =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate());

const shortWeekday = (date: Date) =>
  date.toLocaleDateString(undefined, { weekday: "short" });

const monthLabel = (date: Date) =>
  date.toLocaleDateString(undefined, { month: "long" });

/**
 * Buckets a chat's most recent activity into a fine-grained, chat-app style
 * time divider. Recent activity is grouped by hours, then by calendar day,
 * weekday, week, month and finally year so long-lived lists stay readable.
 */
export function getChatTimeBucket(
  date: Date,
  now: Date = new Date()
): ChatTimeBucket {
  const diffMs = now.getTime() - date.getTime();
  const diffMinutes = diffMs / 60000;
  const diffDays = Math.round(
    (startOfDay(now).getTime() - startOfDay(date).getTime()) / 86400000
  );

  if (diffMinutes < 60) {
    return { key: "last_hour", label: "Last hour" };
  }
  if (diffMinutes < 180) {
    return { key: "last_3_hours", label: "Last 3 hours" };
  }
  if (diffMinutes < 360) {
    return { key: "last_6_hours", label: "Last 6 hours" };
  }
  if (diffDays <= 0) {
    return { key: "today", label: "Today" };
  }
  if (diffDays === 1) {
    return { key: "yesterday", label: "Yesterday" };
  }
  if (diffDays < 7) {
    return {
      key: `weekday_${date.getDay()}`,
      label: shortWeekday(date),
    };
  }
  if (diffDays < 14) {
    return { key: "last_week", label: "Last week" };
  }
  if (
    date.getMonth() === now.getMonth() &&
    date.getFullYear() === now.getFullYear()
  ) {
    return { key: "this_month", label: "This month" };
  }
  if (date.getFullYear() === now.getFullYear()) {
    return { key: `month_${date.getMonth()}`, label: monthLabel(date) };
  }
  return { key: `year_${date.getFullYear()}`, label: String(date.getFullYear()) };
}
