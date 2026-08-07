//go:build !windows

package voice

import "fmt"

// Recorder is a no-op on non-Windows platforms: voice input relies on the
// Windows waveIn API. A build-tagged stub keeps the package compiling everywhere.
type Recorder struct{}

// NewRecorder returns a recorder that reports unsupported.
func NewRecorder() *Recorder { return &Recorder{} }

// Recording is always false on unsupported platforms.
func (r *Recorder) Recording() bool { return false }

// Start returns a friendly error on unsupported platforms.
func (r *Recorder) Start() error {
	return fmt.Errorf("语音输入当前仅支持 Windows")
}

// Stop returns a friendly error on unsupported platforms.
func (r *Recorder) Stop() ([]byte, error) {
	return nil, fmt.Errorf("语音输入当前仅支持 Windows")
}