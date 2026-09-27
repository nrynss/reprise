package assemblyai

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrSocketGone reports a voice socket that is already closed. The end
// still deletes the provider record. It does not open a replacement.
var ErrSocketGone = errors.New("assemblyai: socket is gone")

// ErrVoiceUpgrade reports a handshake that did not open a voice socket.
// The record stays in place, and a later end may dial again.
var ErrVoiceUpgrade = errors.New("assemblyai: voice upgrade failed")

// sessionEndFrame is the client end. The voice socket stops billing only
// when it receives this frame. A record delete does not send it.
var sessionEndFrame = []byte(`{"type":"session.end"}`)

// voiceFrameLimit is the largest voice frame this client will read.
const voiceFrameLimit = 1 << 20

// voiceWait is how long one end waits on a dial or a provider answer.
// The bound sits far under the process limit for one call.
const voiceWait = 5 * time.Second

// LiveSocket is one open voice websocket. SendText writes one text frame.
// A socket that is already closed returns ErrSocketGone.
type LiveSocket interface {
	SendText(ctx context.Context, payload []byte) error
}

// endedWaiter is a socket that waits for the provider end answer. A fake
// socket in a test does not implement it.
type endedWaiter interface {
	waitEnded(ctx context.Context) error
}

// endGate is the end state of one provider session. sent means the end
// frame was written. dialed means this session already chose whether to
// open a socket, so a later end must not open another.
type endGate struct {
	mu     sync.Mutex
	sock   LiveSocket
	dialed bool
	sent   bool
}

// voiceResume is the first frame on a socket this process opened. It
// attaches to the existing session. This client never sends a session
// update on that socket, so a refused resume cannot start a fresh call.
type voiceResume struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

// voiceEvent is the type field of one provider frame.
type voiceEvent struct {
	Type string `json:"type"`
}

// HoldSocket keeps sock as the live voice socket for sessionID.
// EndSession writes session.end on that socket and does not open another.
// An empty id or a nil socket is refused. A socket offered after the end
// frame was already written is ignored.
func (c *SessionsClient) HoldSocket(sessionID string, sock LiveSocket) error {
	if c == nil {
		return fmt.Errorf("assemblyai: hold socket: %w: client must not be nil", ErrInvalid)
	}
	if sessionID == "" || sock == nil {
		return fmt.Errorf("assemblyai: hold socket: %w: session id and socket must not be empty", ErrInvalid)
	}
	c.mu.Lock()
	gate := c.ensureGate(sessionID)
	c.mu.Unlock()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.sent {
		return nil
	}
	gate.sock = sock
	return nil
}

// StopSocket writes session.end for sessionID and does not delete the
// record. A held socket gets the frame directly. With no held socket, one
// dial resumes the session and the frame goes out on that socket. A socket
// that is already gone returns false and no error, and it does not open a
// replacement. A second call does not open a new socket. True means the
// end frame was written.
func (c *SessionsClient) StopSocket(ctx context.Context, sessionID string) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("assemblyai: stop socket: %w: client must not be nil", ErrInvalid)
	}
	if sessionID == "" {
		return false, fmt.Errorf("assemblyai: stop socket: %w: session id must not be empty", ErrInvalid)
	}
	c.mu.Lock()
	gate := c.ensureGate(sessionID)
	dial := c.dial
	c.mu.Unlock()

	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.sent {
		return true, nil
	}
	sock := gate.sock
	if sock == nil && !gate.dialed && dial != nil {
		gate.dialed = true
		opened, err := dial(ctx, sessionID)
		if err != nil {
			if errors.Is(err, ErrSocketGone) {
				return false, nil
			}
			// A refused upgrade is not a gone socket. Clear the dial so a
			// later end can try again, and keep the provider record.
			gate.dialed = false
			return false, err
		}
		if gate.sock != nil {
			closeSocket(opened)
			sock = gate.sock
		} else {
			gate.sock = opened
			sock = opened
		}
	}
	if sock == nil {
		gate.dialed = true
		return false, nil
	}
	if err := sock.SendText(ctx, sessionEndFrame); err != nil {
		if errors.Is(err, ErrSocketGone) {
			if gate.sock == sock {
				gate.sock = nil
			}
			gate.dialed = true
			closeSocket(sock)
			return false, nil
		}
		return false, fmt.Errorf("assemblyai: stop socket: %w", err)
	}
	if waiter, ok := sock.(endedWaiter); ok {
		if err := waiter.waitEnded(ctx); err != nil && !errors.Is(err, ErrSocketGone) {
			return false, fmt.Errorf("assemblyai: stop socket: %w", err)
		}
	}
	gate.sent = true
	gate.dialed = true
	if gate.sock == sock {
		gate.sock = nil
	}
	closeSocket(sock)
	return true, nil
}

