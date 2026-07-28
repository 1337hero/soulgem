package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"soulgem/internal/tts"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	project := filepath.Join(home, "Experiments/voice/qwen3-tts-fast")
	models := getenv("TTS_GGUF_DIR", filepath.Join(project, "models"))
	talker := filepath.Join(models, getenv("TTS_TALKER", "qwen-talker-0.6b-base-Q6_K.gguf"))
	codec := filepath.Join(models, getenv("TTS_CODEC", "qwen-tokenizer-12hz-F32.gguf"))
	referencePath := expandHome(getenv("TTS_REF", filepath.Join(project, "voices/voice_ref_60s.wav")), home)
	libraryDir, err := nativeLibraryDir(project)
	if err != nil {
		return err
	}
	maxTokens, err := strconv.Atoi(getenv("TTS_MAX_NEW_TOKENS", "512"))
	if err != nil {
		return fmt.Errorf("TTS_MAX_NEW_TOKENS: %w", err)
	}
	chunkSec, err := strconv.ParseFloat(getenv("TTS_CHUNK_SEC", "0.5"), 64)
	if err != nil {
		return fmt.Errorf("TTS_CHUNK_SEC: %w", err)
	}
	port, err := strconv.Atoi(getenv("TTS_PORT", "8123"))
	if err != nil {
		return fmt.Errorf("TTS_PORT: %w", err)
	}
	// Vulkan and HIP ordinals differ; index 1 is PCI 0000:06:00.0 here. The
	// default ICD can also pick a stale NVIDIA entry, so RADV is forced.
	if os.Getenv("GGML_VK_VISIBLE_DEVICES") == "" {
		_ = os.Setenv("GGML_VK_VISIBLE_DEVICES", "1")
	}
	if os.Getenv("VK_DRIVER_FILES") == "" {
		_ = os.Setenv("VK_DRIVER_FILES", "/usr/share/vulkan/icd.d/radeon_icd.json")
	}
	reference, _, err := tts.ReadReference(referencePath)
	if err != nil {
		return err
	}
	log.Printf("loading %s (Vulkan)...", filepath.Base(talker))
	started := time.Now()
	native, err := tts.OpenNative(tts.NativeConfig{
		LibraryDir: libraryDir, TalkerPath: talker, CodecPath: codec,
		Reference: reference, MaxTokens: maxTokens,
	})
	if err != nil {
		return err
	}
	defer native.Close()
	log.Printf("ready in %.1fs on port %d (qwentts.cpp %s)",
		time.Since(started).Seconds(), port, native.Version())
	if err := warmUp(native); err != nil {
		return err
	}
	log.Print("warmed up")

	handler := &server{
		engine:     native,
		references: newReferences(home, referencePath, reference),
		talker:     filepath.Base(talker),
		maxTokens:  maxTokens,
		chunkSec:   chunkSec,
	}
	httpServer := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Handler:           handler.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return httpServer.ListenAndServe()
}

// warmUp compiles the Vulkan shaders before the port opens. It is deliberately
// bounded and deterministic: the production 512-token limit would generate a
// full utterance's worth of frames just to warm the pipeline.
func warmUp(native *tts.Native) error {
	request := tts.NewRequest("Ready.")
	request.Seed = 0
	request.MaxTokens = 64
	_, err := native.Synthesize(request)
	return err
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func nativeLibraryDir(project string) (string, error) {
	if explicit := os.Getenv("QWENTTS_CPP_LIB_DIR"); explicit != "" {
		return explicit, nil
	}
	matches, err := filepath.Glob(filepath.Join(
		project, "runtime/lib/*/site-packages/qwentts_cpp/lib/libqwen.so",
	))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("could not find qwentts.cpp native library under %s; set QWENTTS_CPP_LIB_DIR", project)
	}
	return filepath.Dir(matches[0]), nil
}

func init() {
	log.SetFlags(0)
	log.SetPrefix("")
}
