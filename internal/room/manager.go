package room

import (
	"sync"
	"video_conferencing_server/internal/logger"
	"video_conferencing_server/internal/models"

	"github.com/google/uuid"
)

const DefaultRoomCapacity = 50

type Room struct {
	ID                string
	ListLock          sync.RWMutex
	Peers             map[uuid.UUID]*models.Peer
	ManagementDetails *models.ManagementDetails
	AccessDetails     *models.AccessDetails
	WaitingList       []*models.Peer
	Capacity          int
}

type Manager struct {
	rooms     map[string]*Room
	roomsLock sync.RWMutex
}

// NewManager creates and returns a new Room Manager instance
func NewManager() *Manager {
	return &Manager{
		rooms: make(map[string]*Room),
	}
}

// CreateRoom creates a new room with the given ID or returns the existing one
func (m *Manager) CreateRoom(roomID string, capacity int, room *Room) {
	m.roomsLock.Lock()
	defer m.roomsLock.Unlock()

	if _, exists := m.rooms[roomID]; !exists {
		room.ID = roomID
		room.Peers = make(map[uuid.UUID]*models.Peer)
		room.Capacity = capacity
		m.rooms[roomID] = room
	}
}

// GetRoom retrieves a room by its ID
func (m *Manager) GetRoom(roomID string) *Room {
	m.roomsLock.RLock()
	defer m.roomsLock.RUnlock()
	return m.rooms[roomID]
}

func (m *Manager) DeleteRoom(roomID string) {
	m.roomsLock.Lock()
	defer m.roomsLock.Unlock()
	// We need to ensure the room is empty before deleting
	room := m.rooms[roomID]
	if room == nil {
		return
	}
	room.ListLock.RLock()
	defer room.ListLock.RUnlock()
	for _, peer := range room.Peers {
		room.RemovePeer(peer)
	}
	if len(room.Peers) > 0 {
		logger.LogError("Attempted to delete non-empty room", "roomId", roomID)
		return
	}
	delete(m.rooms, roomID) // Should only be called when room is empty
}
