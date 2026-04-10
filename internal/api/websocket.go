package api

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	websocketGUID      = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsOpcodeText       = 0x1
	wsOpcodeClose      = 0x8
	wsOpcodePing       = 0x9
	wsOpcodePong       = 0xA
	wsMaxClientPayload = 2 << 20
)

var wsAllowedTypes = map[string]struct{}{
	"server":    {},
	"sessions":  {},
	"transfers": {},
	"audit":     {},
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if !isWebSocketUpgrade(r) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "websocket upgrade required"})
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = bearerToken(r.Header.Get("Authorization"))
	}
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}
	claims, err := verifyToken(s.secret, token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}
	username := strings.TrimSpace(claims.Sub)
	if username == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token subject"})
		return
	}
	requestedTypes := parseRequestedSnapshotTypes(r.URL.Query().Get("types"))

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "websocket hijacking is not supported"})
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		_ = writeHTTPError(buf, http.StatusBadRequest, "missing websocket key")
		return
	}

	accept := computeWebSocketAccept(key)
	if _, err := fmt.Fprintf(
		buf,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		accept,
	); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}

	writeMu := &sync.Mutex{}
	writeFrameSafe := func(opcode byte, payload []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return writeWSFrame(conn, opcode, payload)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			opcode, payload, err := readWSFrame(conn)
			if err != nil {
				return
			}
			switch opcode {
			case wsOpcodeClose:
				_ = writeFrameSafe(wsOpcodeClose, []byte{})
				return
			case wsOpcodePing:
				_ = writeFrameSafe(wsOpcodePong, payload)
			}
		}
	}()

	sendSnapshot := func() error {
		payload := s.buildWebSocketSnapshot(username, requestedTypes)
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		return writeFrameSafe(wsOpcodeText, raw)
	}

	if err := sendSnapshot(); err != nil {
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := sendSnapshot(); err != nil {
				return
			}
		}
	}
}

func (s *Server) buildWebSocketSnapshot(username string, requestedTypes map[string]struct{}) map[string]any {
	scopedUsername := s.viewerScopedUsername(username, "")
	payload := map[string]any{
		"type":      "snapshot",
		"timestamp": time.Now().UTC(),
	}
	if hasSnapshotType(requestedTypes, "server") && s.status != nil {
		payload["server"] = s.status()
	}
	if hasSnapshotType(requestedTypes, "sessions") && s.sessions != nil {
		payload["sessions"] = filterSessionsByUsername(s.sessions.List(), scopedUsername)
	}
	if hasSnapshotType(requestedTypes, "transfers") && s.transfers != nil {
		active := filterTransfers(s.transfers.Active(), scopedUsername, "", "", "", "")
		recent := filterTransfers(s.transfers.Recent(50), scopedUsername, "", "", "", "")
		stats := any(s.transfers.Stats())
		if scopedUsername != "" {
			stats = scopedTransferStats(active, recent)
		}
		payload["transfers"] = map[string]any{
			"active": active,
			"recent": recent,
			"stats":  stats,
		}
	}
	if hasSnapshotType(requestedTypes, "audit") {
		events := s.readRecentAuditEvents(50)
		if scopedUsername != "" {
			events = filterAuditEvents(events, scopedUsername, "", "", "", "")
		}
		payload["audit"] = map[string]any{
			"events": events,
		}
	}
	payload["viewer"] = map[string]any{
		"username": username,
		"types":    mapKeys(requestedTypes),
	}
	return payload
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	connection := strings.ToLower(r.Header.Get("Connection"))
	return strings.Contains(connection, "upgrade")
}

func computeWebSocketAccept(key string) string {
	hash := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(hash[:])
}

func writeHTTPError(w io.Writer, code int, message string) error {
	body := fmt.Sprintf("{\"error\":\"%s\"}\n", message)
	_, err := fmt.Fprintf(
		w,
		"HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code,
		http.StatusText(code),
		len(body),
		body,
	)
	return err
}

func writeWSFrame(conn net.Conn, opcode byte, payload []byte) error {
	if len(payload) > int(^uint(0)>>1) {
		return errors.New("payload too large")
	}

	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	length := len(payload)
	switch {
	case length <= 125:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(length))
		header = append(header, l[:]...)
	default:
		header = append(header, 127)
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(length))
		header = append(header, l[:]...)
	}

	if _, err := conn.Write(header); err != nil {
		return err
	}
	if length == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

func readWSFrame(conn net.Conn) (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, nil, err
	}
	fin := header[0]&0x80 != 0
	opcode := header[0] & 0x0F
	if !fin {
		return 0, nil, errors.New("fragmented frames are not supported")
	}

	masked := header[1]&0x80 != 0
	lengthMarker := uint64(header[1] & 0x7F)
	length := lengthMarker
	switch lengthMarker {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > wsMaxClientPayload {
		return 0, nil, errors.New("frame payload too large")
	}
	if !masked {
		return 0, nil, errors.New("client websocket frames must be masked")
	}

	var maskKey [4]byte
	if _, err := io.ReadFull(conn, maskKey[:]); err != nil {
		return 0, nil, err
	}

	payload := make([]byte, int(length))
	if length > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return 0, nil, err
		}
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return opcode, payload, nil
}

func parseRequestedSnapshotTypes(raw string) map[string]struct{} {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return copyAllowedTypes()
	}
	result := make(map[string]struct{})
	for _, part := range strings.Split(trimmed, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if _, ok := wsAllowedTypes[name]; ok {
			result[name] = struct{}{}
		}
	}
	if len(result) == 0 {
		return copyAllowedTypes()
	}
	return result
}

func hasSnapshotType(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}

func copyAllowedTypes() map[string]struct{} {
	out := make(map[string]struct{}, len(wsAllowedTypes))
	for k := range wsAllowedTypes {
		out[k] = struct{}{}
	}
	return out
}

func mapKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
