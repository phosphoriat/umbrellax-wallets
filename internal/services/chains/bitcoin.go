package chains

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"sync"

	"wallets/internal/config"
	"wallets/internal/database/redis"
	"wallets/internal/database/vault"
	"wallets/internal/repository"
	"wallets/internal/services/api/bybit"
	"wallets/internal/utils"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/tyler-smith/go-bip39"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

type Bitcoin struct {
	BaseChainService
	Client      *rpcclient.Client
	RateLimit   int
	ChainParams *chaincfg.Params
}

// Global package vars
var (
	btcInstance *Bitcoin
	btcOnce     sync.Once
)

// Global package constants
const (
	BtcBaseTxSize       = 10 // Base transaction size (version (4) + locktime (4) + segwit marker/flag (2))
	BtcInputSize        = 68 // Input size (outpoint (36) + scriptSig (1) + sequence (4) + witness (~27 vbytes))
	BtcOutputSizeP2WPKH = 31 // Native SegWit output size (P2WPKH) (value (8) + scriptPubKey length (1) + P2WPKH script (22))
	BtcOutputSizeP2PKH  = 34 // Legacy output size (value (8) + scriptPubKey length (1) + P2PKH script (25))
)

// GetBitcoin Get Bitcoin chain instance
func GetBitcoin() *Bitcoin {
	btcOnce.Do(func() {
		btcInstance = newBitcoin()
	})
	return btcInstance
}

// @private newBitcoin Create new instance of Bitcoin (private constructor)
func newBitcoin() *Bitcoin {
	// Load config
	cfg, _ := config.Load()

	// Create new RPC client (with local full node connection)
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:         cfg.BtcNodeUrl,
		User:         cfg.BtcNodeUser,
		Pass:         cfg.BtcNodePass,
		HTTPPostMode: true,
		DisableTLS:   true,
	}, nil)
	if err != nil {
		log.Fatalf("[Bitcoin] Failed to connect to RPC API: %v", err)
	}

	// Get network settings
	networkCode := "bitcoin"
	nativeToken := config.NetworkSettings[networkCode].NativeToken

	return &Bitcoin{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: nativeToken,
		},
		Client:      client,
		RateLimit:   cfg.QuickNodeRps,
		ChainParams: &chaincfg.MainNetParams,
	}
}

// GetCode Get network code
func (btc *Bitcoin) GetCode() string {
	return btc.NetworkCode
}

// GetUserWalletAddress Get user HD-wallet by user id
// userId: Unique user id
// Returns: User HD-wallet address
func (btc *Bitcoin) GetUserWalletAddress(userId uint32) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + btc.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", fmt.Errorf("mnemonic not found in Vault")
	}

	addr, _, err := btc.deriveWallet(mnemonic, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive address for user %d: %w", userId, err)
	}

	return addr, nil
}

// GetUserWallet Get user wallet info (address and token balances)
// userId: Unique user id
// Returns: Map of user wallet info (address and balances of all available tokens)
func (btc *Bitcoin) GetUserWallet(userId uint32) (map[string]any, error) {
	// Get user wallet address
	userAddr, _, err := btc.getWallet("user", &userId)
	if err != nil {
		return nil, fmt.Errorf("failed to get user wallet address: %w", err)
	}

	// Parse and validate user address
	addr, err := btcutil.DecodeAddress(userAddr, btc.ChainParams)
	if err != nil {
		return nil, fmt.Errorf("invalid user address: %w", err)
	}

	// Get UXTO for the user address
	unspentList, err := btc.Client.ListUnspentMinMaxAddresses(0, 9999999, []btcutil.Address{addr})
	if err != nil {
		return nil, fmt.Errorf("failed to list unspent: %w", err)
	}

	// Calculate total balance from UTXOs
	var totalBalance btcutil.Amount
	for _, utxo := range unspentList {
		amount, err := btcutil.NewAmount(utxo.Amount)
		if err != nil {
			continue
		}
		totalBalance += amount
	}
	// Get converted balance (in BTC)
	btcBalance := totalBalance.ToBTC()

	return map[string]any{
		"address": userAddr,
		"balance": btcBalance,
	}, nil
}

