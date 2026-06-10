package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput struct {
	Include bool   `json:"include"`
	Reason  string `json:"reason"`
}

var RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentDef = ToolDefinition{
	Name:           "rwth_aachen_seminar_tims_auto_paper_include_exclude",
	Description:    "Include or exclude a paper from the seminar tims auto-paper selection.",
	AdminOnly:      true,
	RequiresInit:   true,
	InputType:      RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput{},
	RequiredParams: []string{"include"},
	Parameters: map[string]interface{}{
		"include": map[string]interface{}{"type": "boolean", "description": "Whether to include the paper"},
		"reason":  map[string]interface{}{"type": "string", "description": "The reason for the decision"},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput)
		paperID, apiHost, paperTitle, err := extractPaperCategorizationInitData(initData)
		if err != nil {
			return "", err
		}
		fullURL := fmt.Sprintf("%s/api/papers/%s/include_exclude/", apiHost, paperID)
		body := new(bytes.Buffer)
		if err := json.NewEncoder(body).Encode(map[string]interface{}{
			"include": toolInput.Include,
			"title":   paperTitle,
			"reason":  toolInput.Reason,
		}); err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}
		if _, err := makeRequestSimple("POST", fullURL, body); err != nil {
			return fmt.Sprintf("error including/excluding paper: %s", err), err
		}
		return "Successfully included/excluded paper", nil
	},
}
