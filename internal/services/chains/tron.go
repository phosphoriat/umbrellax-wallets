package chains

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/repository"
	"wallets/internal/services/api/bybit"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil/base58"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/fbsobreira/gotron-sdk/pkg/address"
	"github.com/fbsobreira/gotron-sdk/pkg/client"
	"github.com/fbsobreira/gotron-sdk/pkg/client/transaction"
	"github.com/fbsobreira/gotron-sdk/pkg/common/decimals"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/api"
	"github.com/fbsobreira/gotron-sdk/pkg/proto/core"
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
	cfg, _ := config.Load()

	// Create new RPC client
	rpcClient := client.NewGrpcClient(cfg.TronRpcUrl)
	err := rpcClient.Start(client.GRPCInsecure())
	if err != nil {
		log.Printf("[Tron] Failed to connect to RPC API: %v", err)
	}

	networkCode := "tron"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	return &Tron{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client: rpcClient,
	}
}

// GetCode Get network code
// Returns: Network code
func (tron *Tron) GetCode() string {
	return tron.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (tron *Tron) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master mnemonic
	derivedAddr, err := tron.deriveAddress(masterData["mnemonic"].(string), uint32(userId))
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balances)
// Returns: Map of mster wallet info (address and balances of all available tokens)
func (tron *Tron) GetMasterWallet() (map[string]any, error) {
	// Get master wallet info from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

	// Get TRC20 token balances
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
// oType: Operation type (withdrawal/inner)
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (tron *Tron) GetTransactionCommission(
	oType, wType string, wId *string, currency, toAddr string, amount float64, memo string,
) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + tron.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, tron.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address
	fromAddr, ok := walletData["address"].(string)
	if !ok || fromAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet address in Vault", tron.NetworkCode)
	}

	// Calc chain fee for currency (in TRX value)
	var chainFee float64
	switch currency {
	case "TRX":
		chainFee, err = tron.calcNativeFee(fromAddr, toAddr, amount)
	default:
		chainFee, err = tron.calcTrc20Fee(currency, fromAddr, toAddr, amount)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction commission: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Get bybit withdrawal fee (in currency value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, tron.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
	}

	return map[string]float64{
		"chain_fee": chainFee, // In TRX
		"bybit_fee": bybitFee, // In currency value (f.e. USDT)
		"aml_fee":   amlFee,   // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
func (tron *Tron) GetTransactionStatus(hash, toAddr string) (string, error) {
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
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// Returns: Transaction hash
func (tron *Tron) SendTransaction(wType string, wId *string, currency, toAddr string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", tron.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, tron.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address and private key
	fromAddr, okF := walletData["address"].(string)
	privateKey, okP := walletData["privateKey"].(string)
	if !okF || !okP || fromAddr == "" {
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", tron.NetworkCode)
	}

	// Send new transaction on chain and get transaction hash
	var hash string
	switch currency {
	case "TRX":
		hash, err = tron.sendNative(fromAddr, toAddr, amount, privateKey)
	default:
		hash, err = tron.sendTrc20(currency, fromAddr, toAddr, amount, privateKey)
	}

	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return hash, nil
}

// GetInboundTransactions Get new inbound transactions list
// Returns: Inbound transactions list
func (tron *Tron) GetInboundTransactions() ([]*InboundTransaction, error) {
	ctx := context.Background()

	// Get current block
	currentBlock, err := tron.Client.GetNowBlock()
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block: %w", tron.NetworkCode, err)
	}
	// Get current block number
	currentBlockNum := currentBlock.BlockHeader.RawData.Number

	// Get last block number from Redis (or use current block number, if not exists)
	lastBlock, err := redis.GetClient().HGet(ctx, "last_blocks", tron.NetworkCode)
	var lastBlockNumber int64
	if err != nil {
		lastBlockNumber = currentBlockNum
	} else {
		lastBlockNumber, _ = strconv.ParseInt(lastBlock, 10, 64)
	}

	// Check if current block not equal or less, than last block
	if currentBlockNum <= lastBlockNumber {
		log.Printf("[%s] No new blocks to process", tron.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all address for network from DB
	addressList, err := tron.WalletRepo.GetAddressListByNetwork(tron.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", tron.NetworkCode, err)
	}

	// Get TRX decimals from config
	trxDecimals, err := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", tron.NetworkCode, "TRX"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get TRX decimals from config: %w", tron.NetworkCode, err)
	}

	// Cache for token decimals
	decimalsCache := make(map[string]int64)
	var decimalsCacheMu sync.RWMutex

	// Wait group to wait for all tx blocks goroutines to complete
	var wg sync.WaitGroup
	// Concurrent save map for successfully processed blocks
	successfulBlocks := &sync.Map{}
	// Channel for collect transactions from all blocks
	resultChan := make(chan []*InboundTransaction, currentBlockNum-lastBlockNumber)

	log.Printf("[%s] Processing blocks %d to %d", tron.NetworkCode, lastBlockNumber+1, currentBlockNum)

	// Check all new blocks
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Get block entity by number
			block, err := tron.Client.GetBlockByNum(blockNum)
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", tron.NetworkCode, blockNum, err)
				resultChan <- []*InboundTransaction{}
				return
			}

			log.Printf("[%s] Processing block %d with %d transactions",
				tron.NetworkCode, blockNum, len(block.Transactions))

			// Local goroutine tx list (one for every block)
			localTxList := make([]*InboundTransaction, 0, len(block.Transactions))

			// Process transactions in block
			for _, tx := range block.Transactions {
				if tx.Transaction.RawData == nil || len(tx.Transaction.RawData.Contract) == 0 {
					log.Printf("[%s] Skipping block %d: no contract found", tron.NetworkCode, blockNum)
					continue
				}

				// Get tx id
				txId := fmt.Sprintf("%x", tx.GetTxid())
				txInfo, err := tron.Client.GetTransactionInfoByID(txId)
				if err != nil {
					log.Printf("[%s] Failed to get transaction info: %v", tron.NetworkCode, err)
					continue
				}

				// Check status (SUCCESS = 1)
				if txInfo.Receipt.Result != core.Transaction_Result_SUCCESS {
					log.Printf("[%s] Transaction '%s' failed with status %v", tron.NetworkCode, txId, txInfo.Receipt.Result)
					continue
				}

				// Get contract entity and tx id
				contract := tx.Transaction.RawData.Contract[0]

				switch contract.Type {
				// TRX token
				case core.Transaction_Contract_TransferContract:
					// Get transfer contract entity
					transferContract := &core.TransferContract{}
					err := proto.Unmarshal(contract.Parameter.Value, transferContract)
					if err != nil {
						log.Printf("[%s] Failed to unmarshal transfer contract: %v", tron.NetworkCode, err)
						continue
					}

					if transferContract.OwnerAddress == nil || transferContract.ToAddress == nil {
						log.Printf("[%s] Error getting transfer contract owner and to address: %v", tron.NetworkCode, err)
						continue
					}

					// Get from and to address
					fromAddr := address.Address(transferContract.OwnerAddress).String()
					toAddr := address.Address(transferContract.ToAddress).String()

					// Check if address exists in network addresses list
					if userId, ok := addressList[toAddr]; ok {
						// Get converted amount (in TRX)
						amount, _ := decimals.RemoveDecimals(big.NewInt(transferContract.Amount), trxDecimals).Float64()

						localTxList = append(localTxList, &InboundTransaction{
							UserId:   int(userId),
							Currency: "TRX",
							TxId:     txId,
							From:     fromAddr,
							To:       toAddr,
							Amount:   amount,
						})
					}

				// TRC20 token
				case core.Transaction_Contract_TriggerSmartContract:
					// Get trigger contract entity
					triggerContract := &core.TriggerSmartContract{}
					err := proto.Unmarshal(contract.Parameter.Value, triggerContract)
					if err != nil {
						log.Printf("[%s] Failed to unmarshal trigger smart contract: %v", tron.NetworkCode, err)
						continue
					}

					// Get contract address
					contractAddr := address.Address(triggerContract.ContractAddress).String()

					// Get token name by contract address
					token, err := config.GetTokenByContractAddr(tron.NetworkCode, contractAddr)
					if err != nil {
						continue
					}

					if triggerContract.OwnerAddress == nil {
						log.Printf("[%s] Error getting trigger smart contract owner address: %v", tron.NetworkCode, err)
						continue
					}

					// Check if it's an TRC20 token transfer
					// Data structure: 4 bytes method ID + 32 bytes address + 32 bytes amount
					if len(triggerContract.Data) == 68 {
						// Check if method is transfer (a9059cbb)
						methodId := fmt.Sprintf("%x", triggerContract.Data[0:4])
						if methodId == "a9059cbb" {
							// Extract to address (bytes 4-36, take last 20 bytes)
							toAddrBytes := triggerContract.Data[16:36]

							// Get from and to address
							fromAddr := address.Address(triggerContract.OwnerAddress).String()
							toAddr := address.Address(toAddrBytes).String()

							// Check if address exists in network addresses list
							if userId, ok := addressList[toAddr]; ok {
								// Get contract decimals from cache or fetch from contract
								decimalsCacheMu.RLock()
								cached, exists := decimalsCache[contractAddr]
								decimalsCacheMu.RUnlock()

								var decimalsValue int64
								if exists {
									// Get contract decimals from cache
									decimalsValue = cached
								} else {
									// Fetch decimals from contract
									decimalsResult, err := tron.Client.TRC20GetDecimals(contractAddr)
									if err != nil {
										log.Printf("[%s] Failed to get contract decimals for '%s': %v",
											tron.NetworkCode, contractAddr, err)
										continue
									}
									decimalsValue = decimalsResult.Int64()

									// Save decimals to cache
									decimalsCacheMu.Lock()
									decimalsCache[contractAddr] = decimalsValue
									decimalsCacheMu.Unlock()
								}

								// Extract amount (bytes 36-68)
								rawAmount := new(big.Int).SetBytes(triggerContract.Data[36:68])
								// Get converted amount
								amount, _ := decimals.RemoveDecimals(rawAmount, decimalsValue).Float64()

								localTxList = append(localTxList, &InboundTransaction{
									UserId:   int(userId),
									Currency: token,
									TxId:     txId,
									From:     fromAddr,
									To:       toAddr,
									Amount:   amount,
								})
							}
						}
					}
				}
			}

			// Mark the block as successfully processed
			successfulBlocks.Store(blockNum, true)
			// Send batch to result channel (one send for block)
			resultChan <- localTxList
		}()
	}

	// Waiting for results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results from batches
	txList := make([]*InboundTransaction, 0)
	for batch := range resultChan {
		txList = append(txList, batch...)
	}

	// Get the last continuously successful block
	// This ensures that we don't miss blocks the next time we run
	lastContinuousBlock := lastBlockNumber
	for blockNum := lastBlockNumber + 1; blockNum <= currentBlockNum; blockNum++ {
		if _, ok := successfulBlocks.Load(blockNum); ok {
			lastContinuousBlock = blockNum
		} else {
			// Stop, if found the first failed block
			log.Printf("[%s] Block %d was not processed successfully, stopping at %d",
				tron.NetworkCode, blockNum, lastContinuousBlock)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockNumber {
		err = redis.GetClient().HSet(ctx, "last_blocks", tron.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", tron.NetworkCode, err)
		}
	}
	log.Printf("[%s] Processed %d transactions successful", tron.NetworkCode, len(txList))

	return txList, nil
}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
// Returns: Derived address
func (tron *Tron) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get bytes from private key
	bytes, _ := hex.DecodeString(masterPrivateKey)
	// Create new master node from bytes for use in creating HD keychain
	masterHdKey, err := hdkeychain.NewMaster(bytes, &chaincfg.MainNetParams)
	if err != nil {
		return "", fmt.Errorf("failed to derive master HD key: %w", err)
	}

	// BIP44 derivation: m/44'/195'/0'/0/{index} (accountKey = 0)
	purposeKey, _ := masterHdKey.Derive(hdkeychain.HardenedKeyStart + 44)
	coinKey, _ := purposeKey.Derive(hdkeychain.HardenedKeyStart + 195)
	accountKey, _ := coinKey.Derive(hdkeychain.HardenedKeyStart + 0)
	changeKey, _ := accountKey.Derive(0)
	finalKey, err := changeKey.Derive(userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive final key: %w", err)
	}

	// Get btcec private key
	btcecPrivateKey, _ := finalKey.ECPrivKey()
	// Convert to ecdsa private key
	privateKeyEcdsa := btcecPrivateKey.ToECDSA()

	// Get ecdsa public key from ecdsa private key
	publicKey := privateKeyEcdsa.Public()
	publicKeyEcdsa, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("failed to convert to ECDSA public key")
	}

	// Get tron address from ecdsa public key
	addr, _ := tron.publicKeyToTronAddress(publicKeyEcdsa)
	return addr, nil
}

