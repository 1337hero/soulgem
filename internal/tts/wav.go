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
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("%s: not a RIFF/WAVE file", path)
	}
	var format, channels, bits uint16
	var rate uint32
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		name := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		off += 8
		if size < 0 || off+size > len(data) {
			return nil, 0, fmt.Errorf("%s: truncated %s chunk", path, name)
		}
		switch name {
		case "fmt ":
			if size < 16 {
				return nil, 0, fmt.Errorf("%s: short fmt chunk", path)
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
		return nil, 0, fmt.Errorf("%s: missing WAV format or audio data", path)
	}
	if rate != 24000 {
		return nil, int(rate), fmt.Errorf("%s: need 24 kHz PCM, got %d Hz", path, rate)
	}
	bytesPerSample := int(bits / 8)
	if bytesPerSample == 0 || len(pcm)%(bytesPerSample*int(channels)) != 0 {
		return nil, int(rate), fmt.Errorf("%s: invalid WAV sample layout", path)
	}
	frames := len(pcm) / bytesPerSample / int(channels)
	out := make([]float32, frames)
	for frame := range frames {
		var sum float32
		for channel := range int(channels) {
			off := (frame*int(channels) + channel) * bytesPerSample
			switch {
			case format == 1 && bits == 16:
				sum += float32(int16(binary.LittleEndian.Uint16(pcm[off:]))) / 32768
			case format == 3 && bits == 32:
				sum += math.Float32frombits(binary.LittleEndian.Uint32(pcm[off:]))
			default:
				return nil, int(rate), fmt.Errorf("%s: unsupported WAV format=%d bits=%d", path, format, bits)
			}
		}
		out[frame] = sum / float32(channels)
	}
	return out, int(rate), nil
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
