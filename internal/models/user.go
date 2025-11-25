package models

import "time"

type User struct {
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id" gorm:"uniqueIndex"`
	Number    string    `json:"number"`
}