// @private publicKeyToTronAddress Get Tron address from HD-wallet public key
// Returns: Address in Tron base58 format
func (tron *Tron) publicKeyToTronAddress(publicKey *ecdsa.PublicKey) (string, error) {
	// Get bytes from public key and remove 0x04 prefix
	publicKeyBytes := crypto.FromECDSAPub(publicKey)[1:]

	// Get Keccak256 hash (standard Tron address generation)
	hash := crypto.Keccak256(publicKeyBytes)

	// Take last 20 bytes of hash
	addressBytes := hash[12:]

	// Add Tron prefix (0x41 for mainnet)
	addressWithPrefix := append([]byte{0x41}, addressBytes...)

	// Calculate checksum (double SHA256)
	checksum1 := sha256.Sum256(addressWithPrefix)
	checksum2 := sha256.Sum256(checksum1[:])

	// Append first 4 bytes of checksum
	finalBytes := append(addressWithPrefix, checksum2[:4]...)

	// Base58 encode
	return base58.Encode(finalBytes), nil
}

/* GetMasterWallet private methods group */
// @private getNativeBalance Get wallet TRX balance
// addr: Wallet address
// Returns: TRX wallet balance
func (tron *Tron) getNativeBalance(addr string) (float64, error) {
	// Get tron address instance
	tronAddr, err := address.Base58ToAddress(addr)
	if err != nil {
		return 0, err
	}

	// Get account instance by wallet address
	account, err := tron.Client.GetAccount(tronAddr.String())
	if err != nil {
		return 0, err
	}
	// Get raw balance (in SUN)
	rawBalance := big.NewInt(account.Balance)

	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", tron.NetworkCode, "TRX"),
	)
	// Get converted balance (in TRX)
	trxBalance, _ := decimals.RemoveDecimals(rawBalance, trxDecimals).Float64()

	return trxBalance, nil
}

