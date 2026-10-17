//go:build windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// profileName is the AppContainer profile iCode provisions for confined
// commands. It is created once per machine and reused afterwards.
const profileName = "iCode.Sandbox"

const (
	procThreadAttrSecurityCaps = 0x00020009
	extendedStartupInfoPresent = 0x00080000
	startfUseStdHandles        = 0x00000100
	createNoWindow             = 0x08000000
	// createUnicodeEnvironment is mandatory whenever lpEnvironment points at a
	// UTF-16 block: without it the block is parsed as ANSI, every entry decodes
	// to an empty name, and CreateProcess fails with ERROR_ENVVAR_NOT_FOUND.
	createUnicodeEnvironment = 0x00000400
	infinite                 = 0xFFFFFFFF
	// SEE_MASK / capability attribute: capability SIDs are always enabled.
	seGroupEnabled = 0x00000004
)

var (
	userenv = windows.NewLazySystemDLL("userenv.dll")
	advapi  = windows.NewLazySystemDLL("advapi32.dll")
	kernel  = windows.NewLazySystemDLL("kernel32.dll")

	procCreateACProfile = userenv.NewProc("CreateAppContainerProfile")
	procDeriveACSid     = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procDeleteACProfile = userenv.NewProc("DeleteAppContainerProfile")

	procSetEntriesInACL = advapi.NewProc("SetEntriesInAclW")

	procInitAttrList   = kernel.NewProc("InitializeProcThreadAttributeList")
	procUpdateAttr     = kernel.NewProc("UpdateProcThreadAttribute")
	procDeleteAttrList = kernel.NewProc("DeleteProcThreadAttributeList")
	procCreateProcessW = kernel.NewProc("CreateProcessW")
	procCreatePipe     = kernel.NewProc("CreatePipe")
	procReadFile       = kernel.NewProc("ReadFile")
	procWaitForObj     = kernel.NewProc("WaitForSingleObject")
	procGetExitCode    = kernel.NewProc("GetExitCodeProcess")
	procTerminateProc  = kernel.NewProc("TerminateProcess")
	procCloseHandle    = kernel.NewProc("CloseHandle")
)

type sidAndAttributes struct {
	Sid        unsafe.Pointer
	Attributes uint32
}