// GetMasterWallet Get master wallet info (address and token balance)
// Returns: Map of master wallet info (address and balances of all available tokens)
func (btc *Bitcoin) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + btc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr, ok := masterData["address"].(string)
	if !ok || masterAddr == "" {
		return nil, fmt.Errorf("master address not found in Vault")
	}

	// Parse and validate master address
	addr, err := btcutil.DecodeAddress(masterAddr, btc.ChainParams)
	if err != nil {
		return nil, fmt.Errorf("invalid master address: %w", err)
	}

	// Get UXTO for the address
	unspentList, err := btc.Client.ListUnspentMinMaxAddresses(0, 9999999, []btcutil.Address{addr})
	if err != nil {
		return nil, fmt.Errorf("failed to list unspent: %w", err)
	}

	// Calculate total balance from UTXOs
	var totalBalance btcutil.Amount
	for _, utxo := range unspentList {
		amount, err := btcutil.NewAmount(utxo.Amount)
		if err != nil {
			continue
		}
		totalBalance += amount
	}
	// Get converted balance (in BTC)
	btcBalance := totalBalance.ToBTC()

	return map[string]any{
		"address": masterAddr,
		"balance": btcBalance,
	}, nil
}

// GetTransactionCommission Get transaction commission
// oType: Operation type (withdrawal/inner)
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Map of all fee types (chain_fee, bybit_fee, aml_fee)
func (btc *Bitcoin) GetTransactionCommission(oType, wType string, wId *uint32, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	// Get wallet address by type and id
	walletAddr, _, err := btc.getWallet(wType, wId)
	if err != nil {
		return nil, err
	}

	// Parse and validate from address (must be Native SegWit)
	fromAddress, err := btcutil.DecodeAddress(walletAddr, btc.ChainParams)
	if err != nil {
		return nil, fmt.Errorf("failed to parse from address: %w", err)
	}
	if _, ok := fromAddress.(*btcutil.AddressWitnessPubKeyHash); !ok {
		return nil, fmt.Errorf("from address must be Native SegWit (bc1q...)")
	}

	// Convert amount to Satoshi
	amountSatoshi, err := btcutil.NewAmount(amount)
	if err != nil {
		return nil, fmt.Errorf("failed to convert amount: %w", err)
	}

	// Get UTXO data (returns fee estimate even without UTXOs)
	_, _, estimatedFee, err := btc.getUTXOData(fromAddress, amountSatoshi)
	if err != nil {
		return nil, fmt.Errorf("failed to get UTXO data: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Load config
		cfg, _ := config.Load()

		// Get bybit withdrawal fee (in BTC value)
		bybitFee, _ = bybit.GetClient().GetWithdrawalFee(context.Background(), currency, btc.NetworkCode)
		// Get AML check cost from config (in USD value)
		amlFee = cfg.AmlCheckCost
	}

	return map[string]float64{
		"chain_fee": estimatedFee.ToBTC(), // In BTC
		"bybit_fee": bybitFee,             // In BTC
		"aml_fee":   amlFee,               // in USD
	}, nil
}

// GetTransactionStatus Get transaction status by hash
// hash: Transaction hash
// Returns: Transaction status (success/pending/failed)
func (btc *Bitcoin) GetTransactionStatus(hash, toAddr string) (string, error) {
	// Parse transaction hash
	txHash, err := chainhash.NewHashFromStr(hash)
	if err != nil {
		return "", fmt.Errorf("invalid transaction hash: %w", err)
	}

	// Get transaction with verbose information
	txVerbose, err := btc.Client.GetRawTransactionVerbose(txHash)
	if err != nil {
		// Transaction not found or error
		return "failed", nil
	}

	// Get confirmations count
	confirmations := txVerbose.Confirmations

	// Determine status based on confirmations
	var status string
	if confirmations >= 1 {
		status = "success" // At least 1 confirmation
	} else {
		status = "pending" // In mempool (0 confirmations)
	}

	return status, nil
}

// SendTransaction Send transaction to the network
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// currency: Token ticker
// toAddr: Withdrawal to address
// amount: Amount in float value
// memo: Memo tag
// Returns: Transaction hash
func (btc *Bitcoin) SendTransaction(wType string, wId *uint32, currency, to string, amount float64, memo string) (string, error) {
	// Get wallet address and private key by type and id
	walletAddr, walletKey, err := btc.getWallet(wType, wId)
	if err != nil {
		return "", err
	}

	// Parse private key (WIF format)
	wif, err := btcutil.DecodeWIF(walletKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}

	// Parse and validate from address (must be Native SegWit)
	fromAddress, err := btcutil.DecodeAddress(walletAddr, btc.ChainParams)
	if err != nil {
		return "", fmt.Errorf("failed to parse from address: %w", err)
	}
	if _, ok := fromAddress.(*btcutil.AddressWitnessPubKeyHash); !ok {
		return "", fmt.Errorf("from address must be Native SegWit (bc1q...)")
	}
	// Parse to address
	toAddress, err := btcutil.DecodeAddress(to, btc.ChainParams)
	if err != nil {
		return "", fmt.Errorf("failed to parse to address: %w", err)
	}

	// Get converted amount (in Satoshi)
	amountSatoshi, err := btcutil.NewAmount(amount)
	if err != nil {
		return "", fmt.Errorf("failed to convert amount: %w", err)
	}

	// Get UTXO data
	selectedUTXOs, totalInput, estimatedFee, err := btc.getUTXOData(fromAddress, amountSatoshi)
	if err != nil {
		return "", fmt.Errorf("failed to get UTXO data: %w", err)
	}

	// Check wallet BTC balance before sending
	if totalInput < amountSatoshi+estimatedFee {
		return "", fmt.Errorf("insufficient BTC balance: have %f BTC, need %f + %f BTC",
			totalInput.ToBTC(), amountSatoshi.ToBTC(), estimatedFee.ToBTC())
	}

	// Fetch previous transactions and store outputs
	prevOuts := make(map[wire.OutPoint]*wire.TxOut)
	for _, utxo := range selectedUTXOs {
		prevTxHash, err := chainhash.NewHashFromStr(utxo.TxID)
		if err != nil {
			return "", fmt.Errorf("failed to parse previous tx hash: %w", err)
		}

		prevTx, err := btc.Client.GetRawTransaction(prevTxHash)
		if err != nil {
			return "", fmt.Errorf("failed to get previous transaction: %w", err)
		}

		outPoint := wire.OutPoint{
			Hash:  *prevTxHash,
			Index: utxo.Vout,
		}
		prevOuts[outPoint] = prevTx.MsgTx().TxOut[utxo.Vout]
	}
	// Create PrevOutputFetcher
	prevOutFetcher := txscript.NewMultiPrevOutFetcher(prevOuts)

	// Create new transaction (For SegWit transactions version 2 is recommended)
	tx := wire.NewMsgTx(2)

	// Add inputs
	for _, utxo := range selectedUTXOs {
		hash, _ := chainhash.NewHashFromStr(utxo.TxID)
		outPoint := wire.NewOutPoint(hash, utxo.Vout)
		txIn := wire.NewTxIn(outPoint, nil, nil)
		txIn.Sequence = wire.MaxTxInSequenceNum - 2
		tx.AddTxIn(txIn)
	}

	// Add recipient output
	toScript, err := txscript.PayToAddrScript(toAddress)
	if err != nil {
		return "", fmt.Errorf("failed to create output script: %w", err)
	}
	tx.AddTxOut(wire.NewTxOut(int64(amountSatoshi), toScript))

	// Add change output if above dust (dust threshold ~546 satoshi)
	change := totalInput - amountSatoshi - estimatedFee
	dustThreshold, _ := btcutil.NewAmount(0.00000546)
	if change > dustThreshold {
		changeScript, err := txscript.PayToAddrScript(fromAddress)
		if err != nil {
			return "", fmt.Errorf("failed to create change script: %w", err)
		}
		tx.AddTxOut(wire.NewTxOut(int64(change), changeScript))
	}

	// Sign all inputs (P2WPKH)
	sigHashes := txscript.NewTxSigHashes(tx, prevOutFetcher)
	pubKey := wif.PrivKey.PubKey()
	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())

	witnessScript, _ := txscript.NewScriptBuilder().
		AddOp(txscript.OP_DUP).
		AddOp(txscript.OP_HASH160).
		AddData(pubKeyHash).
		AddOp(txscript.OP_EQUALVERIFY).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	for i, utxo := range selectedUTXOs {
		prevTxHash, _ := chainhash.NewHashFromStr(utxo.TxID)
		outPoint := wire.OutPoint{Hash: *prevTxHash, Index: utxo.Vout}
		prevOutValue := prevOuts[outPoint].Value

		sig, err := txscript.RawTxInWitnessSignature(
			tx, sigHashes, i, prevOutValue,
			witnessScript, txscript.SigHashAll, wif.PrivKey,
		)
		if err != nil {
			return "", fmt.Errorf("failed to sign input %d: %w", i, err)
		}

		tx.TxIn[i].Witness = wire.TxWitness{
			sig,
			pubKey.SerializeCompressed(),
		}
	}

	// Send transaction
	txHash, err := btc.Client.SendRawTransaction(tx, false)
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	// Return transaction hash
	return txHash.String(), nil
}

