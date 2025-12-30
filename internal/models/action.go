package models

import "time"

type Action struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id"`
	Type      string    `json:"type"`
	Network   string    `json:"network"`
	Currency  string    `json:"currency"`
	Amount    float64   `json:"amount"`
	Hash      string    `json:"hash"`
	Fee       float64   `json:"fee"`
	AmlId     string    `json:"aml_id"`
	Status    string    `json:"status"`
}

func (Action) TableName() string {
	return "actions"
}
