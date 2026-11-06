package webfs

import "embed"

// FS holds ops + wallet static assets for bare installs without a web/ directory on disk.
//
//go:embed all:ops all:wallet
var FS embed.FS
