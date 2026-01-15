package main

import (
	"net/http"
	"video_conferencing_server/internal/handlers"
	"video_conferencing_server/internal/logger"
	"video_conferencing_server/internal/room"

	"github.com/pion/webrtc/v4"
)

func main() {
	// Application entry point
	rtcConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	roomManager := room.NewManager()
	wsHandler := handlers.NewWebSocketHandler(roomManager, rtcConfig)

	http.HandleFunc("/ws", wsHandler.Handle)
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	logger.LogInfo("WebSocket server started on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		logger.LogError("Error starting server", "error", err)
	}
}
