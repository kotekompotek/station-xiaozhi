package voice

import (
	"encoding/json/v2"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// fakeAssistant accepts one device connection and exposes what it received.
type fakeAssistant struct {
	server   *httptest.Server
	headers  chan map[string]string
	received chan string
	conn     chan *websocket.Conn
}

func newFakeAssistant(t *testing.T) *fakeAssistant {
	f := &fakeAssistant{headers: make(chan map[string]string, 1), received: make(chan string, 16), conn: make(chan *websocket.Conn, 1)}
	f.server = httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		r := ws.Request()
		f.headers <- map[string]string{"auth": r.Header.Get("Authorization"), "device": r.Header.Get("Device-Id"), "client": r.Header.Get("Client-Id")}
		var hello string
		if websocket.Message.Receive(ws, &hello) != nil {
			return
		}
		f.received <- hello
		_ = websocket.Message.Send(ws, `{"type":"hello","transport":"websocket","session_id":"s1","audio_params":{"sample_rate":24000}}`)
		f.conn <- ws
		for {
			var msg string
			if websocket.Message.Receive(ws, &msg) != nil {
				return
			}
			f.received <- msg
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAssistant) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case msg := <-f.received:
		var v map[string]any
		if err := json.Unmarshal([]byte(msg), &v); err != nil {
			t.Fatal(err, msg)
		}
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("assistant received nothing")
	}
	return nil
}

func connectFake(t *testing.T) (*fakeAssistant, *xiaozhiCall, *websocket.Conn) {
	f := newFakeAssistant(t)
	id := xiaozhiIdentity{DeviceID: "02:00:00:00:00:01", ClientID: "client-1"}
	conn, err := dialXiaozhi(t.Context(), xiaozhiServer{URL: "ws" + strings.TrimPrefix(f.server.URL, "http"), Token: "tok"}, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	call := &xiaozhiCall{conn: conn, report: log.New(io.Discard, "", 0)}
	if err := call.hello(); err != nil || call.session != "s1" {
		t.Fatal("hello failed", err, call.session)
	}
	if h := <-f.headers; h["auth"] != "Bearer tok" || h["device"] != id.DeviceID || h["client"] != id.ClientID {
		t.Fatal("handshake headers", h)
	}
	if hello := f.next(t); hello["type"] != "hello" || hello["audio_params"].(map[string]any)["frame_duration"] != 60.0 {
		t.Fatal("device hello", hello)
	}
	return f, call, <-f.conn
}

func TestXiaozhiMCPOffersEndTool(t *testing.T) {
	f, call, _ := connectFake(t)
	end := func(error) { t.Fatal("MCP ended the call") }
	call.handle([]byte(`{"type":"mcp","payload":{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}}`), end)
	if reply := f.next(t); reply["payload"].(map[string]any)["result"] == nil || reply["session_id"] != "s1" {
		t.Fatal("initialize", reply)
	}
	call.handle([]byte(`{"type":"mcp","payload":{"jsonrpc":"2.0","method":"tools/list","id":2}}`), end)
	tools := f.next(t)["payload"].(map[string]any)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != endToolName {
		t.Fatal("tools/list", tools)
	}
	call.handle([]byte(`{"type":"mcp","payload":{"jsonrpc":"2.0","method":"notifications/initialized"}}`), end)
	call.handle([]byte(`{"type":"mcp","payload":{"jsonrpc":"2.0","method":"tools/call","id":3,"params":{"name":"self.reboot"}}}`), end)
	if reply := f.next(t); reply["payload"].(map[string]any)["error"] == nil {
		t.Fatal("unknown tool was accepted", reply)
	}
	call.handle([]byte(`{"type":"mcp","payload":{"jsonrpc":"2.0","method":"tools/call","id":4,"params":{"name":"`+endToolName+`","arguments":{}}}}`), end)
	if reply := f.next(t); reply["payload"].(map[string]any)["id"] != 4.0 || call.endRequested.Load() == 0 {
		t.Fatal("end tool", reply)
	}
}

func TestXiaozhiSpeechTurnsAndGoodbye(t *testing.T) {
	f, call, _ := connectFake(t)
	e := newStationEpoch(t.Context(), 1)
	defer e.revoke(errAlicePriority)
	var ended error
	end := func(err error) { ended = err }
	call.handle([]byte(`{"type":"tts","state":"start"}`), end)
	if !call.speaking.Load() || call.finished(e.output) {
		t.Fatal("speaking state")
	}
	call.handle([]byte(`{"type":"tts","state":"stop"}`), end)
	if listen := f.next(t); listen["type"] != "listen" || listen["state"] != "start" || listen["mode"] != "auto" {
		t.Fatal("no listen after the assistant spoke", listen)
	}
	call.handle([]byte(`{"type":"stt","text":"Какая погода?"}`), end)
	if ended != nil || call.finished(e.output) {
		t.Fatal("ordinary speech ended the call")
	}
	call.endRequested.Store(time.Now().UnixNano())
	call.handle([]byte(`{"type":"tts","state":"start"}`), end)
	if call.finished(e.output) {
		t.Fatal("hung up before the goodbye")
	}
	call.handle([]byte(`{"type":"tts","state":"stop"}`), end)
	if !call.finished(e.output) {
		t.Fatal("goodbye did not end the call")
	}
	call.handle([]byte(`{"type":"stt","text":"Ну всё, пока!"}`), end)
	if !errors.Is(ended, errProtocolHangup) {
		t.Fatal("end phrase did not hang up", ended)
	}
}

func TestXiaozhiIdleAndServerClose(t *testing.T) {
	_, call, server := connectFake(t)
	e := newStationEpoch(t.Context(), 1)
	defer e.revoke(errAlicePriority)
	call.activity.Store(time.Now().Add(-idleTimeout).UnixNano())
	if !call.finished(e.output) {
		t.Fatal("idle call stayed open")
	}
	dec, err := newDecoder()
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go call.receive(t.Context(), e.output, dec, func(err error) { ended <- err })
	server.Close()
	select {
	case err := <-ended:
		if !errors.Is(err, errProtocolHangup) && !errors.Is(err, errCallDropped) {
			t.Fatal("server close", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server close was not noticed")
	}
}

func TestLoadIdentityKeepsSavedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xiaozhi.json")
	if err := os.WriteFile(path, []byte(`{"device_id":"02:00:00:00:00:01","client_id":"bound"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, err := loadIdentity(path); err != nil || id.ClientID != "bound" {
		t.Fatal("saved identity was replaced", id, err)
	}
}
