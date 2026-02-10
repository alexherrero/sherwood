package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebSocketManager_Connection(t *testing.T) {
	manager := NewWebSocketManager()
	go manager.Run()

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(manager.HandleWebSocket))
	defer server.Close()

	// Convert http URL to ws URL
	u := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect
	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	require.NoError(t, err)
	defer ws.Close()

	// Verification: Check if client is registered
	// We need to wait a bit for the unexpected async registration
	time.Sleep(50 * time.Millisecond)

	manager.mu.Lock()
	clientCount := len(manager.clients)
	manager.mu.Unlock()

	assert.Equal(t, 1, clientCount, "Client should be registered")
}

func TestWebSocketManager_Broadcast(t *testing.T) {
	manager := NewWebSocketManager()
	go manager.Run()

	server := httptest.NewServer(http.HandlerFunc(manager.HandleWebSocket))
	defer server.Close()
	u := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	require.NoError(t, err)
	defer ws.Close()

	time.Sleep(50 * time.Millisecond)

	// Broadcast message
	payload := map[string]string{"foo": "bar"}
	manager.Broadcast("test_event", payload)

	// Read message
	ws.SetReadDeadline(time.Now().Add(time.Second))
	_, p, err := ws.ReadMessage()
	require.NoError(t, err)

	var msg WebSocketMessage
	err = json.Unmarshal(p, &msg)
	require.NoError(t, err)

	assert.Equal(t, "test_event", msg.Type)

	// Parse payload
	payloadData, ok := msg.Payload.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "bar", payloadData["foo"])
}

func TestWebSocketManager_Disconnect(t *testing.T) {
	manager := NewWebSocketManager()
	go manager.Run()

	server := httptest.NewServer(http.HandlerFunc(manager.HandleWebSocket))
	defer server.Close()
	u := "ws" + strings.TrimPrefix(server.URL, "http")

	ws, _, err := websocket.DefaultDialer.Dial(u, nil)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	manager.mu.Lock()
	assert.Equal(t, 1, len(manager.clients))
	manager.mu.Unlock()

	// Close connection
	ws.Close()

	// Wait for unregistration
	time.Sleep(100 * time.Millisecond)

	manager.mu.Lock()
	assert.Equal(t, 0, len(manager.clients))
	manager.mu.Unlock()
}
