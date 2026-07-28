package tts

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

typedef bool (*qt_cancel_cb)(void *);
typedef bool (*qt_audio_chunk_cb)(const float *, int, void *);

// Trampolines into the Go callbacks in callback.go. qt_synthesize invokes them
// on the calling thread, so the Go side may write to the client directly.
extern bool qtGoChunk(const float *samples, int n, void *user);
extern bool qtGoCancel(void *user);
static bool qt_chunk_trampoline(const float *samples, int n, void *user) {
	return qtGoChunk(samples, n, user);
}
static bool qt_cancel_trampoline(void *user) { return qtGoCancel(user); }

typedef struct {
	int abi_version;
	const char *talker_path;
	const char *codec_path;
	bool use_fa;
	bool clamp_fp16;
} qt_init_params;

typedef struct {
	int abi_version;
	const char *text;
	const char *lang;
	const char *instruct;
	const char *speaker;
	const float *ref_audio_24k;
	int ref_n_samples;
	const char *ref_text;
	int64_t seed;
	int max_new_tokens;
	bool do_sample;
	float temperature;
	int top_k;
	float top_p;
	float repetition_penalty;
	bool subtalker_do_sample;
	float subtalker_temperature;
	int subtalker_top_k;
	float subtalker_top_p;
	const char *dump_dir;
	qt_cancel_cb cancel;
	void *cancel_user_data;
	qt_audio_chunk_cb on_chunk;
	void *on_chunk_user_data;
	float codec_chunk_sec;
	float codec_left_context_sec;
	const float *ref_spk_emb;
	int ref_spk_dim;
	const int32_t *ref_codes;
	int ref_T;
} qt_tts_params;

typedef struct {
	float *samples;
	int n_samples;
	int sample_rate;
	int channels;
} qt_audio;

typedef const char *(*fn_string)(void);
typedef void (*fn_init_defaults)(qt_init_params *);
typedef void (*fn_tts_defaults)(qt_tts_params *);
typedef void *(*fn_init)(const qt_init_params *);
typedef void (*fn_free)(void *);
typedef int (*fn_synthesize)(void *, const qt_tts_params *, qt_audio *);
typedef void (*fn_audio_free)(qt_audio *);

typedef struct {
	void *handle;
	fn_string version;
	fn_string last_error;
	fn_init_defaults init_defaults;
	fn_tts_defaults tts_defaults;
	fn_init init;
	fn_free free_ctx;
	fn_synthesize synthesize;
	fn_audio_free audio_free;
} qt_api;

static const char *qt_dl_error(void) {
	const char *error = dlerror();
	return error ? error : "unknown dynamic-loader error";
}

static void *qt_open_global(const char *path) {
	return dlopen(path, RTLD_NOW | RTLD_GLOBAL);
}

static qt_api *qt_load_api(const char *path) {
	qt_api *api = (qt_api *)calloc(1, sizeof(qt_api));
	if (!api) return NULL;
	api->handle = qt_open_global(path);
	if (!api->handle) {
		free(api);
		return NULL;
	}
	#define LOAD(name) do { \
		*(void **)(&api->name) = dlsym(api->handle, "qt_" #name); \
		if (!api->name) { dlclose(api->handle); free(api); return NULL; } \
	} while (0)
	LOAD(version);
	LOAD(last_error);
	*(void **)(&api->init_defaults) = dlsym(api->handle, "qt_init_default_params");
	*(void **)(&api->tts_defaults) = dlsym(api->handle, "qt_tts_default_params");
	*(void **)(&api->init) = dlsym(api->handle, "qt_init");
	*(void **)(&api->free_ctx) = dlsym(api->handle, "qt_free");
	*(void **)(&api->synthesize) = dlsym(api->handle, "qt_synthesize");
	*(void **)(&api->audio_free) = dlsym(api->handle, "qt_audio_free");
	if (!api->init_defaults || !api->tts_defaults || !api->init || !api->free_ctx ||
	    !api->synthesize || !api->audio_free) {
		dlclose(api->handle);
		free(api);
		return NULL;
	}
	#undef LOAD
	return api;
}

static void qt_unload_api(qt_api *api) {
	if (!api) return;
	if (api->handle) dlclose(api->handle);
	free(api);
}

