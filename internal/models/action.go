package models

import "time"

type Action struct {
	ID        string         `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time      `json:"created_at"`
	UserId    uint           `json:"user_id"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data" gorm:"serializer:json"`
	Status    string         `json:"status"`
}

func (Action) TableName() string {
	return "actions"
}
