package main

import (
	"net/http"
	"os"
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

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.LogInfo("WebSocket server started on :" + port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.LogError("Error starting server", "error", err)
	}
}
