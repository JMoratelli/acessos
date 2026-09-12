// Package cofre lê os segredos do conexoes.ini cifrados pelo app original
// (cofre.py). Mesmo formato, para os dois lerem o MESMO arquivo:
//
//	valor = "enc:v1:" + base64(nonce[12] || AES-GCM(chave, nonce, texto))
//	chave = PBKDF2-HMAC-SHA256(senha mestra, salt, 600000) -> 32 bytes
//
// O salt e o verificador vêm da seção [cofre] do próprio .ini. O
// verificador é o texto fixo "acessos-cofre-ok" selado com a chave: se ele
// abre, a senha mestra está certa (AES-GCM é autenticado, chave errada
// falha em vez de devolver lixo).
//
// Argon2id: o cofre.py escolhe argon2id quando a lib está disponível e
// grava kdf=argon2id no arquivo. Este arquivo aqui é pbkdf2; se um dia
// aparecer um cofre argon2id, Abrir devolve erro explicando, em vez de
// tentar abrir com o KDF errado (que só daria "senha incorreta" e mandaria
// o operador caçar o problema no lugar errado).
package cofre

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	marca       = "enc:v1:"
	iteracoes   = 600000 // PBKDF2_ITER do cofre.py
	tamChave    = 32
	tamNonce    = 12
	verificador = "acessos-cofre-ok"
)

var ErrSenhaIncorreta = errors.New("senha mestra incorreta")

type Cofre struct {
	chave []byte
}

// Parametros são os campos da seção [cofre] do .ini.
type Parametros struct {
	KDF         string // "pbkdf2" ou "argon2id"
	Salt        string // base64
	Verificador string // base64
}

// Abrir deriva a chave da senha mestra e confere contra o verificador.
func Abrir(p Parametros, senhaMestra string) (*Cofre, error) {
	kdf := p.KDF
	if kdf == "" {
		kdf = "pbkdf2"
	}
	if kdf != "pbkdf2" {
		return nil, fmt.Errorf("cofre criado com KDF %q, que este cliente ainda não implementa", kdf)
	}

	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return nil, fmt.Errorf("salt inválido na seção [cofre]: %w", err)
	}
	verif, err := base64.StdEncoding.DecodeString(p.Verificador)
	if err != nil {
		return nil, fmt.Errorf("verificador inválido na seção [cofre]: %w", err)
	}

	chave := pbkdf2.Key([]byte(senhaMestra), salt, iteracoes, tamChave, sha256.New)
	claro, err := abrirSelado(chave, verif)
	if err != nil || string(claro) != verificador {
		return nil, ErrSenhaIncorreta
	}
	return &Cofre{chave: chave}, nil
}

// Cifrado diz se o valor está no formato selado (e portanto precisa do
// cofre aberto para ser lido).
func Cifrado(valor string) bool {
	return strings.HasPrefix(valor, marca)
}

// Decifrar abre um valor "enc:v1:...". Texto claro passa direto — de
// propósito, igual ao cofre.py: permite arquivo em migração, metade
// convertido, sem quebrar nada.
func (c *Cofre) Decifrar(valor string) (string, error) {
	if valor == "" || !Cifrado(valor) {
		return valor, nil
	}
	if c == nil || c.chave == nil {
		return "", errors.New("cofre trancado")
	}
	bruto, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(valor, marca))
	if err != nil {
		return "", fmt.Errorf("valor cifrado ilegível: %w", err)
	}
	claro, err := abrirSelado(c.chave, bruto)
	if err != nil {
		return "", fmt.Errorf("valor não pôde ser decifrado (arquivo alterado ou senha mestra trocada): %w", err)
	}
	return string(claro), nil
}

func abrirSelado(chave, bruto []byte) ([]byte, error) {
	if len(bruto) <= tamNonce {
		return nil, errors.New("bloco cifrado curto demais")
	}
	bloco, err := aes.NewCipher(chave)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(bloco)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, bruto[:tamNonce], bruto[tamNonce:], nil)
}

// Cifrar sela um valor com a chave deste cofre, no MESMO formato que o
// cofre.py grava: "enc:v1:" + base64(nonce || AES-GCM). Nonce novo a cada
// chamada — reusar nonce com a mesma chave quebra o GCM.
func (c *Cofre) Cifrar(texto string) (string, error) {
	if texto == "" {
		return "", nil
	}
	bloco, err := aes.NewCipher(c.chave)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(bloco)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, tamNonce)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	selado := gcm.Seal(nil, nonce, []byte(texto), nil)
	return marca + base64.StdEncoding.EncodeToString(append(nonce, selado...)), nil
}

// Criar monta um cofre NOVO a partir de uma senha mestra: sorteia salt,
// deriva a chave e sela o verificador. Devolve o cofre e os parâmetros que
// vão para a seção [cofre] do .ini.
func Criar(senhaMestra string) (*Cofre, Parametros, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, Parametros{}, err
	}
	c := &Cofre{chave: pbkdf2.Key([]byte(senhaMestra), salt, iteracoes, tamChave, sha256.New)}
	v, err := c.Cifrar(verificador)
	if err != nil {
		return nil, Parametros{}, err
	}
	// o verificador é gravado sem o prefixo "enc:v1:" — é o formato do
	// cofre.py, que guarda ali só o base64.
	return c, Parametros{
		KDF:         "pbkdf2",
		Salt:        base64.StdEncoding.EncodeToString(salt),
		Verificador: strings.TrimPrefix(v, marca),
	}, nil
}
