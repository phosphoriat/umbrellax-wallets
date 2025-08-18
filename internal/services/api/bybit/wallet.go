package bybit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"wallets/internal/config"

	"github.com/gorilla/websocket"
)

// WSResponse Websocket server answer struct
type WSResponse struct {
	Success bool        `json:"success,omitempty"`
	RetCode int         `json:"retCode,omitempty"`
	RetMsg  string      `json:"retMsg,omitempty"`
	Op      string      `json:"op,omitempty"`
	Topic   string      `json:"topic,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	ConnID  string      `json:"connId,omitempty"`
}

// WalletResponse Wallet stream channel response
type WalletResponse struct {
	ID           string       `json:"id"`
	Topic        string       `json:"topic"`
	CreationTime int64        `json:"creationTime"`
	Data         []WalletData `json:"data"`
}

// WalletData data struct
type WalletData struct {
	AccountType            string     `json:"accountType"`
	AccountIMRate          string     `json:"accountIMRate"`
	AccountMMRate          string     `json:"accountMMRate"`
	TotalEquity            string     `json:"totalEquity"`
	TotalWalletBalance     string     `json:"totalWalletBalance"`
	TotalMarginBalance     string     `json:"totalMarginBalance"`
	TotalAvailableBalance  string     `json:"totalAvailableBalance"`
	TotalPerpUPL           string     `json:"totalPerpUPL"`
	TotalInitialMargin     string     `json:"totalInitialMargin"`
	TotalMaintenanceMargin string     `json:"totalMaintenanceMargin"`
	Coin                   []CoinInfo `json:"coin"`
}

// CoinInfo data struct
type CoinInfo struct {
	Coin                string `json:"coin"`
	Equity              string `json:"equity"`
	USDValue            string `json:"usdValue"`
	WalletBalance       string `json:"walletBalance"`
	Free                string `json:"free"`
	Locked              string `json:"locked"`
	AvailableToWithdraw string `json:"availableToWithdraw"`
	BorrowAmount        string `json:"borrowAmount"`
	AccruedInterest     string `json:"accruedInterest"`
	UnrealisedPnl       string `json:"unrealisedPnl"`
	CumRealisedPnl      string `json:"cumRealisedPnl"`
	CollateralSwitch    bool   `json:"collateralSwitch"`
	MarginCollateral    bool   `json:"marginCollateral"`
}

// WSClient WebSocket client struct
type WSClient struct {
	config     *config.Config
	conn       *websocket.Conn
	mu         sync.RWMutex
	stopChan   chan struct{}
	msgHandler func(*WalletResponse)
	httpClient *HTTPClient
}

// NewWSClient Create new WSClient instance
func NewWSClient(config *config.Config) *WSClient {
	return &WSClient{
		config:     config,
		stopChan:   make(chan struct{}),
		httpClient: GetHTTPClient(),
	}
}

// InitWalletStream Init websocket connection to wallet stream channel
func InitWalletStream(cfg *config.Config) {
	// Create new client instance
	client := NewWSClient(cfg)

	// Connect to the server
	if err := client.Connect(); err != nil {
		log.Fatalf("Connection error: %v", err)
	}

	// Waiting for termination signal
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Close connection
	<-interrupt
	if err := client.Close(); err != nil {
		log.Printf("Close connection error: %v", err)
	}
	log.Println("WebSocket connection closed")
}

// Connect to WebSocket server
func (ws *WSClient) Connect() error {
	// Set connection to WS server
	conn, _, err := websocket.DefaultDialer.Dial(ws.config.BybitWsUrl, nil)
	if err != nil {
		return fmt.Errorf("connection error: %w", err)
	}

	ws.mu.Lock()
	ws.conn = conn
	ws.mu.Unlock()

	log.Println("Connected to WebSocket server.")

	// Authentication
	if err := ws.authenticate(); err != nil {
		ws.conn.Close()
		return fmt.Errorf("WebSocket auth error: %w", err)
	}

	// Subscribe to wallet stream channel
	if err := ws.subscribeToWallet(); err != nil {
		ws.conn.Close()
		return fmt.Errorf("WebSocket channel subscribe error: %w", err)
	}

	// Start handle messages
	go ws.messageHandler()
	go ws.pingHandler()

	return nil
}

// Close connection
func (ws *WSClient) Close() error {
	close(ws.stopChan)

	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn != nil {
		return ws.conn.Close()
	}

	return nil
}

// @private pingHandler Send ping messages to maintain the connection
func (ws *WSClient) pingHandler() {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ws.stopChan:
			return

		case <-ticker.C:
			ws.mu.RLock()
			conn := ws.conn
			ws.mu.RUnlock()

			if conn == nil {
				return
			}

			pingMsg := map[string]string{"op": "ping"}
			if err := ws.sendMessage(pingMsg); err != nil {
				log.Printf("Ping sending error: %v", err)
			}
		}
	}
}

// @private generateSignature Generate signature for auth
func (ws *WSClient) generateSignature(expires int64) string {
	h := hmac.New(sha256.New, []byte(ws.config.BybitApiSecret))
	h.Write([]byte("GET/realtime" + strconv.FormatInt(expires, 10)))

	return hex.EncodeToString(h.Sum(nil))
}

// @private authenticate Make authentication
func (ws *WSClient) authenticate() error {
	expires := time.Now().Unix()*1000 + 10000 // +10 секунд
	signature := ws.generateSignature(expires)

	return ws.sendMessage(map[string]any{
		"op": "auth",
		"args": []interface{}{
			ws.config.BybitApiKey,
			expires,
			signature,
		},
	})
}

// @private subscribeToWallet Subscribe to wallet channel
func (ws *WSClient) subscribeToWallet() error {
	// Slight delay to complete authentication
	time.Sleep(1 * time.Second)

	// Send auth message to channel
	return ws.sendMessage(map[string]any{
		"op":   "subscribe",
		"args": []string{"wallet"},
	})
}

// @private sendMessage Send message to websocket channel
func (ws *WSClient) sendMessage(msg interface{}) error {
	ws.mu.RLock()
	defer ws.mu.RUnlock()

	if ws.conn == nil {
		return fmt.Errorf("connection is not set")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("serialization error: %w", err)
	}

	return ws.conn.WriteMessage(websocket.TextMessage, data)
}

// @private messageHandler Incoming messages handler (in a separate goroutines)
func (ws *WSClient) messageHandler() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Error in messageHandler: %v", r)
		}
	}()

	for {
		select {
		case <-ws.stopChan:
			return

		default:
			ws.mu.RLock()
			conn := ws.conn
			ws.mu.RUnlock()

			if conn == nil {
				return
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Error of reading message: %v", err)
				return
			}

			// Process the message in a separate goroutine
			go ws.processMessage(message)
		}
	}
}

// @private processMessage Process message
func (ws *WSClient) processMessage(message []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Error in processMessage: %v", r)
		}
	}()

	// Try to parse like common answer
	var wsResp WSResponse
	if err := json.Unmarshal(message, &wsResp); err != nil {
		log.Printf("Parsing answer error: %v", err)
		return
	}

	// Process different types of messages
	switch wsResp.Op {
	case "auth":
		if wsResp.RetCode == 0 {
			log.Printf("Authentication successfull. ConnID: %s", wsResp.ConnID)
		} else {
			log.Printf("Authentication error: %s (code: %d)", wsResp.RetMsg, wsResp.RetCode)
		}

	case "subscribe":
		if wsResp.RetCode == 0 {
			log.Printf("Subscibe successfull. ConnID: %s", wsResp.ConnID)
		} else {
			log.Printf("Subscibe error: %s (code: %d)", wsResp.RetMsg, wsResp.RetCode)
		}

	case "pong":
		log.Println("Get pong message")

	default:
		// Check, if its wallet topic
		if wsResp.Topic == "wallet" {
			ws.handleWalletData(message)
		} else {
			log.Printf("Unknown message type: %+v", wsResp)
		}
	}
}

// @private handleWalletData Handle wallet data
// Transfer funds from UNIFIED to FUND account
func (ws *WSClient) handleWalletData(message []byte) {
	var walletResp WalletResponse
	if err := json.Unmarshal(message, &walletResp); err != nil {
		fmt.Printf("Parse wallet data error: %v", err)
	}

	for _, data := range walletResp.Data {
		for _, coin := range data.Coin {
			if data.AccountType != "UNIFIED" {
				continue
			}

			_, err := ws.httpClient.CreateInternalTransfer(coin.Coin, coin.Free, "UNIFIED", "FUND")
			if err != nil {
				fmt.Printf("Error transferring %s %s from UNIFIED to FUND: %v\n",
					coin.Equity, coin.Coin, err)
			} else {
				fmt.Printf("Successfully transferred %s %s from UNIFIED to FUND\n",
					coin.Equity, coin.Coin)
			}
		}
	}
}
