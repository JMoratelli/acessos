module acessos-go

// A versão aqui tem de caber no toolchain do SDK do Flatpak (extensão
// golang do runtime 25.08 = Go 1.27.1), porque o build roda OFFLINE com
// GOTOOLCHAIN=local: pedir versão maior faria o Go tentar baixar o
// toolchain e falhar sem rede.
go 1.26.0

require (
	gio.tools/icons v0.0.0-20240708021058-44790e75e701
	gioui.org v0.10.2
	github.com/hinshun/vt10x v0.0.0-20220301184237-5011da428d02
	github.com/pkg/sftp v1.13.11
	golang.org/x/crypto v0.57.0
	golang.org/x/net v0.59.0
	golang.org/x/term v0.46.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	github.com/go-text/typesetting v0.3.4 // indirect
	github.com/kr/fs v0.1.0 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/image v0.26.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace gioui.org => ./third_party/gio