// @private getTrc20Balance Get wallet TRC20 token balance
// contractAddr: Token contract address
// addr: Wallet address
// Returns: TRC20 token wallet balance
func (tron *Tron) getTrc20Balance(contractAddr, addr string) (float64, error) {
	// Get raw contract balance
	rawBalance, err := tron.Client.TRC20ContractBalance(addr, contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract balance: %w", err)
	}

	// Get contract decimals
	decimalsValue, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals: %w", err)
	}

	// Get converted balance
	convertedBalance, _ := decimals.RemoveDecimals(rawBalance, decimalsValue.Int64()).Float64()
	return convertedBalance, nil
}

/* GetTransactionCommission private methods group */
// @private calcNativeFee Calc fee for TRX transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in TRX
func (tron *Tron) calcNativeFee(fromAddr, toAddr string, amount float64) (float64, error) {
	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", tron.NetworkCode, "TRX"),
	)
	// Convert amount to SUN
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

// @private calcTrc20Fee Calc fee for TRC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value
// Returns: Fee in TRX
func (tron *Tron) calcTrc20Fee(currency, fromAddr, toAddr string, amount float64) (float64, error) {
	currency = strings.ToUpper(currency)

	// Get current chain params
	chainParams, err := tron.getCurrentChainParams()
	if err != nil {
		return 0, fmt.Errorf("failed to get current chain params: %w", err)
	}

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", tron.NetworkCode, currency),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract address for %s: %w", currency, err)
	}

	// Get contract decimals
	contractDecimals, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return 0, fmt.Errorf("failed to get contract decimals for %s: %w", currency, err)
	}
	// Convert amount to SUN with contract decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals.Int64())

	// Calc exact needed energy value
	var energyNeeded int64
	energyEstimate, err := tron.Client.EstimateEnergy(
		fromAddr, contractAddr,
		"transfer(address,uint256)",
		fmt.Sprintf(`[{"type":"address","value":"%s"},{"type":"uint256","value":"%s"}]`, toAddr, amountSun.String()),
		0, "", 0,
	)
	// If err returned, get needed energy value from config
	if err != nil {
		energyNeeded, err = config.GetChainSetting[int64](
			config.TokenSettings,
			fmt.Sprintf("%s.%s.estimated_energy", tron.NetworkCode, currency),
		)
		if err != nil {
			return 0, fmt.Errorf("failed to get estimated_energy for %s from config: %w", currency, err)
		}
	} else {
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
		fromAddr, contractAddr,
		"transfer(address,uint256)",
		fmt.Sprintf(`[{"type":"address","value":"%s"},{"type":"uint256","value":"%s"}]`, toAddr, amountSun.String()),
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

	// Get total fee in SUN
	totalFeeUnits := energyCost + bandwidthCost
	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", tron.NetworkCode, "TRX"),
	)
	// Convert total fee to TRX
	totalFee, _ := decimals.RemoveDecimals(big.NewInt(totalFeeUnits), trxDecimals).Float64()

	return totalFee, nil
}