// securityCapabilities must stay 24 bytes on x64: two pointers, a DWORD count
// and a DWORD reserved slot. Widening Reserved to uint64 pads the struct to 32
// and makes UpdateProcThreadAttribute fail with ERROR_INVALID_PARAMETER, whose
// message ("The parameter is incorrect.") points nowhere useful.
type securityCapabilities struct {
	AppContainerSid unsafe.Pointer
	Capabilities    *sidAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

type startupInfo struct {
	Cb             uint32
	Reserved       *uint16
	Desktop        *uint16
	Title          *uint16
	X, Y           uint32
	XSize, YSize   uint32
	XCount, YCount uint32
	FillAttribute  uint32
	Flags          uint32
	ShowWindow     uint16
	CbReserved2    uint16
	Reserved2      *byte
	StdInput       windows.Handle
	StdOutput      windows.Handle
	StdError       windows.Handle
}

type startupInfoEx struct {
	StartupInfo   startupInfo
	AttributeList unsafe.Pointer
}

type processInformation struct {
	Process   windows.Handle
	Thread    windows.Handle
	ProcessID uint32
	ThreadID  uint32
}

// acRunner is the AppContainer backend.
type acRunner struct {
	sid            *windows.SID
	sidString      string
	grantWorkspace bool
	network        NetworkPolicy
	extraWritable  []string
	tmpDir         string
	caps           []sidAndAttributes
	capSIDs        []*windows.SID

	mu      sync.Mutex
	granted map[string]bool
	closed  bool
}

func newPlatformRunner(cfg Config) (Runner, []string, error) {
	sid, created, err := ensureProfile(profileName)
	if err != nil {
		return nil, nil, err
	}
	r := &acRunner{
		sid:            sid,
		sidString:      sid.String(),
		grantWorkspace: cfg.GrantWorkspace,
		network:        cfg.Network,
		extraWritable:  append([]string(nil), cfg.WritablePaths...),
		granted:        make(map[string]bool),
	}

	// A dedicated temp directory rather than the shared %TEMP%: granting the
	// latter would let a confined command read other processes' scratch files.
	tmp := filepath.Join(os.TempDir(), "icode-sandbox")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		tmp = ""
	}
	r.tmpDir = tmp

	var notices []string
	if created {
		notices = append(notices, "created AppContainer profile "+profileName)
	}
	// The NUL device's DACL ships without an AppContainer entry, so a confined
	// process cannot open it. The Go toolchain opens os.DevNull while computing
	// build IDs, which makes `go build` fail with "open NUL: Access is denied".
	// Adding one ACE to that device fixes it; the device holds no data, so this
	// widens nothing meaningful. If the DACL is not writable (non-admin user),
	// confinement still works — building inside it just does not.
	if granted, gerr := ensureNulDeviceAccess(sid); gerr != nil {
		notices = append(notices, "container cannot open the NUL device; builds inside the sandbox may fail: "+gerr.Error())
	} else if granted {
		notices = append(notices, "granted container access to the NUL device so build toolchains work")
	}
	if cfg.Network == NetworkAllow {
		// S-1-15-3-1 is the internetClient capability SID. Granting it is the
		// only way an AppContainer reaches the network at all.
		var capSID *windows.SID
		if err := windows.ConvertStringSidToSid(
			windows.StringToUTF16Ptr("S-1-15-3-1"), &capSID); err == nil {
			r.capSIDs = append(r.capSIDs, capSID)
			r.caps = append(r.caps, sidAndAttributes{
				Sid: unsafe.Pointer(capSID), Attributes: seGroupEnabled,
			})
		}
	}
	return r, notices, nil
}

// ensureProfile returns the AppContainer SID, creating the profile when it does
// not exist yet. created reports whether this call provisioned it.
func ensureProfile(name string) (sid *windows.SID, created bool, err error) {
	n, _ := windows.UTF16PtrFromString(name)
	desc, _ := windows.UTF16PtrFromString("iCode confined command execution")
	var raw unsafe.Pointer
	hr, _, _ := procCreateACProfile.Call(
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(desc)), 0, 0,
		uintptr(unsafe.Pointer(&raw)))
	switch uint32(hr) {
	case 0:
		return (*windows.SID)(raw), true, nil
	case 0x800700B7: // ERROR_ALREADY_EXISTS
		r, _, _ := procDeriveACSid.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&raw)))
		if int32(uint32(r)) < 0 {
			return nil, false, fmt.Errorf("sandbox: derive AppContainer SID for %q failed (hr=0x%x)", name, uint32(r))
		}
		return (*windows.SID)(raw), false, nil
	default:
		return nil, false, fmt.Errorf("sandbox: create AppContainer profile %q failed (hr=0x%x)", name, uint32(hr))
	}
}

func (r *acRunner) Name() string    { return "appcontainer" }
func (r *acRunner) Available() bool { return r != nil && r.sid != nil }

func (r *acRunner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, s := range r.capSIDs {
		windows.LocalFree(windows.Handle(unsafe.Pointer(s)))
	}
	r.capSIDs = nil
	// The profile itself is intentionally left in place: deleting it would
	// invalidate the ACEs already written into project directories.
	return nil
}

