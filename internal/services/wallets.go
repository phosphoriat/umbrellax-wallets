package services

type WalletsService struct{}

// NewWalletsService Create new instance of WalletsService
func NewWalletsService() *WalletsService {
	return &WalletsService{}
}

// GetUserWallet
// Get user HD-wallet by user id
func (s *WalletsService) GetUserWallet(userId int, network string) map[string]string {
	arr := make(map[string]string)
	return arr
}

// GetMasterWallet
// Get master wallet info by network
func (s *WalletsService) GetMasterWallet(network string) map[string]string {
	arr := make(map[string]string)
	return arr
}
