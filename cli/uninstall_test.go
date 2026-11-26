package cli

import "testing"

// Each install method owns the binary differently, and a wrong guess either
// deletes a package manager's file or leaves an install script's file behind.
func TestDetectInstall(t *testing.T) {
	cases := []struct {
		exe, goos, localAppData, installDir string
		want                                installKind
	}{
		{`C:\Users\a\scoop\apps\skael\0.22.0\skael.exe`, "windows", `C:\Users\a\AppData\Local`, "", installScoop},
		{`C:\Users\a\AppData\Local\Programs\skael\bin\skael.exe`, "windows", `C:\Users\a\AppData\Local`, "", installPS1},
		{`D:\tools\skael.exe`, "windows", `C:\Users\a\AppData\Local`, `D:\tools`, installPS1},
		{`C:\Users\a\go\bin\skael.exe`, "windows", `C:\Users\a\AppData\Local`, "", installOther},
		{"/opt/homebrew/Caskroom/skael/0.23.0/skael", "darwin", "", "", installHomebrew},
		{"/usr/local/bin/skael", "linux", "", "", installSh},
		{"/home/a/bin/skael", "linux", "", "/home/a/bin", installSh},
		{"/home/a/go/bin/skael", "linux", "", "", installOther},
	}
	for _, c := range cases {
		if got := detectInstall(c.exe, c.goos, c.localAppData, c.installDir); got != c.want {
			t.Errorf("detectInstall(%q, %s) = %s, want %s", c.exe, c.goos, got, c.want)
		}
	}
}