// ensureAccess makes dir usable by the confined process. It is idempotent
// within a process lifetime and verified against the live DACL, so a
// re-created profile or an externally revoked ACE is repaired on next use.
func (r *acRunner) ensureAccess(dir string) error {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("sandbox: resolve %q: %w", dir, err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return fmt.Errorf("sandbox: %q is not an accessible directory", abs)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.granted[abs] {
		return nil
	}
	if !r.grantWorkspace {
		return fmt.Errorf("sandbox: %q is not granted; enable sandbox.grant_workspace or add it to sandbox.writable_paths", abs)
	}

	has, err := hasACE(abs, r.sidString)
	if err != nil {
		return err
	}
	if !has {
		if err := grantACE(abs, r.sid); err != nil {
			return err
		}
		// The ACE is now in the DACL. Recording it is bookkeeping: if it
		// fails, the grant still exists and the user cannot see it, so the
		// failure is surfaced through Status notices rather than swallowed.
		if err := RecordGrant(abs); err != nil {
			noteRecordFailure(abs, err)
		}
	}
	r.granted[abs] = true
	return nil
}

// forgetPath drops a cached grant so the next command re-reads the DACL. It is
// called after a revocation, where the cached "already granted" answer is now
// wrong and would make the next command fail with a bare access-denied error.
func (r *acRunner) forgetPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.granted {
		if samePath(k, path) {
			delete(r.granted, k)
		}
	}
}

// hasACE inspects the object's DACL for any entry carrying our AppContainer
// SID. The SDDL form is used because the ACE list is otherwise only reachable
// through undocumented layout assumptions.
func hasACE(path, sid string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("sandbox: read DACL of %q: %w", path, err)
	}
	return strings.Contains(sd.String(), sid), nil
}

func grantACE(path string, sid *windows.SID) error {
	return modifyDACL(path, sid, windows.GRANT_ACCESS)
}

func revokeACE(path string, sid *windows.SID) error {
	return modifyDACL(path, sid, windows.REVOKE_ACCESS)
}

// modifyDACL adds (or removes) one inheritable ACE for sid. Existing entries
// are preserved: SetEntriesInAcl merges into the DACL it is handed rather than
// replacing it, which is why the current DACL is read first.
func modifyDACL(path string, sid *windows.SID, mode windows.ACCESS_MODE) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("sandbox: read DACL of %q: %w", path, err)
	}
	oldACL, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("sandbox: extract DACL of %q: %w", path, err)
	}

	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        mode,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}

	var newACL *windows.ACL
	ret, _, _ := procSetEntriesInACL.Call(
		uintptr(len(entries)),
		uintptr(unsafe.Pointer(&entries[0])),
		uintptr(unsafe.Pointer(oldACL)),
		uintptr(unsafe.Pointer(&newACL)))
	if ret != 0 {
		return fmt.Errorf("sandbox: build DACL for %q: %w", path, windows.Errno(ret))
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(newACL)))

	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, newACL, nil); err != nil {
		return fmt.Errorf("sandbox: apply DACL to %q: %w", path, err)
	}
	return nil
}

// nulDevice is the DOS name that resolves to \Device\Null. Its ACL is readable
// and, for an administrator, writable through the ordinary file-object APIs.
const nulDevice = "NUL"

// ensureNulDeviceAccess adds the container SID to the NUL device DACL when it
// is missing. granted reports whether this call changed anything.
func ensureNulDeviceAccess(sid *windows.SID) (granted bool, err error) {
	has, err := hasACE(nulDevice, sid.String())
	if err != nil {
		return false, err
	}
	if has {
		return false, nil
	}
	if err := grantACE(nulDevice, sid); err != nil {
		return false, err
	}
	return true, nil
}

// revokePlatform removes the container ACE from dir and reports whether one
// was there. It answers the question rather than assuming: a path that was
// never granted should be described as clean, not as "revoked".
func revokePlatform(dir string) (bool, error) {
	sid, _, err := ensureProfile(profileName)
	if err != nil {
		return false, err
	}
	has, err := hasACE(dir, sid.String())
	if err != nil {
		return false, err
	}
	if !has {
		return false, nil
	}
	if err := revokeACE(dir, sid); err != nil {
		return false, err
	}
	return true, nil
}

