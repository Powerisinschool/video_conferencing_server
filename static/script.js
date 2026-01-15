let ws = null;
let pc = null;
let roomId = "testRoom";
let peerId = Math.random().toString(36).slice(2);

let localStream = null;
let shareScreenStream = null;
let cameraEnabled = true;
const remoteStreams = new Map(); // trackId -> { stream, videoElement }

// --- Initialization ---
window.addEventListener("load", async () => {
  // We stay in lobby initially.
  // We can request camera permissions eagerly or wait for join.
  // Let's wait for join to keep it clean, or start preview if desired.
  // await startLocalStream();
});

// --- Logic ---

function joinRoomFromLobby() {
  const input = document.getElementById("lobbyRoomInput");
  const newRoomId = input.value.trim();
  if (!newRoomId) {
    alert("Please enter a Room ID");
    return;
  }
  joinRoom(newRoomId);
}

async function joinRoom(newRoomId) {
  roomId = newRoomId;
  // Switch UI to room view
  switchView("room");
  document.getElementById("headerTitle").textContent = `Room: ${roomId}`;
  // Get Media if not already then connect WebSocket for a fresh connection
  await startLocalStream();
  connectWebSocket();
}

function connectWebSocket() {
  // If we have an existing connection, close it first
  if (ws) {
    ws.close();
    ws = null;
  }

  // Create FRESH connection
  const protocol = window.location.protocol === "https:" ? "wss://" : "ws://";
  ws = new WebSocket(`${protocol}${window.location.host}/ws`);

  ws.onopen = async () => {
    console.log("WebSocket connected");

    // Create PC *after* socket is open
    createPeerConnection();

    // Send Join
    ws.send(
      JSON.stringify({
        event: "join",
        data: { roomId, peerId },
      })
    );

    // Create & Send Offer
    try {
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      ws.send(
        JSON.stringify({
          event: "offer",
          data: offer.sdp,
        })
      );
    } catch (e) {
      console.error("Error creating offer:", e);
    }
  };

  ws.onmessage = handleWebSocketMessage;

  ws.onclose = () => {
    console.log("WebSocket disconnected");
    // Only cleanup if we are still in "room" view (not intentionally switching)
    // If the server kicked us, we should clean up.
  };
}

async function handleWebSocketMessage(msg) {
  const message = JSON.parse(msg.data);

  if (message.event === "offer") {
    console.log("Received renegotiation offer");
    if (!pc) return;
    await pc.setRemoteDescription({ type: "offer", sdp: message.data });
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    ws.send(JSON.stringify({ event: "answer", data: answer.sdp }));
  }

  if (message.event === "answer") {
    console.log("Received answer");
    if (!pc) return;
    await pc.setRemoteDescription({ type: "answer", sdp: message.data });
  }

  if (message.event === "iceCandidate") {
    if (pc && pc.remoteDescription) {
      try {
        await pc.addIceCandidate(message.data);
      } catch (e) {
        console.error("Error adding ICE candidate", e);
      }
    }
  }

  if (message.event === "peer-id") {
    peerId = message.data;
  }

  if (message.event === "peer-left") {
    const removedPeerId = message.data;
    const peerName = removeRemoteStream(removedPeerId); // Logic corrected to pass peerId

    if (peerName) {
      showNotification(`${peerName} has left the room.`);
    }
  }

  if (message.event === "room-full") {
    alert("The room is full.");
    leaveRoom();
  }
}

function leaveRoom() {
  // Close Peer Connection and WebSocket
  if (pc) {
    pc.close();
    pc = null;
  }

  if (ws) {
    // We can just close it, server handles "leave" logic on disconnect
    ws.close();
    ws = null;
  }

  // Clear UI and switch to lobby
  clearAllRemoteStreams();
  switchView("lobby");
}

function createPeerConnection() {
  if (pc) pc.close();

  pc = new RTCPeerConnection({
    iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
  });

  pc.ontrack = handleTrackEvent;
  pc.onicecandidate = (event) => {
    if (event.candidate && ws && ws.readyState === WebSocket.OPEN) {
      ws.send(
        JSON.stringify({
          event: "iceCandidate",
          data: {
            candidate: event.candidate.candidate,
            sdpMid: event.candidate.sdpMid,
            sdpMLineIndex: event.candidate.sdpMLineIndex,
          },
        })
      );
    }
  };

  if (localStream) {
    localStream.getTracks().forEach((track) => pc.addTrack(track, localStream));
  }

  forceVP8(pc);
}

function handleTrackEvent(event) {
  // Use the stream ID to group tracks
  const stream = event.streams[0];
  if (!stream) return;
  const streamId = stream.id;

  if (remoteStreams.has(streamId)) {
    return; // Already handled (tracks add themselves to stream automatically)
  }

  console.log("New remote stream:", streamId);

  const videoContainer = document.createElement("div");
  videoContainer.className = "video-container";
  videoContainer.id = `container-${streamId}`;

  const video = document.createElement("video");
  video.id = `video-${streamId}`;
  video.autoplay = true;
  video.playsInline = true;
  video.srcObject = stream;

  const overlay = document.createElement("div");
  overlay.className = "video-overlay";
  const label = document.createElement("span");
  label.className = "video-label";
  label.textContent = `Peer ${remoteStreams.size + 1}`;

  overlay.appendChild(label);
  videoContainer.appendChild(video);
  videoContainer.appendChild(overlay);

  document.getElementById("videoGrid").appendChild(videoContainer);

  remoteStreams.set(streamId, {
    stream,
    container: videoContainer,
  });

  // Handle track removal if needed, though usually we rely on peer-left
  stream.onremovetrack = () => {
    if (stream.getTracks().length === 0) {
      removeRemoteStream(streamId);
    }
  };
}

