//go:build windows

package conpty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
	"unsafe"

	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/muesli/cancelreader"
	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

const (
	defaultColumns = 120
	defaultRows    = 40
	resizeInterval = 250 * time.Millisecond
)

// This file owns the Windows pseudoconsole boundary for live Codex children.
// It is intentionally limited to terminal hosting concerns: child creation,
// raw console forwarding, resize propagation, and deterministic cleanup.

type waitResult struct {
	exitCode int
	err      error
}

// Session owns one pseudoconsole-hosted stock Codex child.
type Session struct {
	process   windows.Handle
	thread    windows.Handle
	pseudo    windows.Handle
	pseudoIn  *os.File
	pseudoOut *os.File

	stdinReader cancelreader.CancelReader
	rawState    *term.State
	onControlC  func()

	waitCh chan waitResult
	ioWG   sync.WaitGroup

	resizeStop chan struct{}
	closeOnce  sync.Once
}

// Start launches a stock Codex child inside a Windows pseudoconsole so CAW
// owns the terminal boundary rather than handing the real console directly to
// the child process.
func Start(spec codex.CommandSpec, onControlC func()) (*Session, error) {
	resolvedPath, err := exec.LookPath(spec.Path)
	if err != nil {
		return nil, err
	}

	ptyInputRead, ptyInputWrite, err := createPipePair()
	if err != nil {
		return nil, err
	}
	defer closeHandleOnErr(&err, ptyInputRead)
	defer closeHandleOnErr(&err, ptyInputWrite)

	ptyOutputRead, ptyOutputWrite, err := createPipePair()
	if err != nil {
		return nil, err
	}
	defer closeHandleOnErr(&err, ptyOutputRead)
	defer closeHandleOnErr(&err, ptyOutputWrite)

	size := currentConsoleSize()
	var pseudo windows.Handle
	if err = windows.CreatePseudoConsole(size, ptyInputRead, ptyOutputWrite, 0, &pseudo); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			windows.ClosePseudoConsole(pseudo)
		}
	}()

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrList.Delete()
	if err = attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(&pseudo), unsafe.Sizeof(pseudo)); err != nil {
		return nil, err
	}

	si := new(windows.StartupInfoEx)
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(*si))
	si.ProcThreadAttributeList = attrList.List()

	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{resolvedPath}, spec.Args...)))
	if err != nil {
		return nil, err
	}
	applicationName, err := windows.UTF16PtrFromString(resolvedPath)
	if err != nil {
		return nil, err
	}

	var envBlock *uint16
	if len(spec.Env) > 0 {
		encodedEnv, envErr := encodeEnvironmentBlock(spec.Env)
		if envErr != nil {
			return nil, envErr
		}
		envBlock = &encodedEnv[0]
	}

	var currentDir *uint16
	if spec.Dir != "" {
		currentDir, err = windows.UTF16PtrFromString(spec.Dir)
		if err != nil {
			return nil, err
		}
	}

	var processInfo windows.ProcessInformation
	if err = windows.CreateProcess(
		applicationName,
		commandLine,
		nil,
		nil,
		false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		envBlock,
		currentDir,
		&si.StartupInfo,
		&processInfo,
	); err != nil {
		return nil, err
	}

	_ = windows.CloseHandle(ptyInputRead)
	ptyInputRead = 0
	_ = windows.CloseHandle(ptyOutputWrite)
	ptyOutputWrite = 0

	pseudoIn := os.NewFile(uintptr(ptyInputWrite), "caw-conpty-in")
	pseudoOut := os.NewFile(uintptr(ptyOutputRead), "caw-conpty-out")

	session := &Session{
		process:    processInfo.Process,
		thread:     processInfo.Thread,
		pseudo:     pseudo,
		pseudoIn:   pseudoIn,
		pseudoOut:  pseudoOut,
		onControlC: onControlC,
		waitCh:     make(chan waitResult, 1),
		resizeStop: make(chan struct{}),
	}
	session.attachConsoleIO()
	session.startResizeLoop()
	go session.waitForExit()
	return session, nil
}

func (s *Session) PID() int {
	if s.process == 0 {
		return 0
	}
	pid, err := windows.GetProcessId(s.process)
	if err != nil {
		return 0
	}
	return int(pid)
}

func (s *Session) Wait() (int, error) {
	result := <-s.waitCh
	s.cleanup()
	s.ioWG.Wait()
	return result.exitCode, result.err
}

