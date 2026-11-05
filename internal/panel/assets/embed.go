package assets

import _ "embed"

// IconPNG is a tray/window icon (robot mark).
//
//go:embed icon.png
var IconPNG []byte

// IconICO is a multi-size Windows icon for NotifyIcon.
//
//go:embed icon.ico
var IconICO []byte