// @private getCurrentChainParams Get current chain params (energyPrice and bandwidthPrice)
// Returns: Map of current chain params (getEnergyFee and getTransactionFee)
func (tron *Tron) getCurrentChainParams() (map[string]int64, error) {
	// Get current chain params
	params, err := tron.Client.Client.GetChainParameters(context.Background(), &api.EmptyMessage{})
	if err != nil {
		return nil, fmt.Errorf("failed to get chain parameters: %w", err)
	}

	// Get only energyPrice and bandwidthPrice from params structure
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
// @private sendNative Send native TRX transaction
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value (in TRX)
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) sendNative(fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	// Get TRX balance
	trxBalance, err := tron.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX balance: %w", err)
	}
	// Get tx fee (in TRX)
	txFee, err := tron.calcNativeFee(fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX tx fee: %w", err)
	}
	// Check TRX balance before sending
	if trxBalance < amount+txFee {
		return "", fmt.Errorf("insufficient balance: have %f %s, need %f + %f %s", trxBalance, "TRX", amount, txFee, "TRX")
	}

	// Get TRX decimals
	trxDecimals, _ := config.GetChainSetting[int64](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", tron.NetworkCode, "TRX"),
	)
	// Convert amount to SUN including decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), trxDecimals)

	// Create TRX transaction
	tx, err := tron.Client.Transfer(fromAddr, toAddr, amountSun.Int64())
	if err != nil {
		return "", fmt.Errorf("failed to create TRX transaction: %v", err)
	}

	// Sign and send transaction to the network
	return tron.signAndSend(tx, privateKey)
}