// Start launches argv inside the AppContainer.
func (r *acRunner) Start(ctx context.Context, argv []string, spec Spec) (Handle, error) {
	if len(argv) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	if spec.Dir != "" {
		if err := r.ensureAccess(spec.Dir); err != nil {
			return nil, err
		}
	}
	for _, d := range append(append([]string(nil), r.extraWritable...), spec.Writable...) {
		// Extra paths are best-effort: a missing cache directory must not turn
		// every command into a failure.
		_ = r.ensureAccess(d)
	}
	if r.tmpDir != "" {
		// Not best-effort: without a usable temp directory most compilers and
		// package managers fail in confusing ways.
		if err := r.ensureAccess(r.tmpDir); err != nil {
			return nil, err
		}
	}

	network := spec.Network
	if network == "" {
		network = r.network
	}
	var caps *securityCapabilities
	caps = &securityCapabilities{AppContainerSid: unsafe.Pointer(r.sid)}
	if network == NetworkAllow && len(r.caps) > 0 {
		caps.Capabilities = &r.caps[0]
		caps.CapabilityCount = uint32(len(r.caps))
	}

	return startConfined(ctx, argv, spec, caps, buildSandboxEnv(r.tmpDir))
}

// buildSandboxEnv inherits the parent environment but repoints TMP/TEMP at the
// dedicated sandbox directory, so tools writing scratch files never touch (or
// need access to) the shared system temp.
func buildSandboxEnv(tmp string) []string {
	env := os.Environ()
	if tmp == "" {
		return env
	}
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i > 0 {
			key = kv[:i]
		}
		if up := strings.ToUpper(key); up == "TMP" || up == "TEMP" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TMP="+tmp, "TEMP="+tmp)
}

// envBlock encodes a UTF-16 environment block ("K=V\0K=V\0\0").
func envBlock(env []string) *uint16 {
	buf := make([]uint16, 0, len(env)*8)
	for _, kv := range env {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			continue // values containing NUL cannot be expressed; skip
		}
		buf = append(buf, u...) // UTF16FromString already terminates each entry
	}
	buf = append(buf, 0)
	return &buf[0]
}

// acHandle tracks one confined process.
type acHandle struct {
	proc   windows.Handle
	thread windows.Handle
	pipe   windows.Handle
	onOut  func([]byte)

	done    chan struct{}
	mu      sync.Mutex
	result  Result
	readErr error
	killed  bool
}

