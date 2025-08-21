package chains

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/vault"
	"wallets/internal/services/api/aml"
	"wallets/internal/services/api/bybit"

	"github.com/fbsobreira/gotron-sdk/pkg/address"
	"github.com/fbsobreira/gotron-sdk/pkg/client"
	"github.com/fbsobreira/gotron-sdk/pkg/client/transaction"
	"github.com/fbsobreira/gotron-sdk/pkg/common/decimals"
	"github.com/fbsobreira/gotron-sdk/pkg/keys"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/gogo/protobuf/proto"
)

// Tron RPC connection struct
type Tron struct {
	BaseChainService
	Client *client.GrpcClient
}

// Global package vars
var (
	tronInstance *Tron
	tronOnce     sync.Once
)

// GetTron Get Tron chain instance
func GetTron() *Tron {
	tronOnce.Do(func() {
		tronInstance = newTron()
	})
	return tronInstance
}

// @private newTron Create new instance of Tron (private constructor)
func newTron() *Tron {
	// Load config
	cfg := config.Load()

	// Create new RPC client
	rpcClient := client.NewGrpcClient(cfg.TronRpcUrl)
	err := rpcClient.Start(client.GRPCInsecure())
	if err != nil {
		log.Printf("failed to connect Tron RPC API: %v", err)
	}

	networkCode := "tron"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Tron{
		BaseChainService: BaseChainService{
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
			AmlTokenId:  networkSettings["aml_token_id"].(string),
		},
		Client: rpcClient,
	}
}

// GetCode Get network code
func (tron *Tron) GetCode() string {
	return tron.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
func (tron *Tron) GetUserWallet(userId int) (string, error) {
	// Get master wallet mnemonic from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master mnemonic
	derivedAddr, err := tron.deriveAddress(masterData["mnemonic"].(string), userId)
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
func (tron *Tron) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get trc20 tokens balances
	balances := make(map[string]any)
	for token, contractAddr := range config.ContractsAddresses[tron.NetworkCode].(map[string]string) {
		tokenBalance, _ := tron.getTrc20Balance(contractAddr, masterAddr)
		balances[token] = tokenBalance
	}

	// Get TRX native balance
	trxBalance, _ := tron.getNativeBalance(masterAddr)
	balances[tron.NativeToken] = trxBalance

	return map[string]any{
		"address":  masterAddr,
		"balances": balances,
	}, nil
}

// GetTransactionCommission Get transaction commission
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
func (tron *Tron) GetTransactionCommission(currency, toAddr string, amount float64) (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Calc chain fee for currency
	var chainFee float64 = 0
	switch currency {
	case "TRX":
		chainFee, _ = tron.calcTrxFee(masterAddr, toAddr, amount)

	case "USDT":
		chainFee, _ = tron.calcUsdtFee(masterAddr, toAddr, amount)

	default:
		return nil, fmt.Errorf("unknown currency: %s", currency)
	}

	// Get bybit withdrawal fee
	bybitFee, _ := bybit.GetHTTPClient().GetWithdrawalFee(currency, tron.NetworkCode)

	// Get AML check cost from config
	cfg := config.Load()
	amlFee := cfg.AmlCheckCost

	return map[string]any{
		"chain_fee": chainFee,
		"bybit_fee": bybitFee,
		"aml_fee":   amlFee,
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
func (tron *Tron) GetTransactionStatus(hash string) (string, error) {
	tx, err := tron.Client.GetTransactionByID(hash)
	if err != nil {
		return "", fmt.Errorf("failed to get transaction %s: %w", hash, err)
	}

	if tx.Ret == nil || len(tx.Ret) == 0 {
		return "pending", nil
	}

	status := tx.Ret[0].ContractRet.String()
	return status, nil
}

// SendTransaction Send transaction to the network
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
func (tron *Tron) SendTransaction(currency, toAddr string, amount float64) (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s master address in Vault", tron.NetworkCode)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "TRX":
		hash, err = tron.sendTrx(masterAddr, toAddr, amount)

	case "USDT":
		hash, err = tron.sendUsdt(masterAddr, toAddr, amount)

	default:
		return nil, fmt.Errorf("unknown currency: %s", currency)
	}

	// AML Check transaction by hash and get aml id
	amlId, err := aml.GetClient().CheckTransaction(tron.NetworkCode, tron.AmlTokenId, hash, toAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to AML check %s %s transaction: %w", tron.NetworkCode, hash, err)
	}

	return map[string]any{
		"hash":   hash,
		"aml_id": amlId,
	}, nil
}

// ProcessInboundTransactions Process inbound transactions
func (tron *Tron) ProcessInboundTransactions() {
	
}

/* GetUserWallet private methods group */
// @private deriveAddress Get user HD-wallet address from master wallet address
// mnemonic: Wallet mnemonic
// userId: Unique user id for derivation index
func (tron *Tron) deriveAddress(mnemonic string, userId int) (string, error) {
	// Generate private key from mnemonic and userId
	private, err := keys.FromMnemonicSeedAndPassphrase(mnemonic, "", userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive private key: %w", err)
	}

	// Get ECDSA private key
	ecdsaPrivate := private.ToECDSA()
	if ecdsaPrivate == nil {
		return "", fmt.Errorf("failed to convert to ECDSA private key")
	}

	// Generate address from public key
	addr := address.PubkeyToAddress(ecdsaPrivate.PublicKey)
	return addr.String(), nil
}

// @private getNativeBalance Get wallet TRX balance
// addr: Wallet address
func (tron *Tron) getNativeBalance(addr string) (float64, error) {
	// Convert wallet address
	tronAddr, err := address.Base58ToAddress(addr)
	if err != nil {
		return 0, err
	}

	// Get account by wallet address
	account, err := tron.Client.GetAccount(tronAddr.String())
	if err != nil {
		return 0, err
	}

	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, tron.NetworkCode, "TRX", "decimals")
	// Get TRX balance
	trxBalance, _ := decimals.RemoveDecimals(big.NewInt(account.Balance), trxDecimals).Float64()

	return trxBalance, nil
}

// @private getTrc20Balance Get wallet TRC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
func (tron *Tron) getTrc20Balance(contractAddr, addr string) (float64, error) {
	// Get contract balance
	rawBalance, err := tron.Client.TRC20ContractBalance(addr, contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract balance: %w", err)
	}

	// Get contract decimals
	decimalsValue, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}

	// Return converted balance
	convertedBalance, _ := decimals.RemoveDecimals(rawBalance, decimalsValue.Int64()).Float64()

	return convertedBalance, nil
}

