package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/handlers"
	"github.com/gorilla/websocket"
)

// Message types
const (
    MessageTypeDraw      = "draw"
    MessageTypeBackground = "background"
    MessageTypeClear     = "clear"
    MessageTypeHistory   = "history"
    MessageTypeText      = "text"
)

// DrawData represents the drawing action data
type DrawData struct {
    Type      string  `json:"type"`
    PrevX     float64 `json:"prevX"`
    PrevY     float64 `json:"prevY"`
    CurrX     float64 `json:"currX"`
    CurrY     float64 `json:"currY"`
    Color     string  `json:"color"`
    BrushSize int     `json:"brushSize"`
}

type TextData struct {
    Type  string `json:"type"`
    Text  string `json:"text"`
    X     int    `json:"x"`
    Y     int    `json:"y"`
    Color string `json:"color"`
    TextSize int `json:"textSize"`
}

// BackgroundData represents background color change data
type BackgroundData struct {
    Type  string `json:"type"`
    Color string `json:"color"`
}

// ClearData represents canvas clear action
type ClearData struct {
    Type string `json:"type"`
}

// HistoryData represents the complete drawing history
type HistoryData struct {
    Type     string          `json:"type"`
    Commands []DrawingCommand `json:"commands"`
}

// DrawingCommand represents a single drawing command
type DrawingCommand struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload"`
}

// WhiteboardState maintains the current state of a room's whiteboard
type WhiteboardState struct {
    BackgroundColor string
    DrawingHistory []DrawingCommand
    mutex          sync.RWMutex
}

// Room represents a whiteboard room
type Room struct {
    ID      string
    Clients map[*websocket.Conn]bool
    State   *WhiteboardState
    mutex   sync.RWMutex
}

var (
    upgrader = websocket.Upgrader{
        ReadBufferSize:  1024,
        WriteBufferSize: 1024,
        CheckOrigin: func(r *http.Request) bool {
            return true
        },
    }
    
    rooms      = make(map[string]*Room)
    roomsMutex sync.RWMutex
)

func getOrCreateRoom(roomID string) *Room {
    roomsMutex.Lock()
    defer roomsMutex.Unlock()

    if room, exists := rooms[roomID]; exists {
        return room
    }

    room := &Room{
        ID:      roomID,
        Clients: make(map[*websocket.Conn]bool),
        State: &WhiteboardState{
            BackgroundColor: "#ffffff",
            DrawingHistory: make([]DrawingCommand, 0),
        },
    }
    rooms[roomID] = room
    return room
}

func broadcastToRoom(room *Room, sender *websocket.Conn, message []byte) {
    room.mutex.Lock()
    defer room.mutex.Unlock()

    for client := range room.Clients {
        if client != sender {
            err := client.WriteMessage(websocket.TextMessage, message)
            if err != nil {
                log.Printf("Error broadcasting message: %v", err)
                client.Close()
                delete(room.Clients, client)
            }
        }
    }
}

func handleBackgroundChange(room *Room, data BackgroundData) {
    room.State.mutex.Lock()
    room.State.BackgroundColor = data.Color
    // Add to drawing history
    command := DrawingCommand{
        Type:    MessageTypeBackground,
        Payload: json.RawMessage(mustMarshal(data)),
    }
    room.State.DrawingHistory = append(room.State.DrawingHistory, command)
    room.State.mutex.Unlock()
}

func mustMarshal(v interface{}) []byte {
    data, err := json.Marshal(v)
    if err != nil {
        log.Printf("Error marshaling data: %v", err)
        return []byte("{}")
    }
    return data
}

func sendCurrentState(conn *websocket.Conn, room *Room) {
    room.State.mutex.RLock()
    defer room.State.mutex.RUnlock()

    // Send complete history
    history := HistoryData{
        Type:     MessageTypeHistory,
        Commands: room.State.DrawingHistory,
    }

    data := mustMarshal(history)
    err := conn.WriteMessage(websocket.TextMessage, data)
    if err != nil {
        log.Printf("Error sending history: %v", err)
    }
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
    roomID := r.URL.Query().Get("room")
    if roomID == "" {
        http.Error(w, "Room ID is required", http.StatusBadRequest)
        return
    }

    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("Failed to upgrade connection: %v", err)
        return
    }
    defer conn.Close()

    room := getOrCreateRoom(roomID)

    room.mutex.Lock()
    room.Clients[conn] = true
    room.mutex.Unlock()

    // Send current state to new client
    sendCurrentState(conn, room)

    defer func() {
        room.mutex.Lock()
        delete(room.Clients, conn)
        room.mutex.Unlock()

        roomsMutex.Lock()
        if len(room.Clients) == 0 {
            delete(rooms, roomID)
        }
        roomsMutex.Unlock()
    }()

    for {
        _, msg, err := conn.ReadMessage()
        if err != nil {
            log.Printf("Error reading message: %v", err)
            break
        }

        var baseMessage struct {
            Type string `json:"type"`
        }
        if err := json.Unmarshal(msg, &baseMessage); err != nil {
            log.Printf("Error parsing message type: %v", err)
            continue
        }

        // Add message to history and broadcast
        room.State.mutex.Lock()
        command := DrawingCommand{
            Type:    baseMessage.Type,
            Payload: json.RawMessage(msg),
        }
        room.State.DrawingHistory = append(room.State.DrawingHistory, command)
        room.State.mutex.Unlock()

        switch baseMessage.Type {
        case MessageTypeBackground:
            var bgData BackgroundData
            if err := json.Unmarshal(msg, &bgData); err != nil {
                continue
            }
            handleBackgroundChange(room, bgData)

        case MessageTypeClear:
            // Clear history when canvas is cleared
            room.State.mutex.Lock()
            room.State.DrawingHistory = []DrawingCommand{{
                Type:    MessageTypeClear,
                Payload: msg,
            }}
            room.State.mutex.Unlock()
        }

        broadcastToRoom(room, conn, msg)
    }
}

func main() {
    mux:=http.NewServeMux()
    mux.Handle("/", http.FileServer(http.Dir("public")))
    mux.HandleFunc("/ws", handleWebSocket)
    
    
    
    log.Println("Server starting on :8080")
    if err := http.ListenAndServe(":8080",handlers.CORS(handlers.AllowedOrigins([]string{"https://klum.vercel.app"}),)(mux)); err != nil {
        log.Fatal("ListenAndServe:", err)
    }
}