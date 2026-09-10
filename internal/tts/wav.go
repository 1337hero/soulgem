package tts

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

func ReadReference(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	wav, err := parseReference(data, path)
	if err != nil {
		return nil, 0, err
	}
	if wav.rate != 24000 {
		return nil, int(wav.rate), fmt.Errorf("%s: need 24 kHz PCM, got %d Hz", path, wav.rate)
	}
	samples, err := wav.mono(path)
	return samples, int(wav.rate), err
}

type wavReference struct {
	format, channels, bits uint16
	rate                   uint32
	pcm                    []byte
}

func parseReference(data []byte, path string) (wavReference, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return wavReference{}, fmt.Errorf("%s: not a RIFF/WAVE file", path)
	}
	var format, channels, bits uint16
	var rate uint32
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		name := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		off += 8
		if size < 0 || off+size > len(data) {
			return wavReference{}, fmt.Errorf("%s: truncated %s chunk", path, name)
		}
		switch name {
		case "fmt ":
			if size < 16 {
				return wavReference{}, fmt.Errorf("%s: short fmt chunk", path)
			}
			format = binary.LittleEndian.Uint16(data[off:])
			channels = binary.LittleEndian.Uint16(data[off+2:])
			rate = binary.LittleEndian.Uint32(data[off+4:])
			bits = binary.LittleEndian.Uint16(data[off+14:])
		case "data":
			pcm = data[off : off+size]
		}
		off += size
		if off%2 != 0 {
			off++
		}
	}
	if channels == 0 || len(pcm) == 0 {
		return wavReference{}, fmt.Errorf("%s: missing WAV format or audio data", path)
	}
	return wavReference{format, channels, bits, rate, pcm}, nil
}

func (w wavReference) mono(path string) ([]float32, error) {
	bytesPerSample := int(w.bits / 8)
	if bytesPerSample == 0 || len(w.pcm)%(bytesPerSample*int(w.channels)) != 0 {
		return nil, fmt.Errorf("%s: invalid WAV sample layout", path)
	}
	var sample func([]byte) float32
	switch {
	case w.format == 1 && w.bits == 16:
		sample = func(b []byte) float32 { return float32(int16(binary.LittleEndian.Uint16(b))) / 32768 }
	case w.format == 3 && w.bits == 32:
		sample = func(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }
	default:
		return nil, fmt.Errorf("%s: unsupported WAV format=%d bits=%d", path, w.format, w.bits)
	}
	frames := len(w.pcm) / bytesPerSample / int(w.channels)
	out := make([]float32, frames)
	for frame := range frames {
		var sum float32
		for channel := range int(w.channels) {
			off := (frame*int(w.channels) + channel) * bytesPerSample
			sum += sample(w.pcm[off:])
		}
		out[frame] = sum / float32(w.channels)
	}
	return out, nil
}

// streamingSize marks a RIFF whose total length is not yet known. Clients read
// until the connection closes.
const streamingSize = 0xffffffff

func header(dataSize uint32, rate int) []byte {
	var data bytes.Buffer
	data.Grow(44)
	data.WriteString("RIFF")
	total := streamingSize
	if dataSize != streamingSize {
		total = 36 + int(dataSize)
	}
	_ = binary.Write(&data, binary.LittleEndian, uint32(total))
	data.WriteString("WAVEfmt ")
	_ = binary.Write(&data, binary.LittleEndian, uint32(16))
	_ = binary.Write(&data, binary.LittleEndian, uint16(1))
	_ = binary.Write(&data, binary.LittleEndian, uint16(1))
	_ = binary.Write(&data, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&data, binary.LittleEndian, uint32(rate*2))
	_ = binary.Write(&data, binary.LittleEndian, uint16(2))
	_ = binary.Write(&data, binary.LittleEndian, uint16(16))
	data.WriteString("data")
	_ = binary.Write(&data, binary.LittleEndian, dataSize)
	return data.Bytes()
}

// StreamHeader is a WAV header with sentinel sizes, for audio whose length is
// unknown when the first byte goes out.
func StreamHeader(rate int) []byte { return header(streamingSize, rate) }

// AppendPCM16 appends samples as little-endian signed 16-bit PCM.
func AppendPCM16(dst []byte, samples []float32) []byte {
	for _, sample := range samples {
		sample = max(-1, min(1, sample))
		value := int16(-32768)
		if sample > -1 {
			value = int16(math.Round(float64(sample * 32767)))
		}
		dst = binary.LittleEndian.AppendUint16(dst, uint16(value))
	}
	return dst
}

func EncodeWAV(samples []float32, rate int) []byte {
	out := make([]byte, 0, 44+len(samples)*2)
	out = append(out, header(uint32(len(samples)*2), rate)...)
	return AppendPCM16(out, samples)
}