static const char *qt_api_version(qt_api *api) { return api->version(); }
static const char *qt_api_last_error(qt_api *api) { return api->last_error(); }
static void qt_api_init_defaults(qt_api *api, qt_init_params *params) { api->init_defaults(params); }
static void qt_api_tts_defaults(qt_api *api, qt_tts_params *params) { api->tts_defaults(params); }
static void *qt_api_init(qt_api *api, qt_init_params *params) { return api->init(params); }
static void qt_api_free(qt_api *api, void *ctx) { api->free_ctx(ctx); }
static int qt_api_synthesize(qt_api *api, void *ctx, qt_tts_params *params, qt_audio *audio) {
	return api->synthesize(ctx, params, audio);
}
static void qt_api_audio_free(qt_api *api, qt_audio *audio) { api->audio_free(audio); }

// Cgo cannot take the address of a static function, so hand the pointers out.
static qt_cancel_cb qt_cancel_callback(void) { return qt_cancel_trampoline; }
static qt_audio_chunk_cb qt_chunk_callback(void) { return qt_chunk_trampoline; }
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"
)

type Native struct {
	api       *C.qt_api
	context   unsafe.Pointer
	version   string
	reference unsafe.Pointer
	refCount  int
	maxTokens int
	mu        sync.Mutex
}

type NativeConfig struct {
	LibraryPath string
	LibraryDir  string
	TalkerPath  string
	CodecPath   string
	Reference   []float32
	MaxTokens   int
}