function removeRemoteStream(idOrPeerId) {
  // Try direct match first
  let streamId = idOrPeerId;
  if (!remoteStreams.has(streamId)) {
    // Try "stream-" prefix match if direct match fails
    streamId = `stream-${idOrPeerId}`;
  }
  if (remoteStreams.has(streamId)) {
    const remote = remoteStreams.get(streamId);

    // Grab the name before we destroy the element
    const nameLabel = remote.container.querySelector(".video-label");
    const peerName = nameLabel ? nameLabel.textContent : "A Peer";

    remote.container.remove();
    remoteStreams.delete(streamId);
    return peerName; // Return the name so we can alert it
  }
  return null;
}

function clearAllRemoteStreams() {
  remoteStreams.forEach((remote) => remote.container.remove());
  remoteStreams.clear();
}

async function startLocalStream() {
  if (localStream) return;
  try {
    localStream = await navigator.mediaDevices.getUserMedia({
      video: true,
      audio: true,
    });
    document.getElementById("localVideo").srcObject = localStream;
  } catch (e) {
    console.error("Error accessing media:", e);
    alert("Could not access camera/microphone.");
  }
}

function switchView(view) {
  const lobby = document.getElementById("lobbyView");
  const room = document.getElementById("roomView");

  if (view === "lobby") {
    lobby.classList.remove("hidden");
    room.classList.add("hidden");
  } else {
    lobby.classList.add("hidden");
    room.classList.remove("hidden");
  }
}

function toggleCamera() {
  if (!localStream) return;
  const videoTrack = localStream.getVideoTracks()[0];
  if (videoTrack) {
    cameraEnabled = !cameraEnabled;
    videoTrack.enabled = cameraEnabled;
    document
      .getElementById("cameraBtn")
      .classList.toggle("camera-off", !cameraEnabled);
  }
}

function toggleMic() {
  if (!localStream) return;
  const audioTrack = localStream.getAudioTracks()[0];
  if (audioTrack) {
    audioTrack.enabled = !audioTrack.enabled;
    document
      .getElementById("micBtn")
      .classList.toggle("mic-off", !audioTrack.enabled);
  }
}

function shareScreen() {
  // alert("Screen sharing is not implemented in this demo.");
  if (shareScreenStream) {
    // Stop screen sharing
    shareScreenStream.getTracks().forEach((track) => track.stop());
    shareScreenStream = null;
    document.getElementById("shareBtn").classList.remove("screen-sharing");
    // Revert to camera video track
    const videoTrack = localStream.getVideoTracks()[0];
    if (videoTrack && pc) {
      pc.getSenders().forEach((sender) => {
        if (sender.track && sender.track.kind === "video") {
          sender.replaceTrack(videoTrack);
        }
      });
    }
  } else {
    // Start screen sharing
    navigator.mediaDevices
      .getDisplayMedia({ video: true })
      .then((stream) => {
        shareScreenStream = stream;
        document.getElementById("shareBtn").classList.add("screen-sharing");
        const screenTrack = stream.getVideoTracks()[0];
        if (screenTrack && pc) {
          pc.getSenders().forEach((sender) => {
            if (sender.track && sender.track.kind === "video") {
              sender.replaceTrack(screenTrack);
            }
          });
        }
        // Listen for when the user stops sharing the screen
        screenTrack.onended = () => {
          shareScreen();
        };
      })
      .catch((e) => {
        console.error("Error sharing screen:", e);
      });
  }
}

function forceVP8(pc) {
  const transceivers = pc.getTransceivers();
  transceivers.forEach((t) => {
    if (t.sender.track && t.sender.track.kind === "video") {
      const capabilities = RTCRtpSender.getCapabilities("video");
      if (!capabilities) return;
      const vp8Codecs = capabilities.codecs.filter(
        (c) => c.mimeType.toLowerCase() === "video/vp8"
      );
      if (vp8Codecs.length > 0) {
        try {
          t.setCodecPreferences(vp8Codecs);
        } catch (e) {
          console.warn(e);
        }
      }
    }
  });
}

function showNotification(message, type = "info") {
  const container = document.getElementById("notificationContainer");

  const toast = document.createElement("div");
  toast.className = `notification-toast ${type}`;
  toast.textContent = message;

  container.appendChild(toast);

  // Remove after 3 seconds
  setTimeout(() => {
    toast.style.animation = "fadeOut 0.3s ease-out forwards";
    toast.addEventListener("animationend", () => {
      toast.remove();
    });
  }, 3000);
}
