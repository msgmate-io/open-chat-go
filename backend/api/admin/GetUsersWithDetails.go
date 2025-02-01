package admin

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

type UserDetails struct {
	ID           uint                  `json:"id"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
	Name         string               `json:"name"`
	Email        string               `json:"email"`
	ContactToken string               `json:"contact_token"`
	IsAdmin      bool                 `json:"is_admin"`
	UserType     string               `json:"user_type"` // "regular", "integration", "network"
	
	// Integration details (if applicable)
	HasIntegrations  bool                 `json:"has_integrations"`
	IntegrationsCount int                 `json:"integrations_count"`
	Integrations     []IntegrationSummary `json:"integrations,omitempty"`
	
	// Network details (if applicable)
	IsNetworkUser    bool   `json:"is_network_user"`
	NetworkName      string `json:"network_name,omitempty"`
	
	// Activity details
	LastLogin        *time.Time `json:"last_login,omitempty"`
	SessionsCount    int        `json:"sessions_count"`
	ChatsCount       int        `json:"chats_count"`
	MessagesCount    int        `json:"messages_count"`
}

type IntegrationSummary struct {
	ID               uint       `json:"id"`
	IntegrationName  string     `json:"integration_name"`
	IntegrationType  string     `json:"integration_type"`
	Active           bool       `json:"active"`
	LastUsed         *time.Time `json:"last_used,omitempty"`
}

type PaginatedUsersData struct {
	database.Pagination
	Users []UserDetails `json:"users"`
}

func GetUsersWithDetails(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	// Setup pagination
	pagination := database.Pagination{Page: 1, Limit: 20}
	if pageParam := r.URL.Query().Get("page"); pageParam != "" {
		if page, err := strconv.Atoi(pageParam); err == nil && page > 0 {
			pagination.Page = page
		}
	}

	if limitParam := r.URL.Query().Get("limit"); limitParam != "" {
		if limit, err := strconv.Atoi(limitParam); err == nil && limit > 0 {
			pagination.Limit = limit
		}
	}

	// Get total count for pagination
	var totalUsers int64
	if err := DB.Model(&database.User{}).Count(&totalUsers).Error; err != nil {
		http.Error(w, fmt.Sprintf("Error counting users: %v", err), http.StatusInternalServerError)
		return
	}

	pagination.TotalRows = totalUsers
	pagination.TotalPages = int(math.Ceil(float64(totalUsers) / float64(pagination.Limit)))

	// Get users with pagination
	var users []database.User
	if err := DB.Offset(pagination.GetOffset()).
		Limit(pagination.GetLimit()).
		Order("created_at DESC").
		Find(&users).Error; err != nil {
		http.Error(w, fmt.Sprintf("Error fetching users: %v", err), http.StatusInternalServerError)
		return
	}

	userDetails := make([]UserDetails, 0, len(users))

	for _, u := range users {
		details := UserDetails{
			ID:           u.ID,
			CreatedAt:    u.CreatedAt,
			UpdatedAt:    u.UpdatedAt,
			Name:         u.Name,
			Email:        u.Email,
			ContactToken: u.ContactToken,
			IsAdmin:      u.IsAdmin,
			UserType:     "regular",
		}

		// Check if user has integrations
		var integrations []database.Integration
		if err := DB.Where("user_id = ?", u.ID).Find(&integrations).Error; err == nil {
			details.HasIntegrations = len(integrations) > 0
			details.IntegrationsCount = len(integrations)
			
			if len(integrations) > 0 {
				details.UserType = "integration"
				for _, integration := range integrations {
					summary := IntegrationSummary{
						ID:               integration.ID,
						IntegrationName:  integration.IntegrationName,
						IntegrationType:  integration.IntegrationType,
						Active:           integration.Active,
						LastUsed:         integration.LastUsed,
					}
					details.Integrations = append(details.Integrations, summary)
				}
			}
		}

		// Check if user is a network user (created for a network)
		var network database.Network
		if err := DB.Where("network_name = ?", u.Name).First(&network).Error; err == nil {
			details.IsNetworkUser = true
			details.NetworkName = network.NetworkName
			if details.UserType == "regular" {
				details.UserType = "network"
			}
		}

		// Get activity stats
		var sessionCount int64
		DB.Model(&database.Session{}).Where("user_id = ?", u.ID).Count(&sessionCount)
		details.SessionsCount = int(sessionCount)

		// Get the latest session for last login
		var latestSession database.Session
		if err := DB.Where("user_id = ?", u.ID).Order("created_at DESC").First(&latestSession).Error; err == nil {
			details.LastLogin = &latestSession.CreatedAt
		}

		// Get chat counts (as participant in User1Id or User2Id)
		var chatCount int64
		DB.Model(&database.Chat{}).
			Where("user1_id = ? OR user2_id = ?", u.ID, u.ID).
			Count(&chatCount)
		details.ChatsCount = int(chatCount)

		// Get message counts
		var messageCount int64
		DB.Model(&database.Message{}).Where("sender_id = ?", u.ID).Count(&messageCount)
		details.MessagesCount = int(messageCount)

		userDetails = append(userDetails, details)
	}

	response := PaginatedUsersData{
		Pagination: pagination,
		Users:      userDetails,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