/* GetTransactionCommission private methods group */
// @private calcTrxFee Calc fee for TRX
// fromAddress: Master wallet address
// toAddress: Address for withdrawal
// amount: Amount in float value
func (tron *Tron) calcTrxFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, tron.NetworkCode, "TRX", "decimals")
	// Convert amount to SUN including decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), trxDecimals)

	// Check, if to account activated
	activationFee := big.NewInt(0)
	toAccount, err := tron.Client.GetAccount(toAddr)
	// If not activated, set 1 TRX of activation fee (in SUN)
	if err != nil || toAccount.Address == nil {
		activationFee, _ = decimals.ApplyDecimals(big.NewFloat(1), trxDecimals) // 1 TRX in SUN
	}

	// Create tx to measure raw data size (don't send it)
	tx, err := tron.Client.Transfer(fromAddr, toAddr, amountSun.Int64())
	if err != nil {
		return 0, fmt.Errorf("failed to create transfer transaction: %v", err)
	}

	// Get serialized protobuf tx raw_data
	rawDataBytes, err := proto.Marshal(tx.GetTransaction().GetRawData())
	if err != nil {
		return 0, fmt.Errorf("failed to marshal TRX transaction raw data: %v", err)
	}
	// Get needed bandwidth - length of serialized data (~268 bytes for TRX)
	bandwidthNeeded := int64(len(rawDataBytes))

	// Calc bandwidth cost
	bandwidthCost := int64(0)
	// Get from address account resources info
	resources, err := tron.Client.GetAccountResource(fromAddr)
	if err == nil {
		// Calc available account bandwidth
		availableBandwidth := (resources.NetLimit - resources.NetUsed) +
			(resources.FreeNetLimit - resources.FreeNetUsed)

		if availableBandwidth < bandwidthNeeded {
			// Not enough free bandwidth
			paidBandwidth := bandwidthNeeded - availableBandwidth
			if paidBandwidth > 0 {
				bandwidthCost = paidBandwidth * chainParams["bandwidthPrice"]
			}
		}
	} else {
		// If we cannot get resources, need to pay for the entire bandwidth
		bandwidthCost = bandwidthNeeded * chainParams["bandwidthPrice"]
	}

	// Get total fee in SUN
	totalFeeSun := bandwidthCost + activationFee.Int64()
	// Convert total fee to TRX
	totalFee, _ := decimals.RemoveDecimals(big.NewInt(totalFeeSun), trxDecimals).Float64()

	return totalFee, nil
}