func (s *Session) Kill() error {
	if s.process == 0 {
		return nil
	}
	if err := windows.TerminateProcess(s.process, 1); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	return nil
}

func (s *Session) waitForExit() {
	_, err := windows.WaitForSingleObject(s.process, windows.INFINITE)
	if err != nil {
		s.waitCh <- waitResult{err: err}
		return
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(s.process, &exitCode); err != nil {
		s.waitCh <- waitResult{err: err}
		return
	}
	s.waitCh <- waitResult{exitCode: int(exitCode)}
}

func (s *Session) attachConsoleIO() {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if state, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
			s.rawState = state
		}
	}

	stdinReader, err := cancelreader.NewReader(os.Stdin)
	if err == nil {
		s.stdinReader = stdinReader
		s.ioWG.Add(1)
		go func() {
			defer s.ioWG.Done()
			s.forwardConsoleInput(stdinReader)
		}()
	}

	s.ioWG.Add(1)
	go func() {
		defer s.ioWG.Done()
		_, _ = io.Copy(os.Stdout, s.pseudoOut)
	}()
}

func (s *Session) startResizeLoop() {
	go func() {
		ticker := time.NewTicker(resizeInterval)
		defer ticker.Stop()
		last := currentConsoleSize()
		for {
			select {
			case <-s.resizeStop:
				return
			case <-ticker.C:
				size := currentConsoleSize()
				if size == last {
					continue
				}
				if err := windows.ResizePseudoConsole(s.pseudo, size); err == nil {
					last = size
				}
			}
		}
	}()
}

func (s *Session) cleanup() {
	s.closeOnce.Do(func() {
		close(s.resizeStop)
		if s.stdinReader != nil {
			s.stdinReader.Cancel()
			_ = s.stdinReader.Close()
		}
		if s.pseudoIn != nil {
			_ = s.pseudoIn.Close()
		}
		if s.pseudoOut != nil {
			_ = s.pseudoOut.Close()
		}
		if s.rawState != nil {
			_ = term.Restore(int(os.Stdin.Fd()), s.rawState)
		}
		if s.thread != 0 {
			_ = windows.CloseHandle(s.thread)
			s.thread = 0
		}
		if s.process != 0 {
			_ = windows.CloseHandle(s.process)
			s.process = 0
		}
		if s.pseudo != 0 {
			windows.ClosePseudoConsole(s.pseudo)
			s.pseudo = 0
		}
	})
}

func createPipePair() (windows.Handle, windows.Handle, error) {
	readHandle := windows.Handle(0)
	writeHandle := windows.Handle(0)
	sa := windows.SecurityAttributes{
		Length:        uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		InheritHandle: 1,
	}
	if err := windows.CreatePipe(&readHandle, &writeHandle, &sa, 0); err != nil {
		return 0, 0, err
	}
	return readHandle, writeHandle, nil
}

func closeHandleOnErr(errp *error, handle windows.Handle) {
	if errp != nil && *errp != nil && handle != 0 {
		_ = windows.CloseHandle(handle)
	}
}

func (s *Session) forwardConsoleInput(reader io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if out := stripControlC(buf[:n], s.onControlC); len(out) > 0 {
				if _, writeErr := s.pseudoIn.Write(out); writeErr != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func stripControlC(data []byte, onControlC func()) []byte {
	if len(data) == 0 {
		return data
	}
	filtered := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x03 {
			// Ctrl+C is a host policy decision while Codex is live. CAW consumes
			// the byte here so the host runtime can return to Home instead of
			// forwarding a raw interrupt into the child.
			if onControlC != nil {
				onControlC()
			}
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered
}

func currentConsoleSize() windows.Coord {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return windows.Coord{X: defaultColumns, Y: defaultRows}
	}
	return windows.Coord{X: int16(width), Y: int16(height)}
}

func encodeEnvironmentBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return windows.UTF16FromString("\x00")
	}
	length := 1
	for _, entry := range env {
		if containsNUL(entry) {
			return nil, windows.ERROR_INVALID_PARAMETER
		}
		length += len(entry) + 1
	}
	block := make([]uint16, 0, length)
	for _, entry := range env {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded[:len(encoded)-1]...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

func containsNUL(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 0 {
			return true
		}
	}
	return false
}
