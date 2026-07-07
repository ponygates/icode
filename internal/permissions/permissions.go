package permissions

import (
	"path/filepath"
	"strings"
)

type Level int

const (
	LevelPlan    Level = iota
	LevelAsk
	LevelAuto
	LevelYOLO
)

type Manager struct {
	Mode         Level
	ReadOnlyDirs []string
	DenyDirs     []string
	BashDenyCmds []string
	Workspace    string
}

func New(mode string, readOnly, deny, bashDeny []string, workspace string) *Manager {
	m := &Manager{
		ReadOnlyDirs: readOnly,
		DenyDirs:     deny,
		BashDenyCmds: bashDeny,
		Workspace:    workspace,
	}
	switch mode {
	case "plan":
		m.Mode = LevelPlan
	case "ask":
		m.Mode = LevelAsk
	case "auto":
		m.Mode = LevelAuto
	case "yolo":
		m.Mode = LevelYOLO
	default:
		m.Mode = LevelAsk
	}
	return m
}

func (m *Manager) CanRead(path string) bool {
	return !m.isDenied(path)
}

func (m *Manager) CanWrite(path string) bool {
	if m.Mode == LevelPlan {
		return false
	}
	if m.isDenied(path) {
		return false
	}
	if m.isReadOnly(path) {
		return false
	}
	return true
}

func (m *Manager) CanExecute(cmd string) bool {
	if m.Mode == LevelPlan {
		return false
	}
	lower := strings.ToLower(cmd)
	for _, denied := range m.BashDenyCmds {
		if strings.Contains(lower, strings.ToLower(denied)) {
			return false
		}
	}
	return true
}

func (m *Manager) NeedsConfirm(path string) bool {
	if m.Mode == LevelAuto || m.Mode == LevelYOLO {
		return false
	}
	if m.Mode == LevelAsk {
		return !m.isReadOnly(path)
	}
	return true
}

func (m *Manager) isDenied(path string) bool {
	abs, _ := filepath.Abs(path)
	for _, d := range m.DenyDirs {
		denyAbs, _ := filepath.Abs(d)
		if strings.HasPrefix(abs, denyAbs) {
			return true
		}
	}
	return false
}

func (m *Manager) isReadOnly(path string) bool {
	abs, _ := filepath.Abs(path)
	for _, d := range m.ReadOnlyDirs {
		roAbs, _ := filepath.Abs(d)
		if strings.HasPrefix(abs, roAbs) {
			return true
		}
	}
	return false
}
