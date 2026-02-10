package realtime

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// WebSocketMessage represents a standard message format.
type WebSocketMessage struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   interface{} `json:"payload"`
}

// WebSocketManager handles websocket connections and broadcasting.
type WebSocketManager struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan WebSocketMessage
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.Mutex
	upgrader   websocket.Upgrader
}

// NewWebSocketManager creates a new WebSocketManager.
func NewWebSocketManager() *WebSocketManager {
	return &WebSocketManager{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan WebSocketMessage),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Allow all origins for now
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// Run starts the manager's main loop.
func (m *WebSocketManager) Run() {
	for {
		select {
		case conn := <-m.register:
			m.mu.Lock()
			m.clients[conn] = true
			m.mu.Unlock()
			log.Info().Msg("WebSocket client connected")

		case conn := <-m.unregister:
			m.mu.Lock()
			if _, ok := m.clients[conn]; ok {
				delete(m.clients, conn)
				conn.Close()
				log.Info().Msg("WebSocket client disconnected")
			}
			m.mu.Unlock()

		case message := <-m.broadcast:
			m.mu.Lock()
			for conn := range m.clients {
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteJSON(message); err != nil {
					log.Error().Err(err).Msg("Failed to write to websocket, closing connection")
					conn.Close()
					delete(m.clients, conn)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Broadcast sends a message to all connected clients.
func (m *WebSocketManager) Broadcast(msgType string, payload interface{}) {
	msg := WebSocketMessage{
		Type:      msgType,
		Timestamp: time.Now(),
		Payload:   payload,
	}
	m.broadcast <- msg
}

// HandleWebSocket upgrades the HTTP connection to a WebSocket connection.
func (m *WebSocketManager) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade websocket")
		return
	}
	m.register <- conn

	go func() {
		defer func() {
			m.unregister <- conn
		}()
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Error().Err(err).Msg("Websocket closed unexpectedly")
				}
				break
			}
		}
	}()
}
