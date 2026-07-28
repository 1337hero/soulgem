package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"soulgem/internal/tts"
)

// fakeEngine stands in for the native binding so the HTTP contract can be
// tested without a GPU or a 600 MB model.
type fakeEngine struct {
	requests []tts.Request
	chunks   [][]float32
	audio    tts.Audio
	err      error
}

func (f *fakeEngine) Version() string { return "fake" }

func (f *fakeEngine) Synthesize(request tts.Request) (tts.Audio, error) {
	f.requests = append(f.requests, request)
	for _, chunk := range f.chunks {
		if request.OnChunk == nil {
			break
		}
		if !request.OnChunk(chunk) {
			return tts.Audio{}, context.Canceled
		}
	}
	if f.err != nil {
		return tts.Audio{}, f.err
	}
	return f.audio, nil
}

func newServer(t *testing.T, engine *fakeEngine) *server {
	t.Helper()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &server{
		engine:     engine,
		references: newReferences(t.TempDir(), "/voices/default.wav", []float32{0.1, 0.2}),
		talker:     "qwen-talker.gguf",
		maxTokens:  512,
		chunkSec:   0.5,
	}
}

func post(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return recorder
}

func TestHealth(t *testing.T) {
	engine := &fakeEngine{}
	recorder := httptest.NewRecorder()
	newServer(t, engine).routes().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true || body["lib"] != "fake" || body["talker"] != "qwen-talker.gguf" {
		t.Errorf("health = %v", body)
	}
	if body["backend"] != "vulkan/qwentts.cpp-go" {
		t.Errorf("backend = %v", body["backend"])
	}
}

