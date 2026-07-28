// Package a11y drives a headless Chrome over the DevTools Protocol.
//
// It speaks WebSocket with the standard library rather than a client library.
// That is a deliberate trade: the protocol needed here is one frame type in each
// direction, which is about a hundred lines, and the alternative is putting a
// dependency in a repository whose whole premise is a static binary with none.
// The same reasoning the rest of this codebase applies to its own dependencies.
package a11y

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// chromeCandidates are the binaries to try, in order.
var chromeCandidates = []string{
	"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
}

// FindChrome locates a browser, or explains that there is not one.
func FindChrome() (string, error) {
	if env := os.Getenv("CHROME"); env != "" {
		return env, nil
	}
	for _, name := range chromeCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no Chrome found; tried %s. Set CHROME to a binary",
		strings.Join(chromeCandidates, ", "))
}

// Browser is a running headless Chrome and the connection to it.
type Browser struct {
	cmd     *exec.Cmd
	conn    *wsConn
	profile string

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan rpcResponse
	closed  bool
}

type rpcResponse struct {
	Result json.RawMessage
	Err    error
}

type rpcMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Launch starts a browser and connects to it.
func Launch() (*Browser, error) {
	bin, err := FindChrome()
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "dpi-a11y-*")
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin,
		"--headless=new",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check",
		"--disable-gpu", "--hide-scrollbars",
		// Determinism: without these, two runs of the same page differ by a
		// subpixel and every screenshot comparison is noise.
		"--force-device-scale-factor=1",
		"--font-render-hinting=none",
		"--disable-lcd-text",
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(profile)
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}

	b := &Browser{cmd: cmd, profile: profile, waiting: map[int]chan rpcResponse{}}

	wsURL, err := waitForDevTools(port, 20*time.Second)
	if err != nil {
		b.Close()
		return nil, err
	}
	conn, err := dialWS(wsURL)
	if err != nil {
		b.Close()
		return nil, err
	}
	b.conn = conn
	go b.readLoop()
	return b, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitForDevTools(port int, within time.Duration) (string, error) {
	deadline := time.Now().Add(within)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	var last error
	for time.Now().Before(deadline) {
		resp, err := http.Get(endpoint)
		if err != nil {
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var v struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		err = json.NewDecoder(resp.Body).Decode(&v)
		resp.Body.Close()
		if err == nil && v.WebSocketDebuggerURL != "" {
			return v.WebSocketDebuggerURL, nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("devtools did not come up on port %d: %v", port, last)
}

func (b *Browser) readLoop() {
	for {
		data, err := b.conn.readMessage()
		if err != nil {
			b.mu.Lock()
			for _, ch := range b.waiting {
				ch <- rpcResponse{Err: err}
			}
			b.waiting = map[int]chan rpcResponse{}
			b.mu.Unlock()
			return
		}
		var m rpcMessage
		if json.Unmarshal(data, &m) != nil || m.ID == 0 {
			continue // an event; this suite polls state rather than listening
		}
		b.mu.Lock()
		ch, ok := b.waiting[m.ID]
		delete(b.waiting, m.ID)
		b.mu.Unlock()
		if !ok {
			continue
		}
		if m.Error != nil {
			ch <- rpcResponse{Err: errors.New(m.Error.Message)}
			continue
		}
		ch <- rpcResponse{Result: m.Result}
	}
}

// call sends one command and waits for its reply.
func (b *Browser) call(sessionID, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if params == nil {
		raw = []byte("{}")
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errors.New("browser is closed")
	}
	b.nextID++
	id := b.nextID
	ch := make(chan rpcResponse, 1)
	b.waiting[id] = ch
	b.mu.Unlock()

	msg, err := json.Marshal(rpcMessage{ID: id, Method: method, Params: raw, SessionID: sessionID})
	if err != nil {
		return err
	}
	if err := b.conn.writeMessage(msg); err != nil {
		return err
	}

	select {
	case resp := <-ch:
		if resp.Err != nil {
			return fmt.Errorf("%s: %w", method, resp.Err)
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-time.After(45 * time.Second):
		return fmt.Errorf("%s: timed out", method)
	}
}

// Close ends the session and reaps the browser.
func (b *Browser) Close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	if b.conn != nil {
		b.conn.Close()
	}
	if b.cmd != nil && b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
		_, _ = b.cmd.Process.Wait()
	}
	if b.profile != "" {
		os.RemoveAll(b.profile)
	}
}

// --- minimal WebSocket ------------------------------------------------------
//
// Only what CDP uses: a client handshake, and text frames in both directions.
// Fragmentation is handled because Chrome fragments large responses — a full
// accessibility tree easily exceeds one frame — and getting that wrong shows up
// as intermittent JSON parse failures rather than as an obvious bug.

type wsConn struct {
	conn net.Conn
	mu   sync.Mutex
	buf  []byte
}

func dialWS(rawURL string) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if u.Port() == "" {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, err
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		conn.Close()
		return nil, err
	}
	nonce := base64.StdEncoding.EncodeToString(key)

	path := u.Path
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + nonce + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}

	// Read the response head byte by byte: anything after the blank line is the
	// first frame and must not be swallowed by a buffered reader.
	var head []byte
	one := make([]byte, 1)
	for !strings.HasSuffix(string(head), "\r\n\r\n") {
		if _, err := io.ReadFull(conn, one); err != nil {
			conn.Close()
			return nil, fmt.Errorf("reading handshake: %w", err)
		}
		head = append(head, one[0])
		if len(head) > 8192 {
			conn.Close()
			return nil, errors.New("handshake response too large")
		}
	}
	if !strings.Contains(string(head), " 101 ") {
		conn.Close()
		return nil, fmt.Errorf("expected 101 Switching Protocols, got:\n%s", head)
	}

	sum := sha1.Sum([]byte(nonce + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if !strings.Contains(string(head), want) {
		conn.Close()
		return nil, errors.New("handshake accept key did not match")
	}
	return &wsConn{conn: conn}, nil
}

func (c *wsConn) Close() error { return c.conn.Close() }

// writeMessage sends one masked text frame.
func (c *wsConn) writeMessage(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var head []byte
	head = append(head, 0x81) // FIN | text
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, byte(0x80|n))
	case n < 1<<16:
		head = append(head, 0x80|126)
		head = binary.BigEndian.AppendUint16(head, uint16(n))
	default:
		head = append(head, 0x80|127)
		head = binary.BigEndian.AppendUint64(head, uint64(n))
	}

	// A client MUST mask. Chrome closes the connection if it does not.
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	head = append(head, mask...)

	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(head); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

// readMessage reassembles one complete message, following continuation frames.
func (c *wsConn) readMessage() ([]byte, error) {
	var message []byte
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8: // close
			return nil, io.EOF
		case 0x9: // ping — reply with pong so Chrome does not drop us
			c.mu.Lock()
			_, _ = c.conn.Write(append([]byte{0x8A, byte(0x80 | len(payload)), 0, 0, 0, 0}, payload...))
			c.mu.Unlock()
			continue
		case 0xA: // pong
			continue
		}
		message = append(message, payload...)
		if fin {
			return message, nil
		}
	}
}

func (c *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	head := make([]byte, 2)
	if _, err = io.ReadFull(c.conn, head); err != nil {
		return
	}
	fin = head[0]&0x80 != 0
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)

	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err = io.ReadFull(c.conn, ext); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(ext))
	case 127:
		ext := make([]byte, 8)
		if _, err = io.ReadFull(c.conn, ext); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(ext)
	}

	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err = io.ReadFull(c.conn, mask); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.conn, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}