func startConfined(ctx context.Context, argv []string, spec Spec, caps *securityCapabilities, env []string) (*acHandle, error) {
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.InheritHandle = 1
	var rd, wr windows.Handle
	if r, _, e := procCreatePipe.Call(uintptr(unsafe.Pointer(&rd)),
		uintptr(unsafe.Pointer(&wr)), uintptr(unsafe.Pointer(&sa)), 0); r == 0 {
		return nil, fmt.Errorf("sandbox: create pipe: %w", e)
	}

	var size uintptr
	procInitAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	list := make([]byte, size)
	if r, _, e := procInitAttrList.Call(uintptr(unsafe.Pointer(&list[0])), 1, 0,
		uintptr(unsafe.Pointer(&size))); r == 0 {
		procCloseHandle.Call(uintptr(wr))
		procCloseHandle.Call(uintptr(rd))
		return nil, fmt.Errorf("sandbox: init attribute list: %w", e)
	}
	if r, _, e := procUpdateAttr.Call(uintptr(unsafe.Pointer(&list[0])), 0,
		procThreadAttrSecurityCaps, uintptr(unsafe.Pointer(caps)),
		unsafe.Sizeof(*caps), 0, 0); r == 0 {
		procDeleteAttrList.Call(uintptr(unsafe.Pointer(&list[0])))
		procCloseHandle.Call(uintptr(wr))
		procCloseHandle.Call(uintptr(rd))
		return nil, fmt.Errorf("sandbox: attach security capabilities: %w", e)
	}
	defer procDeleteAttrList.Call(uintptr(unsafe.Pointer(&list[0])))

	var si startupInfoEx
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))
	si.StartupInfo.Flags = startfUseStdHandles
	si.StartupInfo.StdOutput = wr
	si.StartupInfo.StdError = wr
	si.AttributeList = unsafe.Pointer(&list[0])

	cmdline := windows.ComposeCommandLine(argv)
	cl, err := windows.UTF16FromString(cmdline)
	if err != nil {
		procCloseHandle.Call(uintptr(wr))
		procCloseHandle.Call(uintptr(rd))
		return nil, fmt.Errorf("sandbox: encode command line: %w", err)
	}
	var wd *uint16
	if spec.Dir != "" {
		abs, aerr := filepath.Abs(spec.Dir)
		if aerr == nil {
			wd, _ = windows.UTF16PtrFromString(abs)
		}
	}

	// The environment block must outlive the call: converting a pointer to
	// uintptr hides it from the collector, so the buffer is held in a named
	// variable and kept alive explicitly.
	buf := envBlock(env)

	var pi processInformation
	r, _, e := procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(&cl[0])),
		0, 0, 1,
		extendedStartupInfoPresent|createNoWindow|createUnicodeEnvironment,
		uintptr(unsafe.Pointer(buf)),
		uintptr(unsafe.Pointer(wd)),
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)))
	runtime.KeepAlive(buf)
	runtime.KeepAlive(cl)
	if r == 0 {
		procCloseHandle.Call(uintptr(wr))
		procCloseHandle.Call(uintptr(rd))
		if errors.Is(e, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("sandbox: Windows refused to launch the command inside the container "+
				"(the working directory or the shell executable is not reachable): %w", e)
		}
		return nil, fmt.Errorf("sandbox: CreateProcess failed: %w", e)
	}

	// The parent must drop its write end, otherwise the reader never sees EOF.
	procCloseHandle.Call(uintptr(wr))

	h := &acHandle{proc: pi.Process, thread: pi.Thread, pipe: rd, onOut: spec.OnOutput, done: make(chan struct{})}
	go h.pump()

	// Cancellation is the backend's job, not the caller's: a caller that only
	// calls Wait would otherwise hang past its own deadline.
	go func() {
		select {
		case <-ctx.Done():
			h.Kill()
		case <-h.done:
		}
	}()
	return h, nil
}

// pump drains stdout+stderr until EOF, then reaps the process.
func (h *acHandle) pump() {
	defer close(h.done)
	var out []byte
	buf := make([]byte, 8192)
	for {
		var n uint32
		ok, _, _ := procReadFile.Call(uintptr(h.pipe),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
			uintptr(unsafe.Pointer(&n)), 0)
		if ok == 0 || n == 0 {
			break
		}
		chunk := buf[:n]
		out = append(out, chunk...)
		if h.onOut != nil {
			h.onOut(chunk)
		}
	}
	procCloseHandle.Call(uintptr(h.pipe))
	procWaitForObj.Call(uintptr(h.proc), infinite)
	var code uint32
	procGetExitCode.Call(uintptr(h.proc), uintptr(unsafe.Pointer(&code)))
	procCloseHandle.Call(uintptr(h.proc))
	procCloseHandle.Call(uintptr(h.thread))

	h.mu.Lock()
	h.result = Result{Output: out, ExitCode: int(code)}
	h.mu.Unlock()
}

func (h *acHandle) Wait() (Result, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result, h.readErr
}

func (h *acHandle) Kill() error {
	h.mu.Lock()
	if h.killed {
		h.mu.Unlock()
		return nil
	}
	h.killed = true
	h.mu.Unlock()
	procTerminateProc.Call(uintptr(h.proc), 1)
	return nil
}
