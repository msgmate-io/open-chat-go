package database

import (
	"strings"

	"gorm.io/gorm"
)

type IntegrationAccess struct {
	Model
	UserId          uint   `json:"-" gorm:"index;uniqueIndex:idx_user_integration_access"`
	User            User   `json:"-" gorm:"foreignKey:UserId;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	IntegrationName string `json:"integration_name" gorm:"type:varchar(128);index;uniqueIndex:idx_user_integration_access"`
}

func normalizeIntegrationAccessName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func EnsureIntegrationAccess(db *gorm.DB, userID uint, integrationName string) error {
	integrationName = normalizeIntegrationAccessName(integrationName)
	if userID == 0 || integrationName == "" {
		return nil
	}
	record := IntegrationAccess{UserId: userID, IntegrationName: integrationName}
	return db.Where("user_id = ? AND integration_name = ?", userID, integrationName).FirstOrCreate(&record).Error
}

func RevokeIntegrationAccess(db *gorm.DB, userID uint, integrationName string) error {
	integrationName = normalizeIntegrationAccessName(integrationName)
	if userID == 0 || integrationName == "" {
		return nil
	}
	return db.Where("user_id = ? AND integration_name = ?", userID, integrationName).Delete(&IntegrationAccess{}).Error
}

func HasIntegrationAccess(db *gorm.DB, userID uint, integrationName string) (bool, error) {
	integrationName = normalizeIntegrationAccessName(integrationName)
	if userID == 0 || integrationName == "" {
		return false, nil
	}
	var count int64
	err := db.Model(&IntegrationAccess{}).
		Where("user_id = ? AND integration_name = ?", userID, integrationName).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func ListIntegrationAccessByUserID(db *gorm.DB, userID uint) ([]IntegrationAccess, error) {
	if userID == 0 {
		return []IntegrationAccess{}, nil
	}
	rows := []IntegrationAccess{}
	err := db.Where("user_id = ?", userID).Order("integration_name asc").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
