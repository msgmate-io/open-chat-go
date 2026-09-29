export const mockChat = {
  uuid: "chat-demo-1",
  settings: { title: "Design system" },
  partner: {
    first_name: "Alex",
    second_name: "Kim",
    is_online: true,
    is_bot: false,
  },
  newest_message: { text: "Looks good — ship it." },
  latest_message: { text: "Looks good — ship it." },
};

export const mockBotChat = {
  uuid: "chat-demo-2",
  settings: {},
  partner: { is_bot: true, is_online: false },
  newest_message: { text: "How can I help you today?" },
  latest_message: { text: "How can I help you today?" },
};

export const mockChatsListResponse = {
  rows: [mockChat, mockBotChat],
  page: 1,
  total_pages: 1,
  sort: "updated_at",
};

export const mockContactsResponse = {
  rows: [{ name: "bot", contact_token: "hal-bot-token" }],
};
