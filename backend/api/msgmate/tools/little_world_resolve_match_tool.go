package tools

import "fmt"

type LittleWorldResolveMatchToolInput struct {
	MatchID int `json:"match_id"`
}

var LittleWorldResolveMatchToolDef = ToolDefinition{
	Name:           "little_world__resolve_match",
	Description:    "Resolve a match in Little World.",
	RequiresInit:   true,
	InputType:      LittleWorldResolveMatchToolInput{},
	RequiredParams: []string{"match_id"},
	Parameters: map[string]interface{}{
		"match_id": map[string]interface{}{
			"type":        "integer",
			"description": "The numeric ID of the match to resolve.",
		},
	},
	RunFunction: func(input interface{}, initData map[string]interface{}) (string, error) {
		toolInput := input.(LittleWorldResolveMatchToolInput)
		sessionID, csrfToken, apiHost, _, err := extractUserInitData(initData)
		if err != nil {
			return "", err
		}
		fullURL := fmt.Sprintf("%s/api/matching/matches/%d/resolve/", apiHost, toolInput.MatchID)
		if _, err := makeAPIRequest("POST", fullURL, nil, sessionID, csrfToken); err != nil {
			return fmt.Sprintf("error resolving match: %s", err), err
		}
		return "Successfully resolved match", nil
	},
}