// EndSession writes session.end on the live socket, then deletes the
// provider record. A socket that is already gone still deletes. A second
// end does not open a new socket. SocketEnded reports that the end frame
// was written.
func (c *SessionsClient) EndSession(ctx context.Context, sessionID string) (TerminateResult, error) {
	if c == nil || c.http == nil {
		return TerminateResult{}, fmt.Errorf("assemblyai: end session: %w: client must not be nil", ErrInvalid)
	}
	sent, err := c.StopSocket(ctx, sessionID)
	if err != nil {
		return TerminateResult{}, err
	}
	res, err := c.TerminateSession(ctx, sessionID)
	if err != nil {
		return TerminateResult{}, err
	}
	res.SocketEnded = sent
	return res, nil
}

// ensureGate returns the end state for one session. The caller holds c.mu.
func (c *SessionsClient) ensureGate(sessionID string) *endGate {
	if c.gates == nil {
		c.gates = map[string]*endGate{}
	}
	gate, ok := c.gates[sessionID]
	if !ok {
		gate = &endGate{}
		c.gates[sessionID] = gate
	}
	return gate
}

// closeSocket releases a socket that can close. A fake socket with no
// closer stays as it is.
func closeSocket(sock LiveSocket) {
	closer, ok := sock.(io.Closer)
	if !ok {
		return
	}
	_ = closer.Close()
}

// dialVoice opens one voice socket and resumes sessionID on it. The end
// frame has to ride a socket that already holds the session. A new socket
// attaches with session.resume, and the caller then writes session.end.
// A refused resume returns ErrSocketGone. This dial never sends a session
// update, so it does not start a fresh call.
func (c *SessionsClient) dialVoice(ctx context.Context, sessionID string) (LiveSocket, error) {
	ctx, cancel := context.WithTimeout(ctx, voiceWait)
	defer cancel()
	conn, err := c.dialConn(ctx)
	if err != nil {
		return nil, err
	}
	sock := &wsSocket{conn: conn}
	body, err := json.Marshal(voiceResume{Type: "session.resume", SessionID: sessionID})
	if err != nil {
		_ = sock.Close()
		return nil, fmt.Errorf("assemblyai: encode resume: %w", err)
	}
	if err := sock.SendText(ctx, body); err != nil {
		_ = sock.Close()
		return nil, err
	}
	if err := sock.waitType(ctx, "session.ready"); err != nil {
		_ = sock.Close()
		if errors.Is(err, ErrSocketGone) {
			return nil, fmt.Errorf("assemblyai: resume session: %w", ErrSocketGone)
		}
		return nil, err
	}
	return sock, nil
}

