var ws = new WebSocket(`wss://${window.location.hostname}/ws`);
var roomId = "testRoom"; // Default room ID for testing
var peerId = Math.random().toString(36).slice(2);

let pc;
let localStream = null;
let cameraEnabled = true;
const remoteStreams = new Map(); // trackId -> { stream, videoElement }

ws.onopen = async () => {
  console.log("WebSocket connected");

  ws.send(
    JSON.stringify({
      event: "join",
      data: { roomId, peerId },
    })
  );

  pc = new RTCPeerConnection({
    iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
  });

  // Show remote video
  pc.ontrack = (event) => {
    console.log("Received remote track", event.track.id);
    console.log("remote streams:", event.streams);

    for (const stream of event.streams) {
      console.log("Stream ID:", stream.id, "Tracks:", stream.getTracks());

      const streamId = stream.id;

      // Check if we already have this stream
      if (remoteStreams.has(streamId)) {
        // Stream already exists, just ensure track is added
        const existing = remoteStreams.get(streamId);
        if (!existing.stream.getTrackById(event.track.id)) {
          existing.stream.addTrack(event.track);
        }
        continue;
      }

      // Create new video element for this stream
      const videoContainer = document.createElement("div");
      videoContainer.className = "video-container";
      videoContainer.id = `container-${streamId}`;

      const video = document.createElement("video");
      video.id = `video-${streamId}`;
      video.autoplay = true;
      video.playsInline = true;
      video.srcObject = stream;

      const label = document.createElement("span");
      label.className = "video-label";
      label.textContent = `Peer ${remoteStreams.size + 1}`;

      videoContainer.appendChild(video);
      videoContainer.appendChild(label);
      document.getElementById("videoGrid").appendChild(videoContainer);

      remoteStreams.set(streamId, {
        stream,
        videoElement: video,
        container: videoContainer,
      });

      video.play().catch((e) => {
        console.error("Error playing video:", e);
      });

      // DEBUG: Check if the track starts 'muted' (no data yet)
      event.track.onunmute = () => {
        console.log("Track unmuted (data is flowing)", event.track.id);
      };

      // Handle track ended
      event.track.onended = () => {
        console.log("Track ended", event.track.id);
        removeRemoteStream(streamId);
      };
    }
  };

  // ICE → server
  pc.onicecandidate = (event) => {
    if (event.candidate) {
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

  // Get camera
  const stream = await navigator.mediaDevices.getUserMedia({
    video: true,
    audio: true,
  });

  localStream = stream;
  localVideo.srcObject = stream;
  console.log("local stream:", stream);

  stream.getTracks().forEach((track) => pc.addTrack(track, stream));

  forceVP8(pc);

  // Create offer
  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  ws.send(
    JSON.stringify({
      event: "offer",
      data: offer.sdp,
    })
  );
};

ws.onmessage = async (msg) => {
  const message = JSON.parse(msg.data);

  if (message.event === "offer") {
    console.log("Received renegotiation offer from server");
    await pc.setRemoteDescription({
      type: "offer",
      sdp: message.data,
    });

    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);

    ws.send(
      JSON.stringify({
        event: "answer",
        data: answer.sdp,
      })
    );
  }

  if (message.event === "answer") {
    await pc.setRemoteDescription({
      type: "answer",
      sdp: message.data,
    });
    console.log("Answer applied");
  }

  if (message.event === "iceCandidate") {
    if (pc.remoteDescription) {
      await pc.addIceCandidate(message.data);
    }
  }

  if (message.event === "peer-id") {
    console.log("Server-assigned peerId:", message.data);
    peerId = message.data; // Server is authoritative, update local peerId
  }

  if (message.event === "remove-peer") {
    const removedPeerId = message.data;
    console.log("Removing peer:", removedPeerId);
    removeRemoteStream(`stream-${removedPeerId}`);
  }

  if (message.event === "room-full") {
    alert("The room is full. Cannot join.");
    console.warn("Room is full:", message.data);
    ws.close();
  }
};

function removeRemoteStream(streamId) {
  const remote = remoteStreams.get(streamId);
  if (remote) {
    remote.container.remove();
    remoteStreams.delete(streamId);
    console.log(`Removed stream ${streamId}`);
  }
}

function clearAllRemoteStreams() {
  remoteStreams.forEach((remote, streamId) => {
    remote.container.remove();
  });
  remoteStreams.clear();
  console.log("Cleared all remote streams");
}

function forceVP8(pc) {
  const transceivers = pc.getTransceivers();
  transceivers.forEach((t) => {
    if (t.sender.track && t.sender.track.kind === "video") {
      const capabilities = RTCRtpSender.getCapabilities("video");
      if (!capabilities) return;

      // Find VP8 codecs
      const vp8Codecs = capabilities.codecs.filter(
        (c) => c.mimeType.toLowerCase() === "video/vp8"
      );

      if (vp8Codecs.length > 0) {
        console.log("Forcing VP8 codec");
        // Attempt to set preferences
        // Note: Some browsers throw if parameters are invalid, so we catch errors
        try {
          t.setCodecPreferences(vp8Codecs);
        } catch (e) {
          console.warn("Could not set codec preferences:", e);
        }
      } else {
        console.warn("VP8 not supported by this browser");
      }
    }
  });
}

function toggleCamera() {
  if (!localStream) return;

  const videoTrack = localStream.getVideoTracks()[0];
  if (!videoTrack) return;

  cameraEnabled = !cameraEnabled;
  videoTrack.enabled = cameraEnabled;

  const btn = document.getElementById("cameraBtn");
  if (cameraEnabled) {
    btn.textContent = "Turn Off Camera";
    btn.classList.remove("camera-off");
  } else {
    btn.textContent = "Turn On Camera";
    btn.classList.add("camera-off");
  }

  console.log(`Camera ${cameraEnabled ? "enabled" : "disabled"}`);
}

function toggleMic() {
  if (!localStream) return;

  const audioTrack = localStream.getAudioTracks()[0];
  if (!audioTrack) return;

  const micEnabled = audioTrack.enabled;
  audioTrack.enabled = !micEnabled;

  const btn = document.getElementById("micBtn");
  if (audioTrack.enabled) {
    btn.textContent = "Turn Off Microphone";
    btn.classList.remove("mic-off");
  } else {
    btn.textContent = "Turn On Microphone";
    btn.classList.add("mic-off");
  }

  console.log(`Microphone ${audioTrack.enabled ? "enabled" : "disabled"}`);
}

function joinRoom() {
  const input = document.getElementById("roomIdInput");
  const newRoomId = input.value.trim();
  if (newRoomId) {
    roomId = newRoomId;
    console.log(`Joining room: ${roomId}`);

    // Clear existing remote streams when switching rooms
    clearAllRemoteStreams();

    ws.send(
      JSON.stringify({
        event: "leave",
        data: {},
      })
    );
    ws.send(
      JSON.stringify({
        event: "join",
        data: { roomId, peerId },
      })
    );
  } else {
    alert("Please enter a valid Room ID");
  }
}