// GetInboundTransactions Get new inbound transactions list
// Returns: Inbound transactions list
func (btc *Bitcoin) GetInboundTransactions(ctx context.Context) ([]*InboundTransaction, error) {
	// Create rate limiter
	limiter := rate.NewLimiter(rate.Limit(btc.RateLimit), 1)

	// Get current block height
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}
	currentBlockHeight, err := btc.Client.GetBlockCount()
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block height: %w", btc.NetworkCode, err)
	}

	// Get last block height from Redis
	lastBlockStr, err := redis.GetClient().HGet(ctx, "last_blocks", btc.NetworkCode)
	var lastBlockHeight int64
	if err != nil || lastBlockStr == "" {
		lastBlockHeight = currentBlockHeight - 1
		log.Printf("[%s] First run, starting from block %d (current: %d)",
			btc.NetworkCode, lastBlockHeight, currentBlockHeight)
	} else {
		lastBlockHeight, _ = strconv.ParseInt(lastBlockStr, 10, 64)
	}

	// Check if current block not equal or less, than last block
	if currentBlockHeight <= lastBlockHeight {
		log.Printf("[%s] No new blocks to process", btc.NetworkCode)
		return []*InboundTransaction{}, nil
	}

	// Get all addresses for network from DB
	addressList, err := btc.WalletRepo.GetAddressListByNetwork(btc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get wallet address list: %w", btc.NetworkCode, err)
	}

	// Get BTC decimals from config
	btcDecimals := config.TokenSettings[btc.NetworkCode]["BTC"].Decimals

	log.Printf("[%s] Processing %d blocks from %d to %d",
		btc.NetworkCode, currentBlockHeight-lastBlockHeight, lastBlockHeight+1, currentBlockHeight)

	var (
		// Wait group to wait for all tx blocks goroutines to complete
		wg sync.WaitGroup
		// Semaphore for goroutines limit
		sem = semaphore.NewWeighted(20)
		// Concurrent save map for successfully processed blocks
		successfulBlocks = &sync.Map{}
		// Channel for collect transactions from all blocks
		resultChan = make(chan []*InboundTransaction, currentBlockHeight-lastBlockHeight)
		// Goroutines cancellation context
		cancelCtx, cancel = context.WithCancel(ctx)
		// First error flag
		errOnce  sync.Once
		firstErr error
	)
	defer cancel()

	// Process all new blocks in separate goroutines
	for blockHeight := lastBlockHeight + 1; blockHeight <= currentBlockHeight; blockHeight++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Check context cancellation
			if cancelCtx.Err() != nil {
				return
			}

			// Parallel execution limit
			if err := sem.Acquire(cancelCtx, 1); err != nil {
				return
			}
			defer sem.Release(1)

			// Get block hash by height
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", btc.NetworkCode, blockHeight, err)
				return
			}
			blockHash, err := btc.Client.GetBlockHash(blockHeight)
			if err != nil {
				log.Printf("[%s] Failed to get block hash for height %d: %v", btc.NetworkCode, blockHeight, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			// Get block by hash
			if err := limiter.Wait(cancelCtx); err != nil {
				log.Printf("[%s] Rate limit error for block %d: %v", btc.NetworkCode, blockHeight, err)
				return
			}
			block, err := btc.Client.GetBlock(blockHash)
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", btc.NetworkCode, blockHeight, err)
				// Cancel all another goroutines
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			log.Printf("[%s] Processing block %d with %d raw transactions",
				btc.NetworkCode, blockHeight, len(block.Transactions))

			// Local goroutine tx list
			localTxList := make([]*InboundTransaction, 0)

			// Process transactions in block
			for _, tx := range block.Transactions {
				// Skip, if it's coinbase transaction (first tx in block with special input)
				if len(tx.TxIn) == 1 && tx.TxIn[0].PreviousOutPoint.Index == 0xffffffff {
					continue
				}

				// Get tx hash
				txHash := tx.TxHash().String()

				// Lazy from address resolution (once per transaction)
				var fromAddr string
				var fromAddrResolved bool

				// Iterate all outputs in transaction
				for _, vout := range tx.TxOut {
					// Skip zero-value outputs
					if vout.Value == 0 {
						continue
					}

					// Extract addresses from script
					_, addresses, _, err := txscript.ExtractPkScriptAddrs(vout.PkScript, btc.ChainParams)
					if err != nil {
						continue
					}

					// Iterate all addresses from transaction
					for _, addr := range addresses {
						addrStr := addr.String()

						// Check if address exists in network addresses list
						if userId, ok := addressList[addrStr]; ok {
							// Resolve from address only once per transaction
							if !fromAddrResolved {
								fromAddrResolved = true

								if len(tx.TxIn) > 0 {
									// Get first input
									txIn := tx.TxIn[0]
									prevTxHash := &txIn.PreviousOutPoint.Hash
									prevOutIndex := txIn.PreviousOutPoint.Index

									// Get previous transaction
									if err := limiter.Wait(cancelCtx); err != nil {
										return
									}
									prevTx, err := btc.Client.GetRawTransaction(prevTxHash)

									// Try to find a specific output from previous transaction
									if err == nil && int(prevOutIndex) < len(prevTx.MsgTx().TxOut) {
										prevOut := prevTx.MsgTx().TxOut[prevOutIndex]
										// Extract from address from output
										_, prevAddresses, _, err := txscript.ExtractPkScriptAddrs(prevOut.PkScript, btc.ChainParams)
										if err == nil && len(prevAddresses) > 0 {
											fromAddr = prevAddresses[0].String()
										}
									}
								}
							}

							// Get converted amount (in BTC)
							amount := utils.RemoveDecimals(big.NewInt(vout.Value), int64(btcDecimals))

							localTxList = append(localTxList, &InboundTransaction{
								UserId:   userId,
								Currency: "BTC",
								TxId:     txHash,
								From:     fromAddr,
								To:       addrStr,
								Amount:   amount,
							})

							break
						}
					}
				}
			}

			// Mark the block as successfully processed
			successfulBlocks.Store(blockHeight, true)
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
	lastContinuousBlock := lastBlockHeight
	for blockHeight := lastBlockHeight + 1; blockHeight <= currentBlockHeight; blockHeight++ {
		if _, ok := successfulBlocks.Load(blockHeight); ok {
			lastContinuousBlock = blockHeight
		} else {
			log.Printf("[%s] Block %d was not processed successfully, stopping at %d: %v",
				btc.NetworkCode, blockHeight, lastContinuousBlock, firstErr)
			break
		}
	}

	// Save last processed block in Redis
	if lastContinuousBlock > lastBlockHeight {
		err = redis.GetClient().HSet(ctx, "last_blocks", btc.NetworkCode, lastContinuousBlock)
		if err != nil {
			return nil, fmt.Errorf("[%s] Failed to save last block in Redis: %w", btc.NetworkCode, err)
		}
	}

	log.Printf("[%s] Processing finished successfully: processed %d blocks, found %d user transactions",
		btc.NetworkCode, lastContinuousBlock-lastBlockHeight, len(txList))
	return txList, nil
}

/* GetUserWallet private methods group */
// @private getWallet Get HD-wallet by index
// wType: Wallet type (master/user)
// wId: User id (only for user wType)
// Returns: Derived address and private key
func (btc *Bitcoin) getWallet(wType string, wId *uint32) (string, string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("master/" + btc.NetworkCode)
	if err != nil {
		return "", "", fmt.Errorf("failed to get master data: %w", err)
	}
	mnemonic, ok := masterData["mnemonic"].(string)
	if !ok || mnemonic == "" {
		return "", "", fmt.Errorf("mnemonic not found in vault")
	}

	// Derive wallet address and private key from master mnemonic
	var index uint32
	if wType == "master" {
		index = 0
	} else {
		if wId == nil {
			return "", "", fmt.Errorf("user id is required")
		}
		index = *wId
	}

	return btc.deriveWallet(mnemonic, index)
}

// @private deriveWallet Derive HD-wallet by derivation index from master wallet mnemonic
// mnemonic: Master wallet mnemonic
// userId: Unique user id for derivation index
// Returns: Derived address and private key
func (btc *Bitcoin) deriveWallet(mnemonic string, userId uint32) (string, string, error) {
	// Convert mnemonic to seed
	seed := bip39.NewSeed(mnemonic, "")

	// Create HD master key from seed
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return "", "", fmt.Errorf("failed to create master key: %w", err)
	}

	// BIP84 derivation path: m/84'/0'/0'/0/{userId}
	path := []uint32{
		hdkeychain.HardenedKeyStart + 84,
		hdkeychain.HardenedKeyStart + 0,
		hdkeychain.HardenedKeyStart + 0,
		0,
		userId,
	}
	key := masterKey
	for _, idx := range path {
		key, err = key.Derive(idx)
		if err != nil {
			return "", "", fmt.Errorf("failed to derive key: %w", err)
		}
	}

	// Get private key
	privateKey, err := key.ECPrivKey()
	if err != nil {
		return "", "", fmt.Errorf("failed to get private key: %w", err)
	}

	// Get Native SegWit (P2WPKH) address (bc1...)
	witnessProg := btcutil.Hash160(privateKey.PubKey().SerializeCompressed())
	addr, err := btcutil.NewAddressWitnessPubKeyHash(witnessProg, &chaincfg.MainNetParams)
	if err != nil {
		return "", "", fmt.Errorf("failed to create Native SegWit address: %w", err)
	}

	// Export private key as WIF
	wif, err := btcutil.NewWIF(privateKey, &chaincfg.MainNetParams, true)
	if err != nil {
		return "", "", fmt.Errorf("failed to create WIF: %w", err)
	}

	return addr.EncodeAddress(), wif.String(), nil
}

