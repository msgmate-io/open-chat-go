import React, { useEffect, useMemo, useState } from "react";
import { Button, Input, Text, TextTypes, Textarea, resolveToolInitWidget } from "@open-chat-go/ui";
import type { ToolInitDescriptor } from "../lib/tool-init";

type ToolInitMap = Record<string, Record<string, unknown>>;

function toStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => String(item || "").trim())
    .filter(Boolean);
}

function isObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function getPropertyType(schema: Record<string, unknown>): string {
  const raw = schema.type;
  if (typeof raw === "string") {
    return raw;
  }
  return "string";
}

type ToolInitFieldsProps = {
  descriptors: ToolInitDescriptor[];
  value: ToolInitMap;
  onChange: (next: ToolInitMap) => void;
  emptyHint?: string;
};

export function ToolInitFields({ descriptors, value, onChange, emptyHint }: ToolInitFieldsProps) {
  const [jsonDrafts, setJsonDrafts] = useState<Record<string, string>>({});
  const [jsonErrors, setJsonErrors] = useState<Record<string, string>>({});
  const [numberDrafts, setNumberDrafts] = useState<Record<string, string>>({});

  useEffect(() => {
    setJsonDrafts((current) => {
      const next: Record<string, string> = {};
      for (const descriptor of descriptors) {
        const key = descriptor.configuredName;
        next[key] = current[key] ?? JSON.stringify(value[key] ?? {}, null, 2);
      }
      return next;
    });
  }, [descriptors, value]);

  const requiredByTool = useMemo(() => {
    const out: Record<string, Set<string>> = {};
    for (const descriptor of descriptors) {
      out[descriptor.configuredName] = new Set(
        toStringArray((descriptor.schema as Record<string, unknown>).required),
      );
    }
    return out;
  }, [descriptors]);

  const updateToolValue = (toolName: string, updater: (current: Record<string, unknown>) => Record<string, unknown>) => {
    const currentForTool = isObject(value[toolName]) ? value[toolName] : {};
    const nextForTool = updater(currentForTool);
    onChange({
      ...value,
      [toolName]: nextForTool,
    });
  };

  if (descriptors.length === 0) {
    return (
      <div className="rounded-lg border border-border/70 bg-muted/20 p-3 text-sm text-muted-foreground">
        {emptyHint || "No selected tools require initialization."}
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {descriptors.map((descriptor) => {
        const toolName = descriptor.configuredName;
        const schema = isObject(descriptor.schema) ? descriptor.schema : {};
        const properties = isObject(schema.properties) ? schema.properties : {};
        const hasExplicitProperties = Object.keys(properties).length > 0;
        const requiredFields = requiredByTool[toolName] ?? new Set<string>();
        const currentValue = isObject(value[toolName]) ? value[toolName] : {};

        return (
          <div key={toolName} className="rounded-lg border border-border/70 bg-card p-3">
            <div className="mb-2">
              <Text type={TextTypes.Body6} bold>{toolName}</Text>
              <Text type={TextTypes.Body7} color="muted">
                Configure init payload for this tool.
              </Text>
            </div>

            {(() => {
              const CustomWidget = resolveToolInitWidget(descriptor.ui?.widget);
              if (!CustomWidget) {
                return null;
              }
              return (
                <CustomWidget
                  toolName={toolName}
                  value={currentValue}
                  onChange={(nextValue) => updateToolValue(toolName, () => ({ ...nextValue }))}
                  helpText={descriptor.ui?.helpText}
                />
              );
            })()}

            {descriptor.ui?.widget && resolveToolInitWidget(descriptor.ui.widget) ? null : hasExplicitProperties ? (
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                {Object.entries(properties).map(([fieldName, fieldSchemaRaw]) => {
                  const fieldSchema = isObject(fieldSchemaRaw) ? fieldSchemaRaw : {};
                  const fieldType = getPropertyType(fieldSchema);
                  const fieldDescription = String(fieldSchema.description || "").trim();
                  const enumValues = Array.isArray(fieldSchema.enum) ? fieldSchema.enum : [];
                  const fieldKey = `${toolName}::${fieldName}`;
                  const isRequired = requiredFields.has(fieldName);
                  const rawValue = currentValue[fieldName];

                  if (enumValues.length > 0) {
                    const current = rawValue == null ? "" : String(rawValue);
                    return (
                      <div key={fieldKey} className="space-y-1">
                        <Text type={TextTypes.Body7} bold>
                          {fieldName}{isRequired ? " *" : ""}
                        </Text>
                        <select
                          className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm"
                          value={current}
                          onChange={(event) => {
                            const selected = event.target.value;
                            updateToolValue(toolName, (currentObject) => {
                              const nextObject = { ...currentObject };
                              if (!selected) {
                                delete nextObject[fieldName];
                              } else {
                                nextObject[fieldName] = selected;
                              }
                              return nextObject;
                            });
                          }}
                        >
                          <option value="">Select...</option>
                          {enumValues.map((enumValue, idx) => (
                            <option value={String(enumValue)} key={`${fieldKey}::enum::${idx}`}>
                              {String(enumValue)}
                            </option>
                          ))}
                        </select>
                        {fieldDescription ? <Text type={TextTypes.Body7} color="muted">{fieldDescription}</Text> : null}
                      </div>
                    );
                  }

                  if (fieldType === "boolean") {
                    const checked = Boolean(rawValue);
                    return (
                      <label key={fieldKey} className="flex items-center gap-2 rounded border border-border/60 p-2">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={(event) => {
                            updateToolValue(toolName, (currentObject) => ({
                              ...currentObject,
                              [fieldName]: event.target.checked,
                            }));
                          }}
                        />
                        <div>
                          <Text type={TextTypes.Body7} bold>
                            {fieldName}{isRequired ? " *" : ""}
                          </Text>
                          {fieldDescription ? <Text type={TextTypes.Body7} color="muted">{fieldDescription}</Text> : null}
                        </div>
                      </label>
                    );
                  }

                  if (fieldType === "number" || fieldType === "integer") {
                    const currentDraft = numberDrafts[fieldKey] ?? (rawValue == null ? "" : String(rawValue));
                    return (
                      <div key={fieldKey} className="space-y-1">
                        <Text type={TextTypes.Body7} bold>
                          {fieldName}{isRequired ? " *" : ""}
                        </Text>
                        <Input
                          type="number"
                          step={fieldType === "integer" ? "1" : "any"}
                          value={currentDraft}
                          onChange={(event) => {
                            const nextDraft = event.target.value;
                            setNumberDrafts((current) => ({ ...current, [fieldKey]: nextDraft }));
                            updateToolValue(toolName, (currentObject) => {
                              const nextObject = { ...currentObject };
                              if (!nextDraft.trim()) {
                                delete nextObject[fieldName];
                                return nextObject;
                              }
                              const parsed = Number(nextDraft);
                              if (!Number.isFinite(parsed)) {
                                return nextObject;
                              }
                              if (fieldType === "integer" && !Number.isInteger(parsed)) {
                                return nextObject;
                              }
                              nextObject[fieldName] = parsed;
                              return nextObject;
                            });
                          }}
                          placeholder={fieldType === "integer" ? "42" : "3.14"}
                        />
                        {fieldDescription ? <Text type={TextTypes.Body7} color="muted">{fieldDescription}</Text> : null}
                      </div>
                    );
                  }

                  if (fieldType === "object" || fieldType === "array") {
                    const asString = rawValue == null ? "" : JSON.stringify(rawValue, null, 2);
                    return (
                      <div key={fieldKey} className="space-y-1 md:col-span-2">
                        <Text type={TextTypes.Body7} bold>
                          {fieldName}{isRequired ? " *" : ""}
                        </Text>
                        <Textarea
                          value={asString}
                          onChange={(event) => {
                            const rawText = event.target.value;
                            updateToolValue(toolName, (currentObject) => {
                              const nextObject = { ...currentObject };
                              if (!rawText.trim()) {
                                delete nextObject[fieldName];
                                return nextObject;
                              }
                              try {
                                nextObject[fieldName] = JSON.parse(rawText);
                              } catch {
                                return nextObject;
                              }
                              return nextObject;
                            });
                          }}
                          className="min-h-24 font-mono text-xs"
                          placeholder={fieldType === "array" ? "[]" : "{}"}
                        />
                        {fieldDescription ? <Text type={TextTypes.Body7} color="muted">{fieldDescription}</Text> : null}
                      </div>
                    );
                  }

                  return (
                    <div key={fieldKey} className="space-y-1">
                      <Text type={TextTypes.Body7} bold>
                        {fieldName}{isRequired ? " *" : ""}
                      </Text>
                      <Input
                        value={rawValue == null ? "" : String(rawValue)}
                        onChange={(event) => {
                          const nextValue = event.target.value;
                          updateToolValue(toolName, (currentObject) => {
                            const nextObject = { ...currentObject };
                            if (!nextValue.trim()) {
                              delete nextObject[fieldName];
                            } else {
                              nextObject[fieldName] = nextValue;
                            }
                            return nextObject;
                          });
                        }}
                      />
                      {fieldDescription ? <Text type={TextTypes.Body7} color="muted">{fieldDescription}</Text> : null}
                    </div>
                  );
                })}
              </div>
            ) : (
              <div className="space-y-1">
                <Text type={TextTypes.Body7} color="muted">
                  This tool has no explicit property schema. Provide a JSON object payload.
                </Text>
                <Textarea
                  value={jsonDrafts[toolName] ?? "{}"}
                  onChange={(event) => {
                    const nextText = event.target.value;
                    setJsonDrafts((current) => ({ ...current, [toolName]: nextText }));
                    try {
                      const parsed = JSON.parse(nextText || "{}");
                      if (!isObject(parsed)) {
                        setJsonErrors((current) => ({ ...current, [toolName]: "Init payload must be a JSON object." }));
                        return;
                      }
                      setJsonErrors((current) => ({ ...current, [toolName]: "" }));
                      onChange({
                        ...value,
                        [toolName]: parsed,
                      });
                    } catch {
                      setJsonErrors((current) => ({ ...current, [toolName]: "Invalid JSON." }));
                    }
                  }}
                  className="min-h-24 font-mono text-xs"
                />
                {jsonErrors[toolName] ? <Text type={TextTypes.Body7} color="destructive">{jsonErrors[toolName]}</Text> : null}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