// dialConn opens the voice websocket on the sessions host. Any status
// other than 101 is a failed upgrade, not a gone socket. A cancelled
// context returns that context error, so a cancelled end does not look
// like a gone socket.
func (c *SessionsClient) dialConn(ctx context.Context) (net.Conn, error) {
	parsed, err := url.Parse(c.base)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("assemblyai: dial voice socket: %w: base URL is not usable", ErrSocketGone)
	}
	addr := parsed.Host
	if _, _, splitErr := net.SplitHostPort(addr); splitErr != nil {
		port := "443"
		if parsed.Scheme == "http" {
			port = "80"
		}
		addr = net.JoinHostPort(parsed.Hostname(), port)
	}
	dialer := &net.Dialer{Timeout: voiceWait}
	var conn net.Conn
	switch parsed.Scheme {
	case "http":
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	case "https", "":
		raw, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			err = dialErr
			break
		}
		tlsConn := tls.Client(raw, &tls.Config{ServerName: parsed.Hostname()})
		if handshakeErr := tlsConn.HandshakeContext(ctx); handshakeErr != nil {
			_ = raw.Close()
			err = handshakeErr
			break
		}
		conn = tlsConn
	default:
		return nil, fmt.Errorf("assemblyai: dial voice socket: %w: scheme %s is not usable", ErrSocketGone, parsed.Scheme)
	}
	if err != nil {
		if voiceTimeout(err) {
			return nil, err
		}
		return nil, fmt.Errorf("assemblyai: dial voice socket: %w", err)
	}
	if err := writeUpgrade(conn, parsed.Host, c.key); err != nil {
		_ = conn.Close()
		if voiceTimeout(err) {
			return nil, err
		}
		return nil, fmt.Errorf("assemblyai: dial voice socket: %w", ErrSocketGone)
	}
	status, err := readUpgradeStatus(conn)
	if err != nil {
		_ = conn.Close()
		if voiceTimeout(err) {
			return nil, err
		}
		return nil, fmt.Errorf("assemblyai: dial voice socket: %w", ErrSocketGone)
	}
	if status != 101 {
		_ = conn.Close()
		return nil, fmt.Errorf("assemblyai: voice upgrade status %d: %w", status, ErrVoiceUpgrade)
	}
	return conn, nil
}

// writeUpgrade sends the websocket handshake. The voice socket requires
// a bearer token. REST on this host accepts the raw key, so the prefix
// is added here only. The key never enters an error string.
func writeUpgrade(conn net.Conn, host, key string) error {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(voiceWait))
	request := "GET /v1/ws HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(token) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Authorization: Bearer " + key + "\r\n\r\n"
	_, err := io.WriteString(conn, request)
	return err
}

// readUpgradeStatus reads the handshake status code. It stops at the end
// of the headers so a following frame stays on the socket.
func readUpgradeStatus(conn net.Conn) (int, error) {
	_ = conn.SetReadDeadline(time.Now().Add(voiceWait))
	var head []byte
	buf := make([]byte, 1)
	for len(head) < 8192 {
		if _, err := io.ReadFull(conn, buf); err != nil {
			return 0, err
		}
		head = append(head, buf[0])
		if len(head) >= 4 && string(head[len(head)-4:]) == "\r\n\r\n" {
			break
		}
	}
	if len(head) >= 8192 {
		return 0, errors.New("assemblyai: voice headers are too long")
	}
	line, _, _ := strings.Cut(string(head), "\r")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, errors.New("assemblyai: voice status line is short")
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, err
	}
	return code, nil
}

// voiceTimeout reports a wait that ended because the context or the
// socket deadline fired. That is not a gone socket, so the caller retries
// instead of deleting the record.
func voiceTimeout(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded)
}

// wsSocket is one voice websocket this process opened.
type wsSocket struct {
	mu   sync.Mutex
	conn net.Conn
}

// SendText writes one masked text frame.
func (s *wsSocket) SendText(ctx context.Context, payload []byte) error {
	if s == nil {
		return ErrSocketGone
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return ErrSocketGone
	}
	if err := s.armLocked(ctx); err != nil {
		return err
	}
	frame, err := maskFrame(0x81, payload)
	if err != nil {
		return err
	}
	if _, err := s.conn.Write(frame); err != nil {
		// Keep the write error, including a deadline. The frame was not
		// written, so this is not a gone socket and the record stays.
		return fmt.Errorf("assemblyai: write voice socket: %w", err)
	}
	return nil
}