// @private getUTXOData Get UTXO data
// fromAddress: Wallet from address
// amountSatoshi: Amount in Satoshi
// Returns: selectedUTXOs, totalInput, estimatedFee, error
func (btc *Bitcoin) getUTXOData(fromAddress btcutil.Address, amountSatoshi btcutil.Amount) ([]btcjson.ListUnspentResult, btcutil.Amount, btcutil.Amount, error) {
	// Get fee rate (satoshi/vBytes)
	feeResult, err := btc.Client.EstimateSmartFee(6, &btcjson.EstimateModeConservative)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to estimate fee: %w", err)
	}
	if feeResult.FeeRate == nil || *feeResult.FeeRate <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid fee rate returned")
	}
	feeRateBTCPerKB := *feeResult.FeeRate

	// Get UTXOs for from address (min 1 confirmation for security)
	unspentList, err := btc.Client.ListUnspentMinMaxAddresses(1, 9999999, []btcutil.Address{fromAddress})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to get UTXO list: %w", err)
	}
	// Fallback - if no UTXOs, estimate fee for typical transaction
	if len(unspentList) == 0 {
		// Typical P2WPKH tx: 1 input + 2 outputs ≈ 140 vBytes
		txVSize := 140
		estimatedFee, _ := btcutil.NewAmount(float64(txVSize) / 1000 * feeRateBTCPerKB)
		return nil, 0, estimatedFee, nil // Return only fee estimate
	}

	// Sort UTXOs by amount descending (larger first = fewer inputs = lower fee)
	sort.Slice(unspentList, func(i, j int) bool {
		return unspentList[i].Amount > unspentList[j].Amount
	})

	// Assume 2 outputs: recipient + change (both P2WPKH for estimation)
	numOutputs := 2
	outputsSize := numOutputs * BtcOutputSizeP2WPKH

	// Select UTXOs needed to cover amount + estimated fee
	var selectedUTXOs []btcjson.ListUnspentResult
	var totalInput btcutil.Amount
	var estimatedFee btcutil.Amount

	for _, utxo := range unspentList {
		utxoAmount, err := btcutil.NewAmount(utxo.Amount)
		if err != nil {
			continue
		}

		selectedUTXOs = append(selectedUTXOs, utxo)
		totalInput += utxoAmount

		// Calculate virtual size
		txVSize := BtcBaseTxSize + (len(selectedUTXOs) * BtcInputSize) + outputsSize
		// Fee = vsize * feeRate (convert from BTC/kB to BTC)
		estimatedFee, _ = btcutil.NewAmount(float64(txVSize) / 1000 * feeRateBTCPerKB)

		// Check if total input is enough to cover amount + fee
		if totalInput >= amountSatoshi+estimatedFee {
			break
		}
	}

	return selectedUTXOs, totalInput, estimatedFee, nil
}
