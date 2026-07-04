package database

import "time"

const (
	RegistrationRequestStatusPending  = "pending"
	RegistrationRequestStatusApproved = "approved"
	RegistrationRequestStatusRejected = "rejected"
)

type RegistrationRequest struct {
	Model
	Name             string     `json:"name"`
	Email            string     `json:"email" gorm:"index"`
	PasswordHash     string     `json:"-"`
	Status           string     `json:"status" gorm:"index;default:pending"`
	ReviewNote       string     `json:"review_note"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	ReviewedByUserId *uint      `json:"reviewed_by_user_id" gorm:"index"`
	ReviewedByUser   *User      `json:"reviewed_by_user,omitempty" gorm:"foreignKey:ReviewedByUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
	ApprovedUserId   *uint      `json:"approved_user_id" gorm:"index"`
	ApprovedUser     *User      `json:"approved_user,omitempty" gorm:"foreignKey:ApprovedUserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:SET NULL;"`
}
