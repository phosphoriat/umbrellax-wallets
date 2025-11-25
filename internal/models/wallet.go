package models

import "time"

type Wallet struct {
	CreatedAt time.Time `json:"created_at"`
	UserId    uint      `json:"user_id"`
	Network   string    `json:"network"`
	Address   string    `json:"address"`
}
