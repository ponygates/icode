//go:build windows

package voice

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Microphone capture using the Windows waveIn API. A single fixed-size buffer
// spans the whole recording window (GLM-ASR caps input at 30s), driven by
// waveInStart / waveInStop + waveInReset. No callback is needed for the
// toggle-based UX (press to record, press again to transcribe), which keeps
// the capture path simple and robust.
var (
	modWinmm                = syscall.NewLazyDLL("winmm.dll")
	procWaveInOpen          = modWinmm.NewProc("waveInOpen")
	procWaveInClose         = modWinmm.NewProc("waveInClose")
	procWaveInStart         = modWinmm.NewProc("waveInStart")
	procWaveInStop          = modWinmm.NewProc("waveInStop")
	procWaveInReset         = modWinmm.NewProc("waveInReset")
	procWaveInPrepareHeader = modWinmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHdr  = modWinmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer     = modWinmm.NewProc("waveInAddBuffer")
	procWaveInGetNumDevs    = modWinmm.NewProc("waveInGetNumDevs")
)

const (
	waveFormatPCM    = 1
	sampleRate       = 16000
	channels         = 1
	bitsPerSample    = 16
	maxSeconds       = 30
	mmSysErrNoDriver = 0x00000005 // MMSYSERR_NODRIVER
)

type waveFormatEx struct {
	wFormatTag      uint16
	nChannels       uint16
	nSamplesPerSec  uint32
	nAvgBytesPerSec uint32
	nBlockAlign     uint16
	wBitsPerSample  uint16
	cbSize          uint16
}

type waveHdr struct {
	lpData          uintptr
	dwBufferLength  uint32
	dwBytesRecorded uint32
	dwUser          uintptr
	dwFlags         uint32
	dwLoops         uint32
	lpNext          uintptr
	reserved        uintptr
}

// Recorder captures the microphone into a WAV buffer. Only one recording may
// be active at a time. Stop returns WAV-encoded PCM (16 kHz mono).
type Recorder struct {
	mu     sync.Mutex
	active bool
	hwi    uintptr
	hdr    waveHdr
	chunk  []byte
	fmt    waveFormatEx
}

// NewRecorder returns an idle recorder.
func NewRecorder() *Recorder { return &Recorder{} }

// Recording reports whether a capture is in progress.
func (r *Recorder) Recording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// Start opens the default microphone and begins capturing.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return fmt.Errorf("正在录音中")
	}
	if n, _, _ := procWaveInGetNumDevs.Call(); n == 0 {
		return fmt.Errorf("未检测到麦克风，请连接后重试")
	}

	chunkSize := uint32(sampleRate) * channels * bitsPerSample / 8 * maxSeconds
	r.chunk = make([]byte, chunkSize)
	r.hdr = waveHdr{
		lpData:          uintptr(unsafe.Pointer(&r.chunk[0])),
		dwBufferLength:  chunkSize,
		dwBytesRecorded: 0,
	}
	r.fmt = waveFormatEx{
		wFormatTag:     waveFormatPCM,
		nChannels:      channels,
		nSamplesPerSec: sampleRate,
		nBlockAlign:    channels * bitsPerSample / 8,
		wBitsPerSample: bitsPerSample,
	}
	r.fmt.nAvgBytesPerSec = r.fmt.nSamplesPerSec * uint32(r.fmt.nBlockAlign)

	ret, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&r.hwi)),
		0, // WAVE_MAPPER → default device
		uintptr(unsafe.Pointer(&r.fmt)),
		0, // no callback
		0, // instance
		0, // flags
	)
	if ret != 0 {
		if ret == mmSysErrNoDriver {
			return fmt.Errorf("找不到音频驱动程序")
		}
		return fmt.Errorf("waveInOpen 失败（错误码 0x%x）", ret)
	}

	procWaveInPrepareHeader.Call(uintptr(r.hwi), uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
	procWaveInAddBuffer.Call(uintptr(r.hwi), uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
	procWaveInStart.Call(uintptr(r.hwi))
	r.active = true

	// Auto-stop after the window so a forgotten session never records forever
	// and the max-sized buffer cannot overflow.
	go func() {
		time.Sleep(maxSeconds * time.Second)
		_, _ = r.Stop()
	}()

	return nil
}

// Stop finalises the capture and returns WAV-encoded PCM. Idempotent and safe
// from any goroutine (including the auto-stop timer).
func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	if !r.active {
		r.mu.Unlock()
		return nil, fmt.Errorf("当前没有在录音")
	}
	r.active = false

	procWaveInStop.Call(uintptr(r.hwi))
	procWaveInReset.Call(uintptr(r.hwi))
	n := int(r.hdr.dwBytesRecorded)
	pcm := make([]byte, n)
	copy(pcm, r.chunk[:n])
	procWaveInUnprepareHdr.Call(uintptr(r.hwi), uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
	procWaveInClose.Call(uintptr(r.hwi))
	r.mu.Unlock()

	if n == 0 {
		return nil, fmt.Errorf("没有捕获到声音（时长过短）")
	}
	return BuildWAV(pcm), nil
}

// BuildWAV wraps raw PCM in a 44-byte RIFF/WAVE header for GLM-ASR upload.
func BuildWAV(pcm []byte) []byte {
	byteRate := uint32(sampleRate) * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataLen := uint32(len(pcm))

	buf := make([]byte, 44, 44+len(pcm))
	copy(buf, "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], 36+dataLen)
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], waveFormatPCM)
	binary.LittleEndian.PutUint16(buf[22:], channels)
	binary.LittleEndian.PutUint32(buf[24:], sampleRate)
	binary.LittleEndian.PutUint32(buf[28:], byteRate)
	binary.LittleEndian.PutUint16(buf[32:], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:], bitsPerSample)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], dataLen)
	return append(buf, pcm...)
}
