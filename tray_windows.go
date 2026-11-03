//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32tray = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW = user32.NewProc("CreateWindowExW")
	procDefWindowProcW  = user32.NewProc("DefWindowProcW")
	procGetMessageW     = user32.NewProc("GetMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMsgW    = user32.NewProc("DispatchMessageW")
	procPostQuitMsg     = user32.NewProc("PostQuitMessage")
	procCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	procAppendMenuW     = user32.NewProc("AppendMenuW")
	procTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	procSetForeground   = user32.NewProc("SetForegroundWindow")
	procLoadIconW       = user32.NewProc("LoadIconW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmDestroy    = 0x0002
	wmCommand    = 0x0111
	wmRButtonUp  = 0x0205
	wmLButtonDbl = 0x0203
	wmUser       = 0x0400
	trayCallback = wmUser + 1

	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	mfString = 0x00000000
	mfSeparator = 0x00000800

	idOpen = 1001
	idExit = 1002

	idiApplication = 32512
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	x, y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

var trayHwnd uintptr
var trayIcon notifyIconData

func wndProc(hwnd, uMsg, wParam, lParam uintptr) uintptr {
	switch uMsg {
	case trayCallback:
		switch lParam {
		case wmRButtonUp:
			showTrayMenu(hwnd)
		case wmLButtonDbl:
			abrirBrowser("http://127.0.0.1:8422")
		}
		return 0
	case wmCommand:
		switch wParam & 0xFFFF {
		case idOpen:
			abrirBrowser("http://127.0.0.1:8422")
		case idExit:
			removeTrayIcon()
			syscall.NewLazyDLL("user32.dll").NewProc("PostQuitMessage").Call(0)
		}
		return 0
	case wmDestroy:
		removeTrayIcon()
		procPostQuitMsg.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uMsg, wParam, lParam)
	return r
}

func showTrayMenu(hwnd uintptr) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	procAppendMenuW.Call(hMenu, mfString, idOpen, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Abrir estado"))))
	procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
	procAppendMenuW.Call(hMenu, mfString, idExit, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Salir"))))
	// obtener cursor pos
	var pt point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(hwnd)
	procTrackPopupMenu.Call(hMenu, tpmRightButton|tpmBottomAlign, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	user32.NewProc("DestroyMenu").Call(hMenu)
}

func addTrayIcon(hwnd uintptr) {
	trayIcon.cbSize = uint32(unsafe.Sizeof(trayIcon))
	trayIcon.hWnd = hwnd
	trayIcon.uID = 1
	trayIcon.uFlags = nifMessage | nifIcon | nifTip
	trayIcon.uCallbackMessage = trayCallback
	hIcon, _, _ := procLoadIconW.Call(0, idiApplication)
	trayIcon.hIcon = hIcon
	copy(trayIcon.szTip[:], syscall.StringToUTF16("VPNPrint"))
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&trayIcon)))
}

func removeTrayIcon() {
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&trayIcon)))
}

func runTray() {
	className := syscall.StringToUTF16Ptr("VPNPrintTray")
	hInst, _, _ := kernel32tray.NewProc("GetModuleHandleW").Call(0)

	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInst,
		lpszClassName: className,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	trayHwnd, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("VPNPrint"))),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)

	addTrayIcon(trayHwnd)

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMsgW.Call(uintptr(unsafe.Pointer(&m)))
	}
}