package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"soulgem/internal/tts"
)

// engine is the slice of the native TTS binding the HTTP layer needs, so the
// handlers can be tested without a GPU.
type engine interface {
	Synthesize(tts.Request) (tts.Audio, error)
	Version() string
}

// references caches decoded voice clone references by path. qwentts.cpp
// re-encodes the reference on every call anyway, so this only saves the WAV
// decode.
type references struct {
	mu     sync.Mutex
	home   string
	byPath map[string][]float32
}

func newReferences(home, defaultPath string, defaultReference []float32) *references {
	return &references{home: home, byPath: map[string][]float32{defaultPath: defaultReference}}
}

// load returns nil for an empty path, meaning "the engine's default voice".
func (r *references) load(path string) ([]float32, error) {
	if path == "" {
		return nil, nil
	}
	path = expandHome(path, r.home)
	r.mu.Lock()
	cached, ok := r.byPath[path]
	r.mu.Unlock()
	if ok {
		return cached, nil
	}
	loaded, _, err := tts.ReadReference(path)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.byPath[path] = loaded
	r.mu.Unlock()
	return loaded, nil
}

type server struct {
	engine     engine
	references *references
	talker     string
	maxTokens  int
	chunkSec   float64
}

type speakRequest struct {
	Text string `json:"text"`
	Ref  string `json:"ref"`
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /speak", s.speak)
	mux.HandleFunc("POST /speak_stream", s.speakStream)
	return mux
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "backend": "vulkan/qwentts.cpp-go",
		"lib": s.engine.Version(), "talker": s.talker,
	})
}

// request decodes the body and resolves the voice reference. Both failures are
// the client's fault, so both answer 400 with a JSON error like the Python
// server did.
func (s *server) request(w http.ResponseWriter, r *http.Request) (tts.Request, bool) {
	var body speakRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return tts.Request{}, false
	}
	reference, err := s.references.load(body.Ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return tts.Request{}, false
	}
	request := tts.NewRequest(strings.TrimSpace(body.Text))
	request.Reference = reference
	request.MaxTokens = s.maxTokens
	request.Cancel = r.Context().Done()
	return request, true
}

func (s *server) speak(w http.ResponseWriter, r *http.Request) {
	request, ok := s.request(w, r)
	if !ok {
		return
	}
	started := time.Now()
	audio, err := s.engine.Synthesize(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	wav := tts.EncodeWAV(audio.Samples, audio.Rate)
	duration, elapsed := seconds(len(audio.Samples), audio.Rate), time.Since(started).Seconds()
	log.Printf("[tts-go] %d chars -> %.1fs audio in %.2fs (RTF %.2f)",
		len(request.Text), duration, elapsed, elapsed/duration)
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Length", strconv.Itoa(len(wav)))
	_, _ = w.Write(wav)
}

// speakStream answers with a chunked WAV whose RIFF sizes are the streaming
// sentinel: the header goes out before the total length is known, so the client
// reads until the connection closes.
func (s *server) speakStream(w http.ResponseWriter, r *http.Request) {
	request, ok := s.request(w, r)
	if !ok {
		return
	}
	request.ChunkSec = s.chunkSec
	started := time.Now()

	var (
		wrote     bool
		total     int
		firstAt   time.Duration
		writeFail error
	)
	request.OnChunk = func(samples []float32) bool {
		out := make([]byte, 0, len(samples)*2+44)
		if !wrote {
			out = append(out, tts.StreamHeader(tts.StreamRate)...)
			firstAt = time.Since(started)
		}
		out = tts.AppendPCM16(out, samples)
		if _, err := w.Write(out); err != nil {
			writeFail = err
			return false
		}
		// Flushing per chunk is the whole point: without it net/http buffers
		// and time-to-first-audio collapses back to the non-streaming path.
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		wrote = true
		total += len(samples)
		return true
	}

	w.Header().Set("Content-Type", "audio/wav")
	audio, err := s.engine.Synthesize(request)
	if writeFail != nil || errors.Is(err, context.Canceled) {
		return
	}
	if err != nil {
		if !wrote {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	// A build whose codec never fires the callback still owes the caller audio.
	if !wrote {
		w.Header().Set("Content-Length", strconv.Itoa(44+len(audio.Samples)*2))
		_, _ = w.Write(tts.EncodeWAV(audio.Samples, audio.Rate))
		total = len(audio.Samples)
	}
	log.Printf("[tts-go] stream %d chars -> %.1fs audio, TTFA %.0fms, total %.2fs",
		len(request.Text), seconds(total, tts.StreamRate),
		float64(firstAt.Milliseconds()), time.Since(started).Seconds())
}

// seconds is audio duration; a zero rate would only come from a broken engine
// and must not divide by zero in a log line.
func seconds(samples, rate int) float64 {
	if rate == 0 {
		return 0
	}
	return float64(samples) / float64(rate)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
