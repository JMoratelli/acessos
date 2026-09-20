//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ajustarOpenSSL aponta o OPENSSL_MODULES para a pasta ossl-modules que o
// instalador deixa ao lado do .exe. Chamada no começo do main — antes do
// modoWorker(), para valer também no processo-filho que hospeda a sessão.
//
// ----------------------------------------------------------------------
// O QUE ESTAVA ACONTECENDO
// ----------------------------------------------------------------------
//
// A libcrypto que o instalador embarca vem do MSYS2 e traz COMPILADO o
// caminho onde procurar os providers do OpenSSL 3:
//
//	$ openssl version -m
//	MODULESDIR: "/ucrt64/lib/ossl-modules"
//
// Esse caminho não existe em máquina nenhuma sem o MSYS2 instalado. E o
// provider não entra na conta do scripts/dlls-windows.py, que percorre a
// tabela de importação do PE: provider é carregado em tempo de execução,
// pelo nome. Ou seja, o legacy.dll nunca foi junto e, mesmo que fosse,
// seria procurado no lugar errado.
//
// Sem o provider legacy não há MD4 nem RC4, e é isto que o FreeRDP
// escreve no log a cada conexão:
//
//	OpenSSL LEGACY provider failed to load, no md4 support available!
//	* md4: NTLM support not available
//	* rc4: ... NTLM and autoreconnect cookies will not work
//	* rc4: RDP licensing and RDP security will not work
//
// Traduzindo para o que o operador vê: contra servidor que não fecha por
// Kerberos — o caso normal quando se conecta por IP — o login é RECUSADO
// (ERRCONNECT_LOGON_FAILURE), e uma queda passageira não se resolve
// sozinha, porque o cookie de autoreconnect depende do RC4. As duas
// assinaturas estavam no freerdp.log de produção desta máquina.
//
// A pasta só existe na instalação (ver scripts/build-windows.sh); num
// build de desenvolvimento, onde o MSYS2 está instalado de verdade, ela
// não existe e o caminho compilado funciona — por isso a ausência é
// silêncio, não erro.
func ajustarOpenSSL() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Join(filepath.Dir(exe), "ossl-modules")
	if _, err := os.Stat(dir); err != nil {
		return
	}
	// Sobrepõe mesmo se já vier do ambiente: a pasta ao lado do .exe é a
	// que casa com a libcrypto que ESTE app carrega, e uma variável
	// herdada de outro programa apontaria para providers de outra versão.
	if err := os.Setenv("OPENSSL_MODULES", dir); err != nil {
		fmt.Fprintf(os.Stderr, "OPENSSL_MODULES: %v\n", err)
	}
}
