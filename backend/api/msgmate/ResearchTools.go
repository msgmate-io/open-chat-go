package msgmate

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ---- RWTH-Aachen Seminar Tims auto-Paper Include/Exlude Agent

type RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput struct {
	Include bool   `json:"include"`
	Reason  string `json:"reason"`
}

var RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentDef = ToolDefinition{
	Name:           "rwth_aachen_seminar_tims_auto_paper_include_exclude",
	Description:    "Include or exclude a paper from the seminar tims auto-paper selection",
	RequiresInit:   true,
	InputType:      RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput{},
	RequiredParams: []string{"include"},
	Parameters: map[string]interface{}{
		"include": map[string]interface{}{
			"type":        "boolean",
			"description": "Whether to include the paper in the seminar tims auto-paper selection",
		},
		"reason": map[string]interface{}{
			"type":        "string",
			"description": "The reason for the inclusion/exclusion decision",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		var toolInput = input.(RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentInput)
		paperId, apiHost, paperTitle, err := ExtractPaperCategorizationInitData(initData)
		if err != nil {
			return "", err
		}

		fullURL := fmt.Sprintf("%s/api/papers/%s/include_exclude/", apiHost, paperId)

		body := new(bytes.Buffer)
		err = json.NewEncoder(body).Encode(map[string]interface{}{
			"include": toolInput.Include,
			"title":   paperTitle,
			"reason":  toolInput.Reason,
		})
		if err != nil {
			return "", fmt.Errorf("error encoding request body: %w", err)
		}

		_, err = MakeRequestSimple("POST", fullURL, body)
		if err != nil {
			return fmt.Sprintf("error including/excluding paper: %s", err), err
		}

		return "Successfully included/excluded paper", nil
	},
}

func NewRWTHAachenSeminarTimsAutoPaperIncludeExcludeAgent() Tool {
	return NewToolFromDefinition(RWTHAachenSeminarTimsAutoPaperIncludeExcludeAgentDef)
}
