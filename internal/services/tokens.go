package services

type TokensService struct{}

func NewTokensService() *TokensService {
	return &TokensService{}
}

// GetTokenInfo
// Get token info by ticker
func (s *TokensService) GetTokenInfo(ticker string) map[string]string {
	arr := make(map[string]string)
	return arr
}
