//go:build !windows

package main

// Fora do Windows não há o que ajustar: a libcrypto é a do sistema (ou a
// do runtime do Flatpak), com o MODULESDIR dela batendo com o lugar onde
// os providers de fato estão. O irmão com o porquê é ossl_windows.go.
func ajustarOpenSSL() {}
