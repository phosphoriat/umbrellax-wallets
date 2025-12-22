package chains

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
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

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type Bitcoin struct {
	BaseChainService
	Client      *rpcclient.Client
	ChainParams *chaincfg.Params
}

// Global package vars
var (
	btcInstance *Bitcoin
	btcOnce     sync.Once
)

// Global constants
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

	// Create RPC client with default options
	client, err := rpcclient.New(&rpcclient.ConnConfig{
		Host:         cfg.BtcNodeUrl,
		User:         cfg.BtcNodeUser,
		Pass:         cfg.BtcNodePass,
		HTTPPostMode: true,
		DisableTLS:   false,
	}, nil)
	if err != nil {
		log.Fatalf("[Bitcoin] Failed to connect to RPC API: %v", err)
	}

	networkCode := "bitcoin"
	// Get network settings
	networkSettings := config.NetworkSettings[networkCode].(map[string]any)

	// @TODO Remove
	// Test query
	log.Printf("[Bitcoin] Testing connection...")
	height, err := client.GetBlockCount()
	if err != nil {
		log.Printf("[Bitcoin] Connection test failed: %v", err)
	}
	log.Printf("[Bitcoin] Connected successfully, block height: %d", height)

	return &Bitcoin{
		BaseChainService: BaseChainService{
			WalletRepo:  repository.NewWalletRepository(),
			NetworkCode: networkCode,
			NativeToken: networkSettings["native_token"].(string),
		},
		Client:      client,
		ChainParams: &chaincfg.MainNetParams,
	}
}

// GetCode Get network code
func (btc *Bitcoin) GetCode() string {
	return btc.NetworkCode
}

// GetUserWallet Get user HD-wallet by user id
// userId: Unique user id
func (btc *Bitcoin) GetUserWallet(userId int) (string, error) {
	// Get master wallet data from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + btc.NetworkCode)
	if err != nil {
		return "", fmt.Errorf("failed to get master data: %w", err)
	}

	// Derive user address from master private key
	derivedAddr, err := btc.deriveAddress(masterData["privateKey"].(string), uint32(userId))
	if err != nil {
		return "", err
	}

	return derivedAddr, nil
}

