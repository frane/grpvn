package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsApplicationIcon(t *testing.T) {
	module, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	if module == 0 {
		t.Fatal(err)
	}
	// The linked executable must contain the icon that Wails requests.
	icon, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("LoadIconW").Call(module, 3)
	if icon == 0 {
		t.Fatalf("grpvn application icon missing: %v", err)
	}
}