// @private calcUsdtFee Calc fee for USDT
// fromAddress: Master wallet address
// toAddress: Address for withdrawal
// amount: Amount in float value
func (tron *Tron) calcUsdtFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get USDT contract address
	contractAddr, err := config.GetChainSetting[string](config.ContractsAddresses, tron.NetworkCode, "USDT", "")
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get contract decimals
	contractDecimals, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}
	// Convert amount to units including decimals
	amountUnits, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals.Int64())

	// Get needed energy value from config
	energyNeeded, err := config.GetChainSetting[int64](config.TokenSettings, tron.NetworkCode, "USDT", "estimated_energy")
	if err != nil {
		return 0, fmt.Errorf("failed to get estimated_energy from config: %w", err)
	}
	// Try to calc exact needed energy value
	if energyEstimate, err := tron.Client.EstimateEnergy(
		fromAddr,
		contractAddr,
		"transfer(address,uint256)",
		fmt.Sprintf(`[{"type":"address","value":"%s"},{"type":"uint256","value":"%s"}]`,
			toAddr, amountUnits.String()),
		0, "", 0,
	); err == nil {
		energyNeeded = energyEstimate.EnergyRequired
	}

	// Check, if to address account has USDT (affects cost)
	trc20Balance, _ := tron.getTrc20Balance(contractAddr, toAddr)
	recipientHasBalance := trc20Balance > 0
	if !recipientHasBalance {
		// If to address account doesn't have USDT, need x2 more energy
		energyNeeded = energyNeeded * 2
	}

	// Create tx of smart contract call
	tx, err := tron.Client.TriggerContract(
		fromAddr,
		contractAddr,
		"transfer(address,uint256)",
		fmt.Sprintf(`[{"type":"address","value":"%s"},{"type":"uint256","value":"%s"}]`,
			toAddr, amountUnits.String()),
		0, 0, "", 0,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger contract: %w", err)
	}

	// Get serialized protobuf tx raw_data
	rawDataBytes, err := proto.Marshal(tx.GetTransaction().GetRawData())
	if err != nil {
		return 0, fmt.Errorf("failed to marshal TRC20 transaction raw data: %v", err)
	}
	// Get needed bandwidth - length of serialized data (~345 bytes for TRC20)
	bandwidthNeeded := int64(len(rawDataBytes))

	// Calc energy and bandwidth cost
	energyCost := int64(0)
	bandwidthCost := int64(0)
	// Get from address account resources info
	resources, err := tron.Client.GetAccountResource(fromAddr)
	if err == nil {
		// Calc available account energy
		availableEnergy := resources.EnergyLimit - resources.EnergyUsed
		if availableEnergy < energyNeeded {
			paidEnergy := energyNeeded - availableEnergy
			if paidEnergy < 0 {
				paidEnergy = 0
			}
			energyCost = paidEnergy * chainParams["energyPrice"]
		}

		// Calc available account bandwidth
		availableBandwidth := (resources.NetLimit - resources.NetUsed) +
			(resources.FreeNetLimit - resources.FreeNetUsed)
		if availableBandwidth < bandwidthNeeded {
			paidBandwidth := bandwidthNeeded - availableBandwidth
			if paidBandwidth < 0 {
				paidBandwidth = 0
			}
			bandwidthCost = paidBandwidth * chainParams["bandwidthPrice"]
		}
	} else {
		// If we cannot get resources, need to pay for all
		energyCost = energyNeeded * chainParams["energyPrice"]
		bandwidthCost = bandwidthNeeded * chainParams["bandwidthPrice"]
	}

	// Get total fee in units
	totalFeeUnits := energyCost + bandwidthCost
	// Convert total fee to decimals
	totalFee, _ := decimals.RemoveDecimals(big.NewInt(totalFeeUnits), contractDecimals.Int64()).Float64()

	return totalFee, nil
}

