package tts

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeWAV(t *testing.T) {
	data := EncodeWAV([]float32{-1, 0, 1}, 24000)
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("invalid WAV: %q", data[:12])
	}
	if len(data) != 44+6 {
		t.Fatalf("got %d bytes, want a 44-byte header and 3 samples", len(data))
	}
	if got := binary.LittleEndian.Uint32(data[4:]); got != 36+6 {
		t.Errorf("RIFF size = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[20:]); got != 1 {
		t.Errorf("format = %d, want PCM", got)
	}
	if got := binary.LittleEndian.Uint16(data[22:]); got != 1 {
		t.Errorf("channels = %d, want mono", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:]); got != 24000 {
		t.Errorf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint16(data[34:]); got != 16 {
		t.Errorf("bit depth = %d", got)
	}
	if got := binary.LittleEndian.Uint32(data[40:]); got != 6 {
		t.Errorf("data size = %d", got)
	}
	samples := []int16{
		int16(binary.LittleEndian.Uint16(data[44:])),
		int16(binary.LittleEndian.Uint16(data[46:])),
		int16(binary.LittleEndian.Uint16(data[48:])),
	}
	if samples[0] != -32768 || samples[1] != 0 || samples[2] != 32767 {
		t.Errorf("samples = %v", samples)
	}
}

func TestAppendPCM16Clamps(t *testing.T) {
	data := AppendPCM16(nil, []float32{-2, 2, 0.5, -0.5})
	got := make([]int16, 4)
	for i := range got {
		got[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	if got[0] != -32768 || got[1] != 32767 {
		t.Errorf("out-of-range samples = %v, want them clamped", got[:2])
	}
	if got[2] != 16384 || got[3] != -16384 {
		t.Errorf("half-scale samples = %v", got[2:])
	}
}

func TestStreamHeaderUsesSentinelSizes(t *testing.T) {
	header := StreamHeader(24000)
	if len(header) != 44 {
		t.Fatalf("header is %d bytes", len(header))
	}
	if got := binary.LittleEndian.Uint32(header[4:]); got != 0xffffffff {
		t.Errorf("RIFF size = %#x", got)
	}
	if got := binary.LittleEndian.Uint32(header[40:]); got != 0xffffffff {
		t.Errorf("data size = %#x", got)
	}
	// Everything except the two sizes matches a complete WAV's header.
	complete := EncodeWAV(nil, 24000)
	for _, span := range [][2]int{{0, 4}, {8, 40}} {
		if string(header[span[0]:span[1]]) != string(complete[span[0]:span[1]]) {
			t.Errorf("bytes %d..%d differ from a complete header", span[0], span[1])
		}
	}
}

// referenceWAV writes a WAV the way ffmpeg would, for the reader to consume.
func referenceWAV(t *testing.T, rate int, channels int, bits int, format int, pcm []byte) string {
	t.Helper()
	var out []byte
	u32 := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	u16 := func(v uint16) { out = binary.LittleEndian.AppendUint16(out, v) }
	out = append(out, "RIFF"...)
	u32(uint32(36 + len(pcm)))
	out = append(out, "WAVEfmt "...)
	u32(16)
	u16(uint16(format))
	u16(uint16(channels))
	u32(uint32(rate))
	u32(uint32(rate * channels * bits / 8))
	u16(uint16(channels * bits / 8))
	u16(uint16(bits))
	out = append(out, "data"...)
	u32(uint32(len(pcm)))
	out = append(out, pcm...)

	path := filepath.Join(t.TempDir(), "ref.wav")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadReferenceRoundTrip(t *testing.T) {
	want := []float32{0, 0.5, -0.5, 1}
	path := filepath.Join(t.TempDir(), "voice.wav")
	if err := os.WriteFile(path, EncodeWAV(want, 24000), 0o644); err != nil {
		t.Fatal(err)
	}
	got, rate, err := ReadReference(path)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 24000 {
		t.Errorf("rate = %d", rate)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-4 {
			t.Errorf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestReadReferenceDownmixesStereo(t *testing.T) {
	// Two frames of two channels: (1, -1) averages to 0, (0.5, 0.5) stays.
	pcm := []byte{}
	for _, sample := range []int16{32767, -32768, 16384, 16384} {
		pcm = binary.LittleEndian.AppendUint16(pcm, uint16(sample))
	}
	got, _, err := ReadReference(referenceWAV(t, 24000, 2, 16, 1, pcm))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames, want 2", len(got))
	}
	if math.Abs(float64(got[0])) > 1e-4 {
		t.Errorf("frame 0 = %v, want the channels averaged to 0", got[0])
	}
	if math.Abs(float64(got[1]-0.5)) > 1e-3 {
		t.Errorf("frame 1 = %v, want 0.5", got[1])
	}
}

func TestReadReferenceReadsFloat32(t *testing.T) {
	pcm := []byte{}
	for _, sample := range []float32{0.25, -0.75} {
		pcm = binary.LittleEndian.AppendUint32(pcm, math.Float32bits(sample))
	}
	got, _, err := ReadReference(referenceWAV(t, 24000, 1, 32, 3, pcm))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0.25 || got[1] != -0.75 {
		t.Errorf("samples = %v", got)
	}
}

func TestReadReferenceRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	notRIFF := filepath.Join(dir, "text.wav")
	if err := os.WriteFile(notRIFF, []byte("this is not audio at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, path, match string }{
		{"missing", filepath.Join(dir, "absent.wav"), "no such file"},
		{"not a RIFF", notRIFF, "not a RIFF/WAVE file"},
		{"wrong sample rate", referenceWAV(t, 48000, 1, 16, 1, make([]byte, 4)), "need 24 kHz"},
		{"unsupported depth", referenceWAV(t, 24000, 1, 8, 1, make([]byte, 4)), "unsupported WAV format"},
		{"no audio", referenceWAV(t, 24000, 1, 16, 1, nil), "missing WAV format or audio data"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := ReadReference(test.path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.match) {
				t.Fatalf("err = %q, want it to mention %q", err, test.match)
			}
		})
	}
}

func TestNewRequestDefaults(t *testing.T) {
	request := NewRequest("hello")
	// Seed 0 is a valid deterministic seed, so the default has to be explicit.
	if request.Seed != -1 {
		t.Errorf("Seed = %d, want a random seed by default", request.Seed)
	}
	// 24 seconds means "decode the whole utterance at once"; only the
	// streaming endpoint lowers it.
	if request.ChunkSec != 24 {
		t.Errorf("ChunkSec = %v", request.ChunkSec)
	}
	if request.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want the engine's own limit by default", request.MaxTokens)
	}
	if request.OnChunk != nil || request.Cancel != nil {
		t.Error("streaming and cancellation are opt-in")
	}
}

func TestCallbackStateStopsOnce(t *testing.T) {
	cancel := make(chan struct{})
	state := &callbackState{cancel: cancel}
	if state.stop() {
		t.Fatal("an open channel should not stop synthesis")
	}
	close(cancel)
	if !state.stop() || !state.canceled {
		t.Fatal("a closed channel should stop synthesis")
	}
	// Once cancelled it stays cancelled, without re-reading the channel.
	state.cancel = nil
	if !state.stop() {
		t.Fatal("cancellation should be sticky")
	}
}
