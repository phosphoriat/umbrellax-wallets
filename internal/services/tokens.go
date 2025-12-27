package services

import (
	"context"

	"wallets/internal/services/api/bybit"
)

type TokensService struct{}

// NewTokensService Create new instance of TokensService
func NewTokensService() *TokensService {
	return &TokensService{}
}

// GetTokenInfo Get token info by ticker
func (s *TokensService) GetTokenInfo(network, currency string) (map[string]any, error) {
	res, err := bybit.GetClient().GetCoinInfo(context.Background(), currency, network)
	if err != nil {
		return nil, err
	}

	return res, nil
}