func OpenNative(cfg NativeConfig) (*Native, error) {
	if cfg.LibraryPath == "" {
		cfg.LibraryPath = filepath.Join(cfg.LibraryDir, "libqwen.so")
	}
	for _, name := range []string{
		"libggml-base.so.0", "libggml-cpu.so.0", "libggml-vulkan.so.0", "libggml.so.0",
	} {
		path := filepath.Join(cfg.LibraryDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cpath := C.CString(path)
		handle := C.qt_open_global(cpath)
		C.free(unsafe.Pointer(cpath))
		if handle == nil {
			// Some dependency names are optional aliases. libqwen's own load
			// below is the authoritative error.
			continue
		}
	}
	libraryPath := C.CString(cfg.LibraryPath)
	api := C.qt_load_api(libraryPath)
	C.free(unsafe.Pointer(libraryPath))
	if api == nil {
		return nil, fmt.Errorf("load %s: %s", cfg.LibraryPath, C.GoString(C.qt_dl_error()))
	}
	native := &Native{api: api, maxTokens: cfg.MaxTokens}
	if native.maxTokens == 0 {
		native.maxTokens = 512
	}
	native.version = C.GoString(C.qt_api_version(api))
	var params C.qt_init_params
	C.qt_api_init_defaults(api, &params)
	talker := C.CString(cfg.TalkerPath)
	codec := C.CString(cfg.CodecPath)
	defer C.free(unsafe.Pointer(talker))
	defer C.free(unsafe.Pointer(codec))
	params.talker_path = talker
	params.codec_path = codec
	params.use_fa = true
	params.clamp_fp16 = false
	native.context = C.qt_api_init(api, &params)
	if native.context == nil {
		err := native.lastError()
		C.qt_unload_api(api)
		return nil, fmt.Errorf("initialize qwentts.cpp: %s", err)
	}
	if len(cfg.Reference) == 0 {
		native.Close()
		return nil, fmt.Errorf("voice reference is empty")
	}
	native.refCount = len(cfg.Reference)
	native.reference = C.malloc(C.size_t(len(cfg.Reference)) * C.size_t(unsafe.Sizeof(C.float(0))))
	if native.reference == nil {
		native.Close()
		return nil, fmt.Errorf("allocate voice reference")
	}
	copy(unsafe.Slice((*float32)(native.reference), len(cfg.Reference)), cfg.Reference)
	runtime.SetFinalizer(native, (*Native).Close)
	return native, nil
}

func (n *Native) Version() string { return n.version }

func (n *Native) lastError() string {
	message := C.qt_api_last_error(n.api)
	if message == nil {
		return "unknown qwentts.cpp error"
	}
	return C.GoString(message)
}

// Request is one synthesis call. Build it with NewRequest: Seed 0 is a valid
// deterministic seed, so the zero value cannot mean "unset".
type Request struct {
	Text      string
	Reference []float32 // nil uses the reference the engine was opened with
	Seed      int64     // -1 draws a random seed
	MaxTokens int       // 0 uses the engine's configured limit

	// ChunkSec bounds each codec decode. The default of 24s decodes the whole
	// utterance at once; a fraction of a second trades throughput for
	// time-to-first-audio.
	ChunkSec float64

	// OnChunk, when set, receives decoded audio at StreamRate as it is
	// produced. Returning false aborts synthesis. The slice is only valid for
	// the duration of the call.
	OnChunk func(samples []float32) bool

	// Cancel aborts synthesis when it is closed.
	Cancel <-chan struct{}
}

// StreamRate is the sample rate qwentts.cpp reports to the chunk callback. The
// ABI passes no rate there, only in the final qt_audio.
const StreamRate = 24000

func NewRequest(text string) Request {
	return Request{Text: text, Seed: -1, ChunkSec: 24}
}

type Audio struct {
	Samples []float32
	Rate    int
}

// callbackState is handed to C as an opaque cgo.Handle; see callback.go.
type callbackState struct {
	onChunk  func([]float32) bool
	cancel   <-chan struct{}
	canceled bool
}

func (s *callbackState) stop() bool {
	if s.canceled {
		return true
	}
	select {
	case <-s.cancel:
		s.canceled = true
	default:
	}
	return s.canceled
}

func (n *Native) Synthesize(request Request) (Audio, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.context == nil {
		return Audio{}, fmt.Errorf("qwentts.cpp context is closed")
	}
	var params C.qt_tts_params
	C.qt_api_tts_defaults(n.api, &params)
	ctext := C.CString(request.Text)
	lang := C.CString("english")
	defer C.free(unsafe.Pointer(ctext))
	defer C.free(unsafe.Pointer(lang))
	refPointer := n.reference
	refCount := n.refCount
	if len(request.Reference) != 0 {
		refCount = len(request.Reference)
		refPointer = C.malloc(C.size_t(refCount) * C.size_t(unsafe.Sizeof(C.float(0))))
		if refPointer == nil {
			return Audio{}, fmt.Errorf("allocate request voice reference")
		}
		defer C.free(refPointer)
		copy(unsafe.Slice((*float32)(refPointer), refCount), request.Reference)
	}
	maxTokens := request.MaxTokens
	if maxTokens == 0 {
		maxTokens = n.maxTokens
	}
	params.text = ctext
	params.lang = lang
	params.ref_audio_24k = (*C.float)(refPointer)
	params.ref_n_samples = C.int(refCount)
	params.seed = C.int64_t(request.Seed)
	params.max_new_tokens = C.int(maxTokens)
	// Sampling settings are the ones this voice was tuned and listened to on.
	// Nothing varies them per request, so they are not knobs.
	params.do_sample = true
	params.temperature = 0.9
	params.top_k = 50
	params.top_p = 1
	params.repetition_penalty = 1.05
	params.subtalker_do_sample = true
	params.subtalker_temperature = 0.9
	params.subtalker_top_k = 50
	params.subtalker_top_p = 1
	params.codec_chunk_sec = C.float(request.ChunkSec)
	params.codec_left_context_sec = 2

	state := &callbackState{onChunk: request.OnChunk, cancel: request.Cancel}
	if request.OnChunk != nil || request.Cancel != nil {
		handle := newHandle(state)
		defer handle.Delete()
		user := handle.pointer()
		params.cancel = C.qt_cancel_callback()
		params.cancel_user_data = user
		if request.OnChunk != nil {
			params.on_chunk = C.qt_chunk_callback()
			params.on_chunk_user_data = user
		}
	}

	var audio C.qt_audio
	status := C.qt_api_synthesize(n.api, n.context, &params, &audio)
	if status != 0 {
		C.qt_api_audio_free(n.api, &audio)
		if state.canceled {
			return Audio{}, context.Canceled
		}
		return Audio{}, fmt.Errorf("qwentts.cpp status %d: %s", int(status), n.lastError())
	}
	defer C.qt_api_audio_free(n.api, &audio)
	if state.canceled {
		return Audio{}, context.Canceled
	}
	rate := int(audio.sample_rate)
	if rate == 0 {
		rate = StreamRate
	}
	if audio.samples == nil || audio.n_samples <= 0 {
		return Audio{Rate: rate}, nil
	}
	samples := append([]float32(nil), unsafe.Slice((*float32)(unsafe.Pointer(audio.samples)), int(audio.n_samples))...)
	return Audio{Samples: samples, Rate: rate}, nil
}

func (n *Native) Close() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	runtime.SetFinalizer(n, nil)
	if n.reference != nil {
		C.free(n.reference)
		n.reference = nil
	}
	if n.context != nil && n.api != nil {
		C.qt_api_free(n.api, n.context)
		n.context = nil
	}
	if n.api != nil {
		C.qt_unload_api(n.api)
		n.api = nil
	}
}
