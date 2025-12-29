package models

import "time"

type User struct {
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id" gorm:"primaryKey"`
	Number    string    `json:"number"`
}

func (User) TableName() string {
	return "users"
}