// GetMasterWallet Get master wallet info (address and token balance)
func (btc *Bitcoin) GetMasterWallet() (map[string]any, error) {
	// Get master wallet address from Vault
	masterData, err := vault.GetClient().GetSecret("/master/" + btc.NetworkCode)
	if err != nil {
		return nil, fmt.Errorf("failed to get master data: %w", err)
	}
	masterAddr := masterData["address"].(string)

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
func (btc *Bitcoin) GetTransactionCommission(oType, wType string, wId *string, currency, toAddr string, amount float64, memo string) (map[string]float64, error) {
	var walletPath string
	if wType == "master" {
		walletPath = "/master/" + btc.NetworkCode
	} else {
		if wId == nil {
			return nil, fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, btc.NetworkCode)
	}

	// Get wallet data from Vault
	walletData, err := vault.GetClient().GetSecret(walletPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet data: %w", err)
	}
	// Extract wallet address
	fromAddr, ok := walletData["address"].(string)
	if !ok || fromAddr == "" {
		return nil, fmt.Errorf("invalid or missing %s wallet address in Vault", btc.NetworkCode)
	}

	// Parse and validate from address (must be Native SegWit)
	fromAddress, err := btcutil.DecodeAddress(fromAddr, btc.ChainParams)
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

	// Get UTXO data
	_, _, estimatedFee, err := btc.getUTXOData(fromAddress, amountSatoshi)
	if err != nil {
		return nil, fmt.Errorf("failed to get UTXO data: %w", err)
	}

	var bybitFee, amlFee float64
	if oType == "withdrawal" {
		// Get bybit withdrawal fee (in BTC value)
		bybitFee, _ = bybit.GetHTTPClient().GetWithdrawalFee(currency, btc.NetworkCode)

		// Get AML check cost from config (in USD value)
		cfg, _ := config.Load()
		amlFee, _ = strconv.ParseFloat(cfg.AmlCheckCost, 64)
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
func (btc *Bitcoin) SendTransaction(wType string, wId *string, currency, to string, amount float64, memo string) (string, error) {
	var walletPath string
	if wType == "master" {
		walletPath = fmt.Sprintf("/master/%s", btc.NetworkCode)
	} else {
		if wId == nil {
			return "", fmt.Errorf("wId is required")
		}
		walletPath = fmt.Sprintf("/users/%s/%s", *wId, btc.NetworkCode)
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
		return "", fmt.Errorf("invalid or missing %s wallet data in Vault", btc.NetworkCode)
	}

	// Parse private key (WIF format)
	wif, err := btcutil.DecodeWIF(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}

	// Parse and validate from address (must be Native SegWit)
	fromAddress, err := btcutil.DecodeAddress(fromAddr, btc.ChainParams)
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
func (btc *Bitcoin) GetInboundTransactions() ([]*InboundTransaction, error) {
	ctx := context.Background()

	// Get current block height
	currentBlockHeight, err := btc.Client.GetBlockCount()
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get current block height: %w", btc.NetworkCode, err)
	}

	// Get last block height from Redis
	lastBlockStr, err := redis.GetClient().HGet(ctx, "last_blocks", btc.NetworkCode)
	var lastBlockHeight int64
	if err != nil || lastBlockStr == "" {
		lastBlockHeight = currentBlockHeight - 10
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
	btcDecimals, err := config.GetChainSetting[int](
		config.TokenSettings,
		fmt.Sprintf("%s.%s.decimals", btc.NetworkCode, "BTC"),
	)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to get BTC decimals from config: %w", btc.NetworkCode, err)
	}

	// Wait group to wait for all block goroutines to complete
	var wg sync.WaitGroup
	// Concurrent save map for successfully processed blocks
	successfulBlocks := &sync.Map{}
	// Channel for collect transactions from all blocks
	resultChan := make(chan []*InboundTransaction, currentBlockHeight-lastBlockHeight)

	log.Printf("[%s] Processing blocks %d to %d", btc.NetworkCode, lastBlockHeight+1, currentBlockHeight)

	// Check all new blocks
	for blockHeight := lastBlockHeight + 1; blockHeight <= currentBlockHeight; blockHeight++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Get block hash by height
			blockHash, err := btc.Client.GetBlockHash(blockHeight)
			if err != nil {
				log.Printf("[%s] Failed to get block hash for height %d: %v", btc.NetworkCode, blockHeight, err)
				resultChan <- []*InboundTransaction{}
				return
			}

			// Get block by hash
			block, err := btc.Client.GetBlock(blockHash)
			if err != nil {
				log.Printf("[%s] Failed to get block %d: %v", btc.NetworkCode, blockHeight, err)
				resultChan <- []*InboundTransaction{}
				return
			}

			log.Printf("[%s] Processing block %d with %d transactions",
				btc.NetworkCode, blockHeight, len(block.Transactions))

			// Local goroutine tx list
			localTxList := make([]*InboundTransaction, 0)

			// Process transactions in block
			for _, tx := range block.Transactions {
				// Check if it's coinbase transaction and skip it (first tx in block with special input)
				if len(tx.TxIn) == 1 && tx.TxIn[0].PreviousOutPoint.Index == 0xffffffff {
					continue
				}

				// Get tx hash
				txHash := tx.TxHash().String()

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
							// Get from address
							var fromAddr string

							if len(tx.TxIn) > 0 {
								// Get first input
								txIn := tx.TxIn[0]
								prevTxHash := txIn.PreviousOutPoint.Hash.String()
								prevOutIndex := txIn.PreviousOutPoint.Index

								// Get previous transaction
								prevTxHashObj := &txIn.PreviousOutPoint.Hash
								prevTx, err := btc.Client.GetRawTransaction(prevTxHashObj)

								if err != nil {
									log.Printf("[%s] Failed to get previous transaction %s: %v",
										btc.NetworkCode, prevTxHash, err)
									fromAddr = ""
								} else {
									// Find a specific output from previous transaction
									if int(prevOutIndex) < len(prevTx.MsgTx().TxOut) {
										prevOut := prevTx.MsgTx().TxOut[prevOutIndex]

										// Extract address from output
										_, prevAddresses, _, err := txscript.ExtractPkScriptAddrs(prevOut.PkScript, btc.ChainParams)
										if err == nil && len(prevAddresses) > 0 {
											fromAddr = prevAddresses[0].String()
										} else {
											fromAddr = ""
										}
									} else {
										fromAddr = ""
									}
								}
							} else {
								fromAddr = ""
							}

							// Get convert amount (in BTC)
							amount := utils.RemoveDecimals(big.NewInt(vout.Value), int64(btcDecimals))

							localTxList = append(localTxList, &InboundTransaction{
								UserId:   int(userId),
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
			log.Printf("[%s] Block %d was not processed successfully, stopping at %d",
				btc.NetworkCode, blockHeight, lastContinuousBlock)
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

	log.Printf("[%s] Processed %d transactions successful", btc.NetworkCode, len(txList))

	return txList, nil
}

/* GetUserWallet private methods group */
// @private deriveAddress Derive HD-wallet address by derivation index from master wallet private key
// masterPrivateKey: Master wallet private key
// userId: Unique user id for derivation index
func (btc *Bitcoin) deriveAddress(masterPrivateKey string, userId uint32) (string, error) {
	// Get seed bytes from private key
	seed, err := hex.DecodeString(masterPrivateKey)
	if err != nil {
		return "", fmt.Errorf("invalid master key format: %w", err)
	}

	// Derive private key using BIP84 for Native SegWit
	_, publicKey, err := btc.deriveSecp256k1Key(seed, userId)
	if err != nil {
		return "", fmt.Errorf("failed to derive key: %w", err)
	}

	// Create P2WPKH address (Native SegWit - bc1...)
	witnessProg := btcutil.Hash160(publicKey.SerializeCompressed())
	addr, err := btcutil.NewAddressWitnessPubKeyHash(witnessProg, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create segwit address: %w", err)
	}

	return addr.EncodeAddress(), nil
}

// @private deriveSecp256k1Key Bitcoin secp256k1 keys derivation for Native SegWit (BIP84)
// seed: Seed phrase
// userId: User id
// Returns: privateKey, publicKey
func (btc *Bitcoin) deriveSecp256k1Key(seed []byte, userId uint32) (*btcec.PrivateKey, *btcec.PublicKey, error) {
	// Init master HD key for BIP32
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	mac.Write(seed)
	result := mac.Sum(nil)

	key := result[:32]
	chainCode := result[32:]

	// BIP84 derivation path for Native SegWit: m/84'/0'/0'/0/{userId}
	path := []uint32{
		0x80000054, // 84' (BIP84 for Native SegWit)
		0x80000000, // 0' for mainnet, 1' for testnet
		0x80000000, // 0' (account)
		0,          // 0 (external chain, non-hardened)
		userId,     // {userId} (address index, non-hardened)
	}

	// Apply HMAC-SHA512 for each path level
	currentKey := key
	currentChainCode := chainCode

	for i, index := range path {
		var data []byte

		if index >= 0x80000000 { // Hardened derivation
			// data = 0x00 || private_key || index
			data = make([]byte, 37)
			data[0] = 0x00
			copy(data[1:33], currentKey)
			binary.BigEndian.PutUint32(data[33:], index)
		} else { // Non-hardened derivation
			// data = public_key || index
			privKey, _ := btcec.PrivKeyFromBytes(currentKey)
			pubKey := privKey.PubKey()
			data = make([]byte, 37)
			copy(data[:33], pubKey.SerializeCompressed())
			binary.BigEndian.PutUint32(data[33:], index)
		}

		hmacHash := hmac.New(sha512.New, currentChainCode)
		hmacHash.Write(data)
		result := hmacHash.Sum(nil)

		// Parse the new key
		newKey := result[:32]
		newChainCode := result[32:]

		// For secp256k1, need to add the parent key to the child key (modulo n)
		if i > 0 { // Skip for master key
			parentKey, _ := btcec.PrivKeyFromBytes(currentKey)
			childKeyScalar := new(btcec.ModNScalar)
			childKeyScalar.SetByteSlice(newKey)

			parentScalar := new(btcec.ModNScalar)
			parentScalar.Set(&parentKey.Key)

			childKeyScalar.Add(parentScalar)

			// Convert [32]byte array to []byte slice
			keyBytes := childKeyScalar.Bytes()
			newKey = keyBytes[:]
		}

		currentKey = newKey
		currentChainCode = newChainCode
	}

	// Create final private key
	privateKey, _ := btcec.PrivKeyFromBytes(currentKey)
	if privateKey == nil {
		return nil, nil, fmt.Errorf("failed to create private key")
	}
	// Get public key
	publicKey := privateKey.PubKey()

	return privateKey, publicKey, nil
}

// @private getUTXOData Get UTXO data
// fromAddress: Wallet from address
// amountSatoshi: Amount in Satoshi
// Returns: selectedUTXOs, totalInput, estimatedFee, error
func (btc *Bitcoin) getUTXOData(fromAddress btcutil.Address, amountSatoshi btcutil.Amount) ([]btcjson.ListUnspentResult, btcutil.Amount, btcutil.Amount, error) {
	// Get UTXOs for from address (min 1 confirmation for security)
	unspentList, err := btc.Client.ListUnspentMinMaxAddresses(1, 9999999, []btcutil.Address{fromAddress})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to get UTXO list: %w", err)
	}
	if len(unspentList) == 0 {
		return nil, 0, 0, fmt.Errorf("no UTXOs available for address '%s'", fromAddress.String())
	}

	// Sort UTXOs by amount descending (larger first = fewer inputs = lower fee)
	sort.Slice(unspentList, func(i, j int) bool {
		return unspentList[i].Amount > unspentList[j].Amount
	})

	// Get fee rate (sat/vB)
	feeResult, err := btc.Client.EstimateSmartFee(6, &btcjson.EstimateModeConservative)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to estimate fee: %w", err)
	}
	if feeResult.FeeRate == nil || *feeResult.FeeRate <= 0 {
		return nil, 0, 0, fmt.Errorf("invalid fee rate returned")
	}
	feeRateBTCPerKB := *feeResult.FeeRate

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
