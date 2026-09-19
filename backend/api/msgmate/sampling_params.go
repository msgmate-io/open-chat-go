package msgmate

import (
	"encoding/json"
	"math"
	"strings"
)

// SamplingParams carries optional generation parameters from a chat's shared
// config. Pointer fields distinguish "unset" (nil, the model provider's
// default applies) from valid zero values such as temperature 0.
type SamplingParams struct {
	Temperature      *float64
	MaxTokens        *int
	TopP             *float64
	PresencePenalty  *float64
	FrequencyPenalty *float64
	// UseMaxCompletionTokens overrides model-family detection for OpenAI
	// model aliases and future models.
	UseMaxCompletionTokens *bool
	// DisabledSamplingParams replaces model-family defaults when configured.
	// A pointer distinguishes an explicit empty list from an absent setting.
	DisabledSamplingParams *[]string
}

func (p SamplingParams) isEmpty() bool {
	return p.Temperature == nil && p.MaxTokens == nil && p.TopP == nil &&
		p.PresencePenalty == nil && p.FrequencyPenalty == nil
}

// configNumberAsFloat converts a JSON-decoded config value into a float64.
// Config maps round-trip through encoding/json, so numbers usually arrive as
// float64, but int/int64/json.Number values are accepted as well.
func configNumberAsFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return 0, false
		}
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// configNumberAsInt converts a JSON-decoded config value into an int,
// tolerating the float64 representation produced by encoding/json.
func configNumberAsInt(value interface{}) (int, bool) {
	asFloat, ok := configNumberAsFloat(value)
	if !ok {
		return 0, false
	}
	if asFloat != math.Trunc(asFloat) {
		return 0, false
	}
	return int(asFloat), true
}

func mapOptionalFloat(m map[string]interface{}, key string) *float64 {
	if m == nil {
		return nil
	}
	value, exists := m[key]
	if !exists || value == nil {
		return nil
	}
	asFloat, ok := configNumberAsFloat(value)
	if !ok {
		return nil
	}
	return &asFloat
}

func mapOptionalInt(m map[string]interface{}, key string) *int {
	if m == nil {
		return nil
	}
	value, exists := m[key]
	if !exists || value == nil {
		return nil
	}
	asInt, ok := configNumberAsInt(value)
	if !ok {
		return nil
	}
	return &asInt
}

// mapInt64OrDefault reads an integer config value with a default. Unlike a
// plain type assertion it tolerates the float64 representation produced by
// encoding/json, so values like context windows configured as JSON numbers
// are honored.
func mapInt64OrDefault(m map[string]interface{}, key string, defaultValue int64) int64 {
	if m == nil {
		return defaultValue
	}
	value, exists := m[key]
	if !exists || value == nil {
		return defaultValue
	}
	if typed, ok := value.(int64); ok {
		return typed
	}
	asInt, ok := configNumberAsInt(value)
	if !ok {
		return defaultValue
	}
	return int64(asInt)
}

// samplingParamsFromConfig extracts the supported sampling parameters from a
// shared config map. Only explicitly configured values are returned; missing
// keys stay nil so the provider default applies.
func samplingParamsFromConfig(configMap map[string]interface{}) SamplingParams {
	var useMaxCompletionTokens *bool
	if value, ok := configMap["use_max_completion_tokens"].(bool); ok {
		useMaxCompletionTokens = &value
	}
	disabledSamplingParams := mapOptionalStringSlice(configMap, "disabled_sampling_params")

	return SamplingParams{
		Temperature:            mapOptionalFloat(configMap, "temperature"),
		MaxTokens:              mapOptionalInt(configMap, "max_tokens"),
		TopP:                   mapOptionalFloat(configMap, "top_p"),
		PresencePenalty:        mapOptionalFloat(configMap, "presence_penalty"),
		FrequencyPenalty:       mapOptionalFloat(configMap, "frequency_penalty"),
		UseMaxCompletionTokens: useMaxCompletionTokens,
		DisabledSamplingParams: disabledSamplingParams,
	}
}

func mapOptionalStringSlice(m map[string]interface{}, key string) *[]string {
	if m == nil {
		return nil
	}
	raw, exists := m[key]
	if !exists {
		return nil
	}

	values := make([]string, 0)
	switch items := raw.(type) {
	case []interface{}:
		for _, item := range items {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return nil
			}
			values = append(values, strings.ToLower(strings.TrimSpace(value)))
		}
	case []string:
		for _, value := range items {
			if strings.TrimSpace(value) == "" {
				return nil
			}
			values = append(values, strings.ToLower(strings.TrimSpace(value)))
		}
	default:
		return nil
	}
	return &values
}

func openAIModelUsesMaxCompletionTokens(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "gpt-5" || strings.HasPrefix(model, "gpt-5-") || strings.HasPrefix(model, "gpt-5.") {
		return true
	}

	if len(model) < 2 || model[0] != 'o' {
		return false
	}
	i := 1
	for i < len(model) && model[i] >= '0' && model[i] <= '9' {
		i++
	}
	return i > 1 && (i == len(model) || model[i] == '-')
}

func useMaxCompletionTokens(backend, model string, override *bool) bool {
	if !strings.EqualFold(strings.TrimSpace(backend), "openai") {
		return false
	}
	if override != nil {
		return *override
	}
	return openAIModelUsesMaxCompletionTokens(model)
}

func disabledSamplingParams(backend, model string, params SamplingParams) map[string]bool {
	disabled := make(map[string]bool)
	if params.DisabledSamplingParams != nil {
		for _, name := range *params.DisabledSamplingParams {
			disabled[strings.ToLower(strings.TrimSpace(name))] = true
		}
		return disabled
	}

	if useMaxCompletionTokens(backend, model, params.UseMaxCompletionTokens) {
		for _, name := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty"} {
			disabled[name] = true
		}
	}
	return disabled
}

// applySamplingParamsToRequestBody adds configured sampling parameters to an
// OpenAI-compatible chat completions request body. Only explicitly configured
// values are forwarded (nil means "keep the provider default"), and zero
// values such as temperature 0 are forwarded like any other value.
//
// The anthropic backend has no equivalent of presence_penalty and
// frequency_penalty; those two are dropped for it. temperature, top_p and
// the provider-compatible token limit are forwarded for all backends.
func applySamplingParamsToRequestBody(requestBody map[string]interface{}, params SamplingParams, backend, model string) {
	if requestBody == nil || params.isEmpty() {
		return
	}
	disabled := disabledSamplingParams(backend, model, params)
	if params.Temperature != nil && !disabled["temperature"] {
		requestBody["temperature"] = *params.Temperature
	}
	if params.MaxTokens != nil {
		tokenLimitKey := "max_tokens"
		if useMaxCompletionTokens(backend, model, params.UseMaxCompletionTokens) {
			tokenLimitKey = "max_completion_tokens"
		}
		requestBody[tokenLimitKey] = *params.MaxTokens
	}
	if params.TopP != nil && !disabled["top_p"] {
		requestBody["top_p"] = *params.TopP
	}
	if backend == "anthropic" {
		return
	}
	if params.PresencePenalty != nil && !disabled["presence_penalty"] {
		requestBody["presence_penalty"] = *params.PresencePenalty
	}
	if params.FrequencyPenalty != nil && !disabled["frequency_penalty"] {
		requestBody["frequency_penalty"] = *params.FrequencyPenalty
	}
}
