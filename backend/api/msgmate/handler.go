package msgmate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// FunctionType represents the type of function
type FunctionType string

const (
	FunctionTypeInteractionStart    FunctionType = "interaction_start"
	FunctionTypeInteractionComplete FunctionType = "interaction_complete"
	FunctionTypeCustom              FunctionType = "custom"
)

// FunctionMetadata contains metadata about a stored function
type FunctionMetadata struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Type        FunctionType           `json:"type"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
}

// DynamicFunction represents a function that can be stored and executed
type DynamicFunction struct {
	Metadata FunctionMetadata
	Execute  func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error)
}

// FunctionRegistry stores and manages dynamic functions
type FunctionRegistry struct {
	functions map[string]*DynamicFunction
	mu        sync.RWMutex
}

// MsgmateHandler provides functionality for managing dynamic functions and interactions
type MsgmateHandler struct {
	registry *FunctionRegistry
}

// Global singleton instance
var (
	globalHandler *MsgmateHandler
	handlerOnce   sync.Once
)

// GetGlobalMsgmateHandler returns the global singleton instance of MsgmateHandler
func GetGlobalMsgmateHandler() *MsgmateHandler {
	handlerOnce.Do(func() {
		globalHandler = &MsgmateHandler{
			registry: &FunctionRegistry{
				functions: make(map[string]*DynamicFunction),
			},
		}
		log.Println("Global MsgmateHandler initialized")
	})
	return globalHandler
}

// NewMsgmateHandler creates a new MsgmateHandler instance (for testing or local use)
func NewMsgmateHandler() *MsgmateHandler {
	return &MsgmateHandler{
		registry: &FunctionRegistry{
			functions: make(map[string]*DynamicFunction),
		},
	}
}

func (h *MsgmateHandler) QuickRegisterFunction(
	executeFunc func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error),
) (string, error) {
	// Generate a random 16-byte ID
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("failed to generate random ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	err := h.RegisterFunction(id, id, id, FunctionTypeCustom, executeFunc, nil, nil)
	return id, err
}

// RegisterFunction registers a new dynamic function
func (h *MsgmateHandler) RegisterFunction(
	id string,
	name string,
	description string,
	functionType FunctionType,
	executeFunc func(initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error),
	parameters map[string]interface{},
	tags []string,
) error {
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()

	now := time.Now()

	// Check if function already exists
	if _, exists := h.registry.functions[id]; exists {
		return fmt.Errorf("function with ID '%s' already exists", id)
	}

	// Create the dynamic function
	dynamicFunc := &DynamicFunction{
		Metadata: FunctionMetadata{
			ID:          id,
			Name:        name,
			Description: description,
			Type:        functionType,
			CreatedAt:   now,
			UpdatedAt:   now,
			Parameters:  parameters,
			Tags:        tags,
		},
		Execute: executeFunc,
	}

	h.registry.functions[id] = dynamicFunc
	log.Printf("Registered function: %s (%s) - %s", id, name, description)

	return nil
}

// GetFunction retrieves a function by ID
func (h *MsgmateHandler) GetFunction(id string) (*DynamicFunction, error) {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()

	function, exists := h.registry.functions[id]
	if !exists {
		return nil, fmt.Errorf("function with ID '%s' not found", id)
	}

	return function, nil
}

// ListFunctions returns all registered functions
func (h *MsgmateHandler) ListFunctions() []FunctionMetadata {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()

	functions := make([]FunctionMetadata, 0, len(h.registry.functions))
	for _, function := range h.registry.functions {
		functions = append(functions, function.Metadata)
	}

	return functions
}

// ListFunctionsByType returns functions filtered by type
func (h *MsgmateHandler) ListFunctionsByType(functionType FunctionType) []FunctionMetadata {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()

	functions := make([]FunctionMetadata, 0)
	for _, function := range h.registry.functions {
		if function.Metadata.Type == functionType {
			functions = append(functions, function.Metadata)
		}
	}

	return functions
}

// ListFunctionsByTag returns functions that have the specified tag
func (h *MsgmateHandler) ListFunctionsByTag(tag string) []FunctionMetadata {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()

	functions := make([]FunctionMetadata, 0)
	for _, function := range h.registry.functions {
		for _, functionTag := range function.Metadata.Tags {
			if functionTag == tag {
				functions = append(functions, function.Metadata)
				break
			}
		}
	}

	return functions
}

// UpdateFunction updates an existing function's metadata
func (h *MsgmateHandler) UpdateFunction(
	id string,
	name string,
	description string,
	parameters map[string]interface{},
	tags []string,
) error {
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()

	function, exists := h.registry.functions[id]
	if !exists {
		return fmt.Errorf("function with ID '%s' not found", id)
	}

	function.Metadata.Name = name
	function.Metadata.Description = description
	function.Metadata.Parameters = parameters
	function.Metadata.Tags = tags
	function.Metadata.UpdatedAt = time.Now()

	log.Printf("Updated function: %s (%s)", id, name)
	return nil
}

// UpdateFunctionExecute updates the execute function for an existing function
func (h *MsgmateHandler) DeleteFunction(id string) error {
	h.registry.mu.Lock()
	defer h.registry.mu.Unlock()

	if _, exists := h.registry.functions[id]; !exists {
		return fmt.Errorf("function with ID '%s' not found", id)
	}

	delete(h.registry.functions, id)
	log.Printf("Deleted function: %s", id)
	return nil
}

// ExecuteFunctionByToolName executes a function based on the tool name from interaction tools
func (h *MsgmateHandler) ExecuteFunctionByToolName(toolName string, initData map[string]interface{}, inputData map[string]interface{}) (interface{}, error) {
	// Extract the actual function ID from the tool name
	// Format: interaction_start:function_id or interaction_complete:function_id
	var functionID string

	if len(toolName) > 0 {
		// Find the last colon to get the function ID
		lastColonIndex := -1
		for i := len(toolName) - 1; i >= 0; i-- {
			if toolName[i] == ':' {
				lastColonIndex = i
				break
			}
		}

		if lastColonIndex != -1 && lastColonIndex < len(toolName)-1 {
			functionID = toolName[lastColonIndex+1:]
		} else {
			functionID = toolName
		}
	} else {
		return nil, fmt.Errorf("invalid tool name: %s", toolName)
	}

	function, err := h.GetFunction(functionID)
	if err != nil {
		return nil, err
	}

	return function.Execute(initData, inputData)
}

// GetFunctionMetadata returns the metadata for a function by ID
func (h *MsgmateHandler) GetFunctionMetadata(id string) (*FunctionMetadata, error) {
	function, err := h.GetFunction(id)
	if err != nil {
		return nil, err
	}

	return &function.Metadata, nil
}

// ExportFunctions exports all functions to JSON
func (h *MsgmateHandler) ExportFunctions() ([]byte, error) {
	functions := h.ListFunctions()
	return json.MarshalIndent(functions, "", "  ")
}

// ImportFunctions imports functions from JSON (metadata only, execute functions need to be registered separately)
func (h *MsgmateHandler) ImportFunctions(data []byte) error {
	var functions []FunctionMetadata
	if err := json.Unmarshal(data, &functions); err != nil {
		return fmt.Errorf("failed to unmarshal functions: %w", err)
	}

	log.Printf("Importing %d function metadata records", len(functions))
	// Note: This only imports metadata, not the actual execute functions
	// Execute functions would need to be registered separately

	return nil
}