// @private sendTrc20 Send TRC20 token transaction
// currency: Token ticker
// fromAddr: Wallet address
// toAddr: Address for withdrawal
// amount: Amount in float value (in currency value)
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) sendTrc20(currency, fromAddr, toAddr string, amount float64, privateKey string) (string, error) {
	currency = strings.ToUpper(currency)

	// Get token contract address
	contractAddr, err := config.GetChainSetting[string](
		config.ContractsAddresses,
		fmt.Sprintf("%s.%s", tron.NetworkCode, currency),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get contract address: %w", err)
	}

	// Get TRX and token balances
	trxBalance, err := tron.getNativeBalance(fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get TRX balance: %w", err)
	}
	trc20Balance, err := tron.getTrc20Balance(contractAddr, fromAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get %s balance: %w", currency, err)
	}
	// Get tx fee (in TRX)
	txFee, err := tron.calcTrc20Fee(currency, fromAddr, toAddr, amount)
	if err != nil {
		return "", fmt.Errorf("failed to get %s tx fee: %w", currency, err)
	}
	// Check TRX and currency balances before sending
	if trxBalance < txFee {
		return "", fmt.Errorf("insufficient TRX balance: have %f %s, need %f %s", trxBalance, "TRX", txFee, "TRX")
	}
	if trc20Balance < amount {
		return "", fmt.Errorf("insufficient currency balance: have %f %s, need %f %s", trc20Balance, currency, amount, currency)
	}

	// Get contract decimals
	contractDecimals, err := tron.Client.TRC20GetDecimals(contractAddr)
	if err != nil {
		return "", fmt.Errorf("failed to get contract decimals: %w", err)
	}
	// Convert amount to SUN with contract decimals
	amountSun, _ := decimals.ApplyDecimals(big.NewFloat(amount), contractDecimals.Int64())

	// Create TRC20 transaction
	tx, err := tron.Client.TRC20Send(fromAddr, toAddr, contractAddr, amountSun, 0)
	if err != nil {
		return "", fmt.Errorf("failed to create USDT transaction: %v", err)
	}

	// Sign and send transaction to the network
	return tron.signAndSend(tx, privateKey)
}

// @private signAndSend Sign and send transaction to the network
// tx: Transaction struct
// privateKey: Wallet private key
// Returns: Transaction hash
func (tron *Tron) signAndSend(tx *api.TransactionExtention, privateKey string) (string, error) {
	// Convert hex private key string to *ecdsa.PrivateKey
	ecdsaPk, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse %s wallet private key: %w", tron.NetworkCode, err)
	}

	// Sign transaction
	signedTx, err := transaction.SignTransactionECDSA(tx.Transaction, ecdsaPk)
	if err != nil {
		return "", fmt.Errorf("failed to sign %s transaction: %w", tron.NetworkCode, err)
	}

	// Broadcast transaction
	result, err := tron.Client.Broadcast(signedTx)
	if err != nil {
		return "", fmt.Errorf("failed to broadcast %s transaction: %w", tron.NetworkCode, err)
	}

	// Check result and return transaction hash
	if !result.Result || result.Code != api.Return_SUCCESS {
		return "", fmt.Errorf("%s transaction failed: [%d] %s", tron.NetworkCode, result.Code, result.Message)
	}
	return hex.EncodeToString(tx.Txid), nil
}
