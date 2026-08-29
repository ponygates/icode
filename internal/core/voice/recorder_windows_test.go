//go:build windows

package voice

import (
	"encoding/binary"
	"testing"
)

// TestBuildWAV verifies the 44-byte RIFF/WAVE header the recorder prepends to
// raw PCM: it must be a standard 16kHz 16-bit mono PCM WAV so ASR providers
// (Baidu in particular — format "wav") can parse it. Any header corruption
// here breaks every provider's transcription silently.
func TestBuildWAV(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	out := BuildWAV(pcm)

	if len(out) != 44+len(pcm) {
		t.Fatalf("BuildWAV length = %d, want %d", len(out), 44+len(pcm))
	}
	// RIFF/WAVE magic + fmt chunk.
	if string(out[0:4]) != "RIFF" {
		t.Errorf("missing RIFF magic: %q", out[0:4])
	}
	if string(out[8:12]) != "WAVE" {
		t.Errorf("missing WAVE magic: %q", out[8:12])
	}
	if string(out[12:16]) != "fmt " {
		t.Errorf("missing fmt chunk: %q", out[12:16])
	}
	if string(out[36:40]) != "data" {
		t.Errorf("missing data chunk: %q", out[36:40])
	}

	// RIFF size field = 36 + dataLen (44-byte header minus the 8-byte RIFF tag).
	if want := uint32(36 + len(pcm)); binary.LittleEndian.Uint32(out[4:8]) != want {
		t.Errorf("RIFF size = %d, want %d", binary.LittleEndian.Uint32(out[4:8]), want)
	}
	// fmt subchunk: PCM=1, mono, 16kHz, 16-bit.
	if tag := binary.LittleEndian.Uint16(out[20:22]); tag != waveFormatPCM {
		t.Errorf("format tag = %d, want waveFormatPCM(%d)", tag, waveFormatPCM)
	}
	if ch := binary.LittleEndian.Uint16(out[22:24]); ch != 1 {
		t.Errorf("channels = %d, want 1", ch)
	}
	if rate := binary.LittleEndian.Uint32(out[24:28]); rate != sampleRate {
		t.Errorf("sample rate = %d, want %d", rate, sampleRate)
	}
	if bits := binary.LittleEndian.Uint16(out[34:36]); bits != bitsPerSample {
		t.Errorf("bits per sample = %d, want %d", bits, bitsPerSample)
	}
	// data chunk size field.
	if want := uint32(len(pcm)); binary.LittleEndian.Uint32(out[40:44]) != want {
		t.Errorf("data size = %d, want %d", binary.LittleEndian.Uint32(out[40:44]), want)
	}
	// PCM bytes must follow the header untouched.
	for i, b := range pcm {
		if out[44+i] != b {
			t.Errorf("pcm byte %d = %#x, want %#x", i, out[44+i], b)
		}
	}
}

// TestBuildWAVEmptyPCM covers the degenerate case (silence / very short clip).
func TestBuildWAVEmptyPCM(t *testing.T) {
	out := BuildWAV(nil)
	if len(out) != 44 {
		t.Fatalf("BuildWAV(nil) length = %d, want 44", len(out))
	}
	if want := uint32(36); binary.LittleEndian.Uint32(out[4:8]) != want {
		t.Errorf("RIFF size for empty = %d, want %d", binary.LittleEndian.Uint32(out[4:8]), want)
	}
}
