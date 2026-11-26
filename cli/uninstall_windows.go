//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	winreg "golang.org/x/sys/windows/registry"
)

// removeUserPathEntry drops dir from the user PATH that install.ps1 wrote,
// and tells running programs that the environment changed, as
// [Environment]::SetEnvironmentVariable does.
func removeUserPathEntry(dir string) error {
	k, err := winreg.OpenKey(winreg.CURRENT_USER, "Environment", winreg.QUERY_VALUE|winreg.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	value, valType, err := k.GetStringValue("Path")
	if errors.Is(err, winreg.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var kept []string
	for _, e := range strings.Split(value, ";") {
		if e == "" || strings.EqualFold(filepath.Clean(e), filepath.Clean(dir)) {
			continue
		}
		kept = append(kept, e)
	}
	joined := strings.Join(kept, ";")
	if joined == value {
		return nil
	}
	if valType == winreg.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", joined)
	} else {
		err = k.SetStringValue("Path", joined)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}

// deleteSelf removes the running binary. Windows cannot delete a running
// executable, so a detached cmd waits for this process to exit, then deletes
// the file and the folders install.ps1 created, if they are empty.
func deleteSelf(exe string) error {
	if strings.ContainsAny(exe, `"%`) {
		return fmt.Errorf("the path %s has a character cmd cannot quote; delete it by hand", exe)
	}
	bin := filepath.Dir(exe)
	cleanup := fmt.Sprintf(`rmdir "%s" 2>nul`, bin)
	// install.ps1 makes Programs\skael\bin. A custom SKAEL_INSTALL_DIR's
	// parent is not ours, even when it is empty.
	if parent := filepath.Dir(bin); strings.EqualFold(filepath.Base(parent), "skael") {
		cleanup += fmt.Sprintf(` & rmdir "%s" 2>nul`, parent)
	}
	script := fmt.Sprintf(`/d /c "for /l %%i in (1,1,20) do (del /f /q "%s" 2>nul & if not exist "%s" (%s & exit /b 0) & ping -n 2 127.0.0.1 >nul)"`,
		exe, exe, cleanup)
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := exec.Command(comspec)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `"` + comspec + `" ` + script,
		CreationFlags: 0x00000008 | syscall.CREATE_NEW_PROCESS_GROUP, // DETACHED_PROCESS
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