// waitEnded blocks until the provider sends session.ended or the socket
// closes. A close after the end frame still counts as gone, and the
// caller treats that as a written end.
func (s *wsSocket) waitEnded(ctx context.Context) error {
	return s.waitType(ctx, "session.ended")
}

// Close releases the socket. A second close is safe.
func (s *wsSocket) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// waitType reads provider frames until want arrives, the socket closes,
// or the deadline passes. session.error and a close both report
// ErrSocketGone. Ping frames get a pong so the provider does not drop
// the socket while this wait runs.
func (s *wsSocket) waitType(ctx context.Context, want string) error {
	ctx, cancel := context.WithTimeout(ctx, voiceWait)
	defer cancel()
	for range 64 {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		if s.conn == nil {
			s.mu.Unlock()
			return ErrSocketGone
		}
		if err := s.armLocked(ctx); err != nil {
			s.mu.Unlock()
			return err
		}
		conn := s.conn
		s.mu.Unlock()
		opcode, payload, err := readWSFrame(conn)
		if err != nil {
			if voiceTimeout(err) {
				return context.DeadlineExceeded
			}
			return fmt.Errorf("assemblyai: read voice socket: %w", ErrSocketGone)
		}
		switch opcode {
		case 0x9:
			frame, ferr := maskFrame(0x8a, payload)
			if ferr != nil {
				return ferr
			}
			s.mu.Lock()
			if s.conn != nil {
				_, _ = s.conn.Write(frame)
			}
			s.mu.Unlock()
		case 0x8:
			return ErrSocketGone
		case 0x1, 0x2:
			var event voiceEvent
			if err := json.Unmarshal(payload, &event); err != nil {
				continue
			}
			if event.Type == "session.error" {
				return fmt.Errorf("assemblyai: voice session error: %w", ErrSocketGone)
			}
			if event.Type == want {
				return nil
			}
		}
	}
	return fmt.Errorf("assemblyai: voice socket: %w", context.DeadlineExceeded)
}

// armLocked applies a write and read deadline from ctx, capped at
// voiceWait. The caller holds s.mu.
func (s *wsSocket) armLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.conn == nil {
		return ErrSocketGone
	}
	deadline := time.Now().Add(voiceWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := s.conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("assemblyai: voice socket deadline: %w", ErrSocketGone)
	}
	return nil
}

// maskFrame builds one masked client frame. opcode carries the FIN bit.
func maskFrame(opcode byte, payload []byte) ([]byte, error) {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return nil, fmt.Errorf("assemblyai: mask voice frame: %w", err)
	}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	var header []byte
	switch {
	case len(payload) < 126:
		header = []byte{opcode, 0x80 | byte(len(payload))}
	case len(payload) < 65536:
		header = []byte{opcode, 0x80 | 126, byte(len(payload) >> 8), byte(len(payload))}
	default:
		header = []byte{opcode, 0x80 | 127, 0, 0, 0, 0,
			byte(len(payload) >> 24), byte(len(payload) >> 16),
			byte(len(payload) >> 8), byte(len(payload))}
	}
	frame := make([]byte, 0, len(header)+4+len(masked))
	frame = append(frame, header...)
	frame = append(frame, mask[:]...)
	frame = append(frame, masked...)
	return frame, nil
}

// readWSFrame reads one websocket frame. The caller caps how many frames
// it will accept. A frame over voiceFrameLimit is refused.
func readWSFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0f
	length := int(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		length = int(binary.BigEndian.Uint16(ext))
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return 0, nil, err
		}
		wide := binary.BigEndian.Uint64(ext)
		if wide > voiceFrameLimit {
			return 0, nil, errors.New("assemblyai: voice frame is too large")
		}
		length = int(wide)
	}
	if length > voiceFrameLimit {
		return 0, nil, errors.New("assemblyai: voice frame is too large")
	}
	var mask [4]byte
	masked := header[1]&0x80 != 0
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}