func TestSpeakReturnsCompleteWAV(t *testing.T) {
	engine := &fakeEngine{audio: tts.Audio{Samples: []float32{0, 0.5, -0.5, 1}, Rate: 24000}}
	recorder := post(t, newServer(t, engine).routes(), "/speak", `{"text":"  hello  "}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "audio/wav" {
		t.Errorf("Content-Type = %q", got)
	}
	body := recorder.Body.Bytes()
	if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length = %q, body is %d bytes", got, len(body))
	}
	if string(body[:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
		t.Fatalf("not a WAV: %q", body[:12])
	}
	// A complete response declares its real size, unlike the streaming one.
	if got := binary.LittleEndian.Uint32(body[4:]); got != uint32(36+4*2) {
		t.Errorf("RIFF size = %d", got)
	}
	if got := binary.LittleEndian.Uint32(body[40:]); got != 8 {
		t.Errorf("data size = %d, want 4 samples at 16 bits", got)
	}
	if len(engine.requests) != 1 {
		t.Fatalf("engine called %d times", len(engine.requests))
	}
	request := engine.requests[0]
	if request.Text != "hello" {
		t.Errorf("text = %q, want it trimmed", request.Text)
	}
	if request.MaxTokens != 512 || request.Seed != -1 {
		t.Errorf("MaxTokens = %d, Seed = %d", request.MaxTokens, request.Seed)
	}
	if request.OnChunk != nil {
		t.Error("/speak must not ask for chunks")
	}
	if request.Reference != nil {
		t.Error("no ref in the body means the engine's default voice")
	}
}

func TestSpeakStreamChunksAudio(t *testing.T) {
	engine := &fakeEngine{
		chunks: [][]float32{{0, 0.5}, {-0.5, 1}},
		audio:  tts.Audio{Samples: []float32{0, 0.5, -0.5, 1}, Rate: 24000},
	}
	recorder := post(t, newServer(t, engine).routes(), "/speak_stream", `{"text":"hello"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "audio/wav" {
		t.Errorf("Content-Type = %q", got)
	}
	if recorder.Header().Get("Content-Length") != "" {
		t.Error("a streamed response cannot know its length up front")
	}
	body := recorder.Body.Bytes()
	if len(body) != 44+8 {
		t.Fatalf("got %d bytes, want a 44-byte header and 4 samples", len(body))
	}
	// The sizes are the streaming sentinel: the client reads to end of stream.
	if got := binary.LittleEndian.Uint32(body[4:]); got != 0xffffffff {
		t.Errorf("RIFF size = %#x, want the streaming sentinel", got)
	}
	if got := binary.LittleEndian.Uint32(body[40:]); got != 0xffffffff {
		t.Errorf("data size = %#x, want the streaming sentinel", got)
	}
	if got := binary.LittleEndian.Uint32(body[24:]); got != tts.StreamRate {
		t.Errorf("sample rate = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[46:])); got != 16384 {
		t.Errorf("second sample = %d, want 0.5 as PCM16", got)
	}
	if request := engine.requests[0]; request.ChunkSec != 0.5 {
		t.Errorf("ChunkSec = %v, want the server's configured value", request.ChunkSec)
	}
	if engine.requests[0].OnChunk == nil {
		t.Error("/speak_stream must pass a chunk callback")
	}
}

// TestSpeakStreamFallsBackWhenNoChunksArrive covers a codec build that never
// fires the callback: the caller still has to get its audio.
func TestSpeakStreamFallsBackWhenNoChunksArrive(t *testing.T) {
	engine := &fakeEngine{audio: tts.Audio{Samples: []float32{0, 1}, Rate: 24000}}
	recorder := post(t, newServer(t, engine).routes(), "/speak_stream", `{"text":"hello"}`)
	body := recorder.Body.Bytes()
	if len(body) != 44+4 {
		t.Fatalf("got %d bytes", len(body))
	}
	if got := binary.LittleEndian.Uint32(body[40:]); got != 4 {
		t.Errorf("the fallback should declare its real size, got %#x", got)
	}
}

func TestBadRequestsAnswerWithJSON(t *testing.T) {
	cases := []struct {
		name, path, body string
	}{
		{"malformed JSON", "/speak", `{"text":`},
		{"malformed JSON streaming", "/speak_stream", `not json`},
		{"unreadable reference", "/speak", `{"text":"hi","ref":"/nope/missing.wav"}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recorder := post(t, newServer(t, &fakeEngine{}).routes(), test.path, test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", recorder.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %s", recorder.Body)
			}
			if body["error"] == "" {
				t.Errorf("body = %v, want an error message", body)
			}
		})
	}
}

func TestSynthesisFailureIsReported(t *testing.T) {
	engine := &fakeEngine{err: errors.New("vulkan device lost")}
	recorder := post(t, newServer(t, engine).routes(), "/speak", `{"text":"hi"}`)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "vulkan device lost") {
		t.Errorf("body = %s", recorder.Body)
	}
}

func TestCancelledRequestWritesNothing(t *testing.T) {
	engine := &fakeEngine{err: context.Canceled}
	recorder := post(t, newServer(t, engine).routes(), "/speak", `{"text":"hi"}`)
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %s, want nothing written for a cancelled request", recorder.Body)
	}
}

func TestUnknownRoutesAre404(t *testing.T) {
	for _, path := range []string{"/speak_slowly", "/"} {
		recorder := post(t, newServer(t, &fakeEngine{}).routes(), path, `{}`)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, recorder.Code)
		}
	}
}

func TestReferencesAreLoadedAndCached(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "voice.wav")
	if err := os.WriteFile(path, tts.EncodeWAV([]float32{0.25, -0.25}, 24000), 0o644); err != nil {
		t.Fatal(err)
	}
	references := newReferences(home, "/voices/default.wav", []float32{1})

	first, err := references.load("~/voice.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("loaded %d samples", len(first))
	}
	// Removing the file must not break the second load: it is cached.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := references.load("~/voice.wav"); err != nil {
		t.Errorf("second load = %v, want the cached reference", err)
	}
	if got, err := references.load(""); got != nil || err != nil {
		t.Errorf("an empty ref means the default voice, got %v %v", got, err)
	}
	if _, err := references.load("/voices/default.wav"); err != nil {
		t.Errorf("the startup reference should be pre-cached: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	cases := map[string]string{
		"~":              "/home/test",
		"~/voices/a.wav": "/home/test/voices/a.wav",
		"/abs/a.wav":     "/abs/a.wav",
		"relative.wav":   "relative.wav",
		"~notauser/a":    "~notauser/a",
	}
	for input, want := range cases {
		if got := expandHome(input, "/home/test"); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", input, got, want)
		}
	}
}
