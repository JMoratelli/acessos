//go:build !windows

package main

// Fora do Windows o app roda com stdout e stderr de verdade: quem abriu
// pelo terminal vê na hora, e quem abriu pelo menu tem o journal do
// systemd (ou o log do Flatpak). Não há por que inventar arquivo.
func iniciarLog(dir string) {}
