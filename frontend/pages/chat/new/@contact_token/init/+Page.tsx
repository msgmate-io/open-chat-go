import React, { useEffect, useMemo, useState } from "react";
import { usePageContext } from "vike-react/usePageContext";
import { navigate } from "vike/client/router";
import useSWR from "swr";
import { BotSelector, Button, LoadingSpinner, Text, TextTypes } from "@open-chat-go/ui";
import { ChatBase } from "@open-chat-go/ui";
import { fetcher } from "@/lib/utils";
import { ToolInitFields } from "@/components/chat/ToolInitFields";
import {
  asToolInitMap,
  getMissingRequiredToolInitFields,
  pickToolInitForTools,
  resolveRequiredToolInitDescriptors,
} from "@/lib/tool-init";
import { getPendingToolInit, setPendingToolInit } from "@/lib/chat-tool-init-store";

type BotModel = {
  title: string;
  description?: string;
  configuration?: Record<string, unknown>;
};

type ContactResponse = {
  name?: string;
  is_automated?: boolean;
  profile_data?: {
    models?: BotModel[];
  };
};

type ToolsResponse = {
  rows: Array<{
    name: string;
    requires_init?: boolean;
    init_schema?: Record<string, unknown>;
  }>;
};

function asStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.map((item) => String(item || "").trim()).filter(Boolean);
}

export default function Page() {
  const pageContext = usePageContext();
  const contactToken = String(pageContext.routeParams.contact_token || "").trim();

  const { data: contact, isLoading } = useSWR<ContactResponse>(
    contactToken ? `/api/v1/contacts/${contactToken}` : null,
    fetcher,
  );
  const { data: toolsData } = useSWR<ToolsResponse>("/api/v1/tools?page=1&page_size=400", fetcher);

  const [selectedModel, setSelectedModel] = useState("");
  const [toolInitData, setToolInitData] = useState<Record<string, Record<string, unknown>>>({});
  const [errorText, setErrorText] = useState("");

  const botModels = contact?.profile_data?.models ?? [];
  const selectedModelConfig = useMemo(
    () => botModels.find((model) => model.title === selectedModel)?.configuration ?? {},
    [botModels, selectedModel],
  );
  const selectedTools = useMemo(
    () => asStringArray(selectedModelConfig.tools),
    [selectedModelConfig],
  );
  const requiredToolInitDescriptors = useMemo(
    () => resolveRequiredToolInitDescriptors(selectedTools, toolsData?.rows ?? []),
    [selectedTools, toolsData?.rows],
  );

  useEffect(() => {
    if (!selectedModel && botModels.length > 0) {
      setSelectedModel(botModels[0]?.title ?? "");
    }
  }, [botModels, selectedModel]);

  useEffect(() => {
    if (!selectedModel) {
      return;
    }
    const fromStore = asToolInitMap(getPendingToolInit(contactToken, selectedModel));
    const selectedOnly = pickToolInitForTools(requiredToolInitDescriptors, fromStore);
    for (const descriptor of requiredToolInitDescriptors) {
      if (!selectedOnly[descriptor.configuredName]) {
        selectedOnly[descriptor.configuredName] = {};
      }
    }
    setToolInitData(selectedOnly);
  }, [contactToken, selectedModel, requiredToolInitDescriptors]);

  const onNext = () => {
    setErrorText("");
    if (!selectedModel) {
      setErrorText("Please select a model.");
      return;
    }

    if (requiredToolInitDescriptors.length > 0) {
      const missing = getMissingRequiredToolInitFields(requiredToolInitDescriptors, toolInitData);
      if (missing.length > 0) {
        setErrorText(`Missing required tool init fields: ${missing.join(", ")}`);
        return;
      }

      const payload = pickToolInitForTools(requiredToolInitDescriptors, toolInitData);
      setPendingToolInit(contactToken, selectedModel, payload);
    }

    navigate(`/chat/new/${contactToken}?init_filled=1`);
  };

  const formScrollInset =
    "max(var(--openchat-keyboard-bottom, 0px), var(--openchat-keyboard-bottom-visual, 0px), var(--openchat-safe-bottom, 0px))";

  const handleFormFocusCapture = (event: React.FocusEvent<HTMLDivElement>) => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const isMobileRuntime =
      typeof document !== "undefined" &&
      document.documentElement.getAttribute("data-openchat-runtime") === "mobile";
    if (!isMobileRuntime) {
      return;
    }
    const tag = target.tagName.toLowerCase();
    const isInputLike = tag === "input" || tag === "textarea" || tag === "select" || target.isContentEditable;
    if (!isInputLike) {
      return;
    }
    window.setTimeout(() => {
      target.scrollIntoView({ block: "center", inline: "nearest" });
    }, 120);
  };

  if (isLoading) {
    return (
      <ChatBase chatUUID={null} hideMobileShortcut navigateTo={(to: string) => navigate(to)}>
        <div className="flex h-full items-center justify-center">
          <LoadingSpinner size={48} />
        </div>
      </ChatBase>
    );
  }

  const isBotContact = contact?.is_automated === true;

  return (
    <ChatBase chatUUID={null} hideMobileShortcut navigateTo={(to: string) => navigate(to)}>
      <div
        className="mx-auto flex h-full min-h-0 w-full max-w-4xl flex-col gap-4 overflow-y-auto px-4 pt-6 md:px-6"
        style={{
          paddingBottom: `calc(${formScrollInset} + 1.5rem)`,
          scrollPaddingBottom: `calc(${formScrollInset} + 1rem)`,
        }}
        onFocusCapture={handleFormFocusCapture}
      >
        <div className="rounded-lg border border-border/70 bg-card p-4">
          <Text type={TextTypes.Heading5} tag="h1" bold>
            Initialize tool parameters
          </Text>
          <Text type={TextTypes.Body6} color="muted">
            Configure required tool init values before starting an interaction with {contact?.name || "this bot"}.
          </Text>
        </div>

        {!isBotContact ? (
          <div className="rounded-lg border border-border/70 bg-card p-4">
            <Text type={TextTypes.Body6}>This contact is not a bot. No tool init is required.</Text>
            <div className="mt-3">
              <Button onClick={() => navigate(`/chat/new/${contactToken}`)}>Continue</Button>
            </div>
          </div>
        ) : (
          <div className="space-y-4 rounded-lg border border-border/70 bg-card p-4">
            <div>
              <Text type={TextTypes.Body6} bold>Model</Text>
              <div className="mt-2">
                <BotSelector
                  contact={contact}
                  selectedModel={selectedModel || botModels[0]?.title || ""}
                  setSelectedModel={setSelectedModel}
                  defaultCollapsed={false}
                />
              </div>
            </div>

            <div>
              <Text type={TextTypes.Body6} bold>Required init fields</Text>
              <div className="mt-2">
                <ToolInitFields
                  descriptors={requiredToolInitDescriptors}
                  value={toolInitData}
                  onChange={setToolInitData}
                  emptyHint="This model has no tools requiring init values."
                />
              </div>
            </div>

            {errorText ? <Text type={TextTypes.Body6} color="destructive">{errorText}</Text> : null}

            <div className="flex flex-wrap gap-2">
              <Button onClick={onNext}>Next</Button>
              <Button variant="outline" onClick={() => navigate(`/chat/new/${contactToken}`)}>Back</Button>
            </div>
          </div>
        )}
      </div>
    </ChatBase>
  );
}
