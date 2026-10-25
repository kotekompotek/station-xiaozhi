package voice

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"golang.org/x/net/websocket"
)

const xiaozhiOTAURL = "https://api.tenclass.net/xiaozhi/ota/"
const xiaozhiDefaultURL = "wss://api.tenclass.net/xiaozhi/v1/"

// The device was bound on xiaozhi.me under this board description; keep it stable.
const xiaozhiBoard = "bread-compact-wifi"
const xiaozhiApp = "py-xiaozhi"
const xiaozhiAppVersion = "2.1.2"

const serverRefresh = time.Hour
const helloTimeout = 10 * time.Second
const sendTimeout = 5 * time.Second

// A call with no speech from either side for this long hangs up.
const idleTimeout = 20 * time.Second

// After the assistant agrees to end the call, wait this long for its goodbye.
const goodbyeTimeout = 8 * time.Second

const endToolName = "self.end_conversation"
const endToolDescription = "Завершить голосовой разговор. Вызывай, когда пользователь прощается " +
	"или просит закончить разговор, после короткого прощания."

var errConnectFailed = errors.New("cannot connect to the assistant")
var errNotActivated = errors.New("Station is not bound on xiaozhi.me")
var errCallDropped = errors.New("assistant connection dropped")
var errProtocolHangup = errors.New("assistant call ended")

// activationError carries the code the owner enters at xiaozhi.me to bind this Station.
type activationError struct{ code string }

func (e activationError) Error() string {
	return fmt.Sprintf("%v: enter code %s at https://xiaozhi.me (Add Device)", errNotActivated, e.code)
}

func (e activationError) Unwrap() error { return errNotActivated }

type xiaozhiIdentity struct {
	DeviceID string `json:"device_id"`
	ClientID string `json:"client_id"`
}

// loadIdentity returns the persistent device identity, creating it from the Wi-Fi MAC on first use.
func loadIdentity(path string) (xiaozhiIdentity, error) {
	var id xiaozhiIdentity
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &id) == nil && id.DeviceID != "" && id.ClientID != "" {
		return id, nil
	}
	mac, err := os.ReadFile("/sys/class/net/wlan0/address")
	if err != nil {
		return xiaozhiIdentity{}, errors.New("cannot read the Station MAC address")
	}
	id = xiaozhiIdentity{DeviceID: strings.TrimSpace(string(mac)), ClientID: uuid.New().String()}
	data, err := json.Marshal(id)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil || os.WriteFile(path, data, 0o600) != nil {
		return xiaozhiIdentity{}, errors.New("cannot save the assistant identity")
	}
	return id, nil
}

type xiaozhiServer struct {
	URL, Token, ActivationCode string
}

