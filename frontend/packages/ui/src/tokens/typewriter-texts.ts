export type TypewriterSlide = {
  model: string;
  prompt: string;
  completion: string;
};

/** Default demo slide shown in Storybook when no texts prop is passed. */
export const typewriterDemoTexts: TypewriterSlide[] = [
  {
    model: "gpt-4o",
    prompt: '"What makes Open-Chat different?"',
    completion:
      "Open-Chat is self-hostable, model-agnostic, and built for bots, APIs, and private AI workflows.",
  },
];

/** Marketing slides used on the Open-Chat landing page hero. */
export const openChatLandingTypewriterTexts: TypewriterSlide[] = [
  {
    model: "cognitivecomputations/dolphin-2.6-mixtral-8x7b",
    prompt: '"Why is open-chat different than other LLM chat interfaces?"',
    completion:
      "Open-chat is different due to its decentralized, self-hostable nature and support for both proprietary and open-source AI backend models. It also offers a full API endpoint for chat-based interactions, unlike many other LLM chat interfaces. This gives users unique features like running LLM completions via GPU at home!",
  },
  {
    model: "gpt-4o",
    prompt: '"Name a thing that open-chat can do that OpenAI\'s chat GPT can\'t."',
    completion:
      "Open-Chat can integrate with and self-host various open-source models, allowing for more customizable and private AI interactions.",
  },
  {
    model: "meta-llama/Meta-Llama-3-8B-Instruct",
    prompt: '"How can open-chat bots be used?"',
    completion:
      "Open-chat bots can be used like regular users, authenticating with the server and joining websocket channels. Bot logic is encapsulated within the bot itself, allowing any bot to serve any purpose, such as processing commands, generating content, or even creating complex conversations.",
  },
  {
    model: "databricks/dbrx-instruct",
    prompt: '"Why should I use open chat if I could just use ChatGpt?"',
    completion:
      "You should use Open Chat if you want more control and flexibility over your AI interactions. Open Chat is self-hostable, compatible with various proprietary services and open-source LLMs, and allows users to run AI bots and LLMs on their own devices or choose from pre-hosted services without exposing your API keys to the world. Also, Open Chat is designed to support any LLM backend or service.",
  },
  {
    model: "llama3-70b-8192",
    prompt: '"What is open-chat\'s audio chat feature?"',
    completion:
      "Open-Chat's audio chat feature allows users to engage in voice conversations with AI bots, offering a more human-like interaction experience.",
  },
];
