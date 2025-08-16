package services

type TokensService struct{}

// NewTokensService Create new instance of TokensService
func NewTokensService() *TokensService {
	return &TokensService{}
}

// GetTokenInfo Get token info by ticker
func (service *TokensService) GetTokenInfo(ticker string) (map[string]any, error) {
	token := make(map[string]any)

	return map[string]any{
		"token": token,
	}, nil
}
