package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/pkg/dto"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// Client represents a connected WebSocket client.
type Client struct {
	conn     *websocket.Conn
	send     chan []byte
	streamID string // optional filter
}

// Hub maintains active WebSocket clients and broadcasts events.
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the hub event loop. Call this in a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
			observability.WSConnections.Inc()
			slog.Debug("ws client connected", "filter", client.streamID)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			observability.WSConnections.Dec()
			slog.Debug("ws client disconnected")

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				// If client has a stream filter, check it
				if client.streamID != "" {
					var evt dto.WSEvent
					if err := json.Unmarshal(message, &evt); err == nil {
						if evt.StreamID.String() != client.streamID {
							continue
						}
					}
				}

				select {
				case client.send <- message:
				default:
					// Client buffer full — disconnect
					h.mu.RUnlock()
					h.mu.Lock()
					delete(h.clients, client)
					close(client.send)
					h.mu.Unlock()
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastEvent sends a detection event to all connected clients.
func (h *Hub) BroadcastEvent(event *dto.WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("marshal ws event", "error", err)
		return
	}
	h.broadcast <- data
}

// HandleWS handles WebSocket upgrade requests.
func (h *Hub) HandleWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "error", err)
		return
	}

	streamFilter := c.Query("stream_id")

	client := &Client{
		conn:     conn,
		send:     make(chan []byte, 64),
		streamID: streamFilter,
	}

	h.register <- client

	go client.writePump()
	go client.readPump(h)
}

func (c *Client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		// We don't process incoming messages from clients.
		// This loop exists to detect disconnection.
	}
}