// @private getCurrentChainParams Get current chain params (energyPrice and bandwidthPrice)
func (tron *Tron) getCurrentChainParams() (map[string]int64, error) {
	// Get current chain params
	params, err := tron.Client.Client.GetChainParameters(context.Background(), &api.EmptyMessage{})
	if err != nil {
		return nil, fmt.Errorf("failed to get chain parameters: %w", err)
	}

	// Get only energyPrice and bandwidthPrice from params
	chainParams := make(map[string]int64)
	for _, param := range params.ChainParameter {
		switch param.Key {
		case "getEnergyFee":
			chainParams["energyPrice"] = param.Value

		case "getTransactionFee":
			chainParams["bandwidthPrice"] = param.Value
		}
	}

	return chainParams, nil
}

/* SendTransaction private methods group */
// @private sendTrx Send TRX transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
func (tron *Tron) sendTrx(fromAddr, toAddr string, amount float64) (string, error) {
	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](config.TokenSettings, tron.NetworkCode, "TRX", "decimals")
	// Convert amount to SUN including decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), trxDecimals)

	// Create transaction
	tx, err := tron.Client.Transfer(fromAddr, toAddr, amountSun.Int64())
	if err != nil {
		return "", fmt.Errorf("failed to create TRX transaction: %v", err)
	}

	// Sign and broadcast transaction to the network
	return tron.signAndBroadcast(tx)
}

// @private sendUsdt Send USDT transaction
// fromAddr: Master wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
func (tron *Tron) sendUsdt(fromAddr, toAddr string, amount float64) (string, error) {
	// Get USDT contract address
	contractAddr, err := config.GetChainSetting[string](config.ContractsAddresses, tron.NetworkCode, "USDT", "")
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get contract decimals
	contractDecimals, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get contract decimals: %w", err)
	}
	// Convert amount to units including decimals
	amountUnits, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals.Int64())

	// Create TRC20 transaction
	tx, err := tron.Client.TRC20Send(fromAddr, toAddr, contractAddr, amountUnits, 0)
	if err != nil {
		return "", fmt.Errorf("failed to create USDT transaction: %v", err)
	}

	// Sign and broadcast transaction to the network
	return tron.signAndBroadcast(tx)
}

// @private signAndBroadcast Sign and broadcast transaction to the network
// tx: Transaction struct
func (tron *Tron) signAndBroadcast(tx *api.TransactionExtention) (string, error) {
	// Get master wallet private key from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	privateKeyStr, ok := masterData["privateKey"].(string)
	if !ok || privateKeyStr == "" {
		return "", fmt.Errorf("invalid or missing %s master private key in Vault", tron.NetworkCode)
	}

	// Convert hex string to *ecdsa.PrivateKey
	privateKeyBytes, err := hex.DecodeString(privateKeyStr)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key hex: %w", err)
	}
	privateKey, err := keys.GetPrivateKeyFromBytes(privateKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Sign transaction
	signedTx, err := transaction.SignTransactionECDSA(tx.Transaction, privateKey.ToECDSA())
	if err != nil {
		return "", err
	}

	// Broadcast transaction
	result, err := tron.Client.Broadcast(signedTx)
	if err != nil {
		return "", err
	}

	if !result.Result || result.Code != api.Return_SUCCESS {
		return "", fmt.Errorf("transaction failed: (%d) %s", result.Code, result.Message)
	}
	return hex.EncodeToString(tx.Txid), nil
}

/* ProcessInboundTransactions private methods group */