func fetchServer(ctx context.Context, id xiaozhiIdentity) (xiaozhiServer, error) {
	body, err := json.Marshal(map[string]any{
		"application": map[string]string{"version": xiaozhiAppVersion, "elf_sha256": "unknown"},
		"board":       map[string]string{"type": xiaozhiBoard, "name": xiaozhiApp, "mac": id.DeviceID},
	})
	if err != nil {
		return xiaozhiServer{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xiaozhiOTAURL, bytes.NewReader(body))
	if err != nil {
		return xiaozhiServer{}, err
	}
	req.Header.Set("Device-Id", id.DeviceID)
	req.Header.Set("Client-Id", id.ClientID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", xiaozhiBoard+"/"+xiaozhiApp+"-"+xiaozhiAppVersion)
	req.Header.Set("Accept-Language", "ru-RU")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return xiaozhiServer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return xiaozhiServer{}, fmt.Errorf("OTA returned HTTP %d", resp.StatusCode)
	}
	var reply struct {
		Websocket struct {
			URL   string `json:"url"`
			Token string `json:"token"`
		} `json:"websocket"`
		Activation *struct {
			Code string `json:"code"`
		} `json:"activation"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || json.Unmarshal(data, &reply) != nil {
		return xiaozhiServer{}, errors.New("invalid OTA reply")
	}
	server := xiaozhiServer{URL: reply.Websocket.URL, Token: reply.Websocket.Token}
	if server.URL == "" {
		server.URL = xiaozhiDefaultURL
	}
	if reply.Activation != nil {
		server.ActivationCode = reply.Activation.Code
	}
	return server, nil
}

// xiaozhiDirectory caches the OTA answer so a call does not wait for it.
type xiaozhiDirectory struct {
	identity xiaozhiIdentity
	mu       sync.Mutex
	server   xiaozhiServer
	fetched  time.Time
}

func (d *xiaozhiDirectory) lookup(ctx context.Context, report *log.Logger) (xiaozhiServer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server.URL != "" && time.Since(d.fetched) < serverRefresh {
		return d.server, nil
	}
	server, err := fetchServer(ctx, d.identity)
	if err != nil {
		if ctx.Err() != nil {
			return xiaozhiServer{}, context.Cause(ctx)
		}
		report.Print("Assistant OTA lookup failed: ", err)
		if d.server.URL != "" {
			return d.server, nil
		}
		return xiaozhiServer{URL: xiaozhiDefaultURL, Token: "test-token"}, nil
	}
	if server.ActivationCode != "" {
		err := activationError{server.ActivationCode}
		report.Print(err)
		return server, err
	}
	d.server, d.fetched = server, time.Now()
	return server, nil
}

func (d *xiaozhiDirectory) invalidate() {
	d.mu.Lock()
	d.fetched = time.Time{}
	d.mu.Unlock()
}

type wsFrame struct {
	text bool
	data []byte
}

var frameCodec = websocket.Codec{
	Marshal: func(any) ([]byte, byte, error) { return nil, 0, errors.New("receive-only codec") },
	Unmarshal: func(data []byte, payloadType byte, v any) error {
		f := v.(*wsFrame)
		f.text, f.data = payloadType == websocket.TextFrame, data
		return nil
	},
}

func dialXiaozhi(ctx context.Context, server xiaozhiServer, id xiaozhiIdentity) (*websocket.Conn, error) {
	cfg, err := websocket.NewConfig(server.URL, "https://xiaozhi.me")
	if err != nil {
		return nil, err
	}
	cfg.Header = http.Header{}
	cfg.Header.Set("Authorization", "Bearer "+server.Token)
	cfg.Header.Set("Protocol-Version", "1")
	cfg.Header.Set("Device-Id", id.DeviceID)
	cfg.Header.Set("Client-Id", id.ClientID)
	return cfg.DialContext(ctx)
}

type xiaozhiCall struct {
	conn     *websocket.Conn
	report   *log.Logger
	session  string
	writeMu  sync.Mutex
	speaking atomic.Bool
	activity atomic.Int64
	// Unix nanoseconds when the assistant agreed to end the call; zero otherwise.
	endRequested atomic.Int64
	goodbyeDone  atomic.Bool
}

func (c *xiaozhiCall) touch() { c.activity.Store(time.Now().UnixNano()) }

func (c *xiaozhiCall) send(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(sendTimeout))
	return websocket.Message.Send(c.conn, v)
}

func (c *xiaozhiCall) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dumpEvent(append([]byte("> "), data...))
	return c.send(string(data))
}

func (c *xiaozhiCall) sendAudio(packet []byte) error { return c.send(packet) }

func (c *xiaozhiCall) listen() error {
	return c.sendJSON(map[string]string{"session_id": c.session, "type": "listen", "state": "start", "mode": "auto"})
}

func (c *xiaozhiCall) hello() error {
	err := c.sendJSON(map[string]any{
		"type": "hello", "version": 1, "transport": "websocket", "features": map[string]bool{"mcp": true},
		"audio_params": map[string]any{"format": "opus", "sample_rate": 16000, "channels": 1, "frame_duration": 60},
	})
	if err != nil {
		return err
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(helloTimeout))
	defer c.conn.SetReadDeadline(time.Time{})
	for {
		var f wsFrame
		if err := frameCodec.Receive(c.conn, &f); err != nil {
			return err
		}
		var reply struct {
			Type      string `json:"type"`
			Transport string `json:"transport"`
			SessionID string `json:"session_id"`
		}
		if !f.text || json.Unmarshal(f.data, &reply) != nil || reply.Type != "hello" {
			continue
		}
		if reply.Transport != "websocket" {
			return errors.New("server hello did not accept the WebSocket transport")
		}
		c.session = reply.SessionID
		return nil
	}
}

// receive plays server audio and handles control messages until the connection ends.
func (c *xiaozhiCall) receive(ctx context.Context, out *audio, dec *decoder, end func(error)) {
	for {
		var f wsFrame
		if err := frameCodec.Receive(c.conn, &f); err != nil {
			if ctx.Err() == nil && errors.Is(err, io.EOF) {
				c.report.Print("Assistant closed the conversation.")
				end(errProtocolHangup)
			} else if ctx.Err() == nil {
				end(errCallDropped)
			}
			return
		}
		if !f.text {
			_ = out.play(dec, f.data)
			continue
		}
		dumpEvent(f.data)
		c.handle(f.data, end)
	}
}

func (c *xiaozhiCall) handle(data []byte, end func(error)) {
	var msg struct {
		Type    string         `json:"type"`
		State   string         `json:"state"`
		Text    string         `json:"text"`
		Message string         `json:"message"`
		Payload jsontext.Value `json:"payload"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Type {
	case "stt":
		c.touch()
		c.report.Print("Heard: ", msg.Text)
		if isEndRequest(msg.Text) {
			c.report.Print("End phrase heard; hanging up.")
			end(errProtocolHangup)
		}
	case "tts":
		c.touch()
		switch msg.State {
		case "start":
			c.speaking.Store(true)
		case "sentence_start":
			c.report.Print("Assistant: ", msg.Text)
		case "stop":
			c.speaking.Store(false)
			if c.endRequested.Load() != 0 {
				c.goodbyeDone.Store(true)
			} else if c.listen() != nil {
				end(errCallDropped)
			}
		}
	case "mcp":
		c.answerMCP(msg.Payload)
	case "alert":
		c.report.Print("Assistant alert: ", msg.Message)
	}
}

func (c *xiaozhiCall) answerMCP(payload jsontext.Value) {
	var req struct {
		ID     jsontext.Value `json:"id"`
		Method string         `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(payload, &req) != nil || len(req.ID) == 0 {
		return
	}
	reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	switch {
	case req.Method == "initialize":
		reply["result"] = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": "yandex-station", "version": "1.0"},
		}
	case req.Method == "tools/list":
		reply["result"] = map[string]any{"tools": []any{map[string]any{
			"name": endToolName, "description": endToolDescription,
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		}}}
	case req.Method == "tools/call" && req.Params.Name == endToolName:
		c.report.Print("Assistant agreed to end the conversation.")
		c.endRequested.Store(time.Now().UnixNano())
		reply["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": "true"}}, "isError": false}
	default:
		reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
	}
	_ = c.sendJSON(map[string]any{"session_id": c.session, "type": "mcp", "payload": reply})
}

// finished reports whether the call should hang up normally: after the goodbye has played, or when idle.
func (c *xiaozhiCall) finished(out *audio) bool {
	if requested := c.endRequested.Load(); requested != 0 {
		drained := c.goodbyeDone.Load() && len(out.playback) == 0
		return drained || time.Since(time.Unix(0, requested)) >= goodbyeTimeout
	}
	last := max(c.activity.Load(), out.heard.Load())
	if !c.speaking.Load() && time.Since(time.Unix(0, last)) >= idleTimeout {
		c.report.Print("Conversation idle; hanging up.")
		return true
	}
	return false
}

func runCall(e *stationEpoch, directory *xiaozhiDirectory, capture *stationCapture) (result error) {
	defer func() {
		switch result {
		case nil, errAlicePriority, context.Canceled:
			return
		}
		if !errors.Is(result, errStationAudio) {
			result = voiceCallError{result}
		}
	}()
	ctx := e.ctx
	ringCtx, stopRinging := context.WithCancel(ctx)
	var ringing sync.WaitGroup
	ringing.Go(func() { playRingback(ringCtx) })
	hangUpRingback := func() { stopRinging(); ringing.Wait() }
	defer hangUpRingback()

	setupCtx, finishSetup := context.WithTimeout(ctx, setupTimeout)
	defer finishSetup()
	setupStart := time.Now()
	server, err := directory.lookup(setupCtx, e.report)
	if err != nil {
		return err
	}
	conn, err := dialXiaozhi(setupCtx, server, directory.identity)
	if err != nil {
		directory.invalidate()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fmt.Errorf("%w: %w", errConnectFailed, err)
	}
	callCtx, endCall := context.WithCancelCause(ctx)
	defer endCall(context.Canceled)
	stopClosing := context.AfterFunc(callCtx, func() { _ = conn.Close() })
	defer func() {
		if stopClosing() {
			_ = conn.Close()
		}
	}()
	call := &xiaozhiCall{conn: conn, report: e.report}
	if err := call.hello(); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fmt.Errorf("%w: %w", errConnectFailed, err)
	}
	e.report.Printf("Assistant connected after %v.", time.Since(setupStart).Round(time.Millisecond))
	dec, err := newDecoder()
	if err != nil {
		return err
	}
	if err = e.output.open(); err != nil {
		return err
	}
	defer func() {
		if err := e.output.close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	finishSetup()
	hangUpRingback()
	if err = playReadyCue(ctx); err != nil {
		return err
	}
	if err = capture.beginStreaming(ctx); err != nil {
		return err
	}
	if call.listen() != nil {
		return errCallDropped
	}
	e.report.Print("Voice media ready.")
	call.touch()

	var workers sync.WaitGroup
	defer func() {
		endCall(context.Canceled)
		workers.Wait()
		sent := e.output.sentPackets.Load()
		e.report.Printf("Uplink: %d packets, mean level %d.", sent, e.output.sentLevel.Load()/max(sent, 1))
	}()
	workers.Go(func() { breatheRing(callCtx) })
	workers.Go(func() {
		if err := e.output.send(callCtx, capture.input, call.speaking.Load, call.sendAudio); err != nil {
			endCall(err)
		}
	})
	workers.Go(func() { call.receive(callCtx, e.output, dec, endCall) })

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-callCtx.Done():
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if cause := context.Cause(callCtx); cause != errProtocolHangup {
				return cause
			}
			return nil
		case <-tick.C:
			if call.finished(e.output) {
				return nil
			}
		}
	}
}
