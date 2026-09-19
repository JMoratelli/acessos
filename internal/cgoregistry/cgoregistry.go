// Package cgoregistry guarda o mapa handle→*T que os wrappers cgo de
// internal/rdp e internal/vnc precisam: o C só aceita um void* opaco como
// contexto do callback, e um handle inteiro nesse void* é mais seguro que
// um ponteiro Go de verdade (o coletor de lixo não move nem invalida um
// inteiro). Extraído porque os dois pacotes tinham o mesmo mapa, mutex e
// contador duplicados byte a byte.
package cgoregistry

import "sync"

// Registry associa handles crescentes a *T. Uso típico: Registrar no
// construtor da sessão, De dentro dos callbacks exportados via cgo, e
// Remover no Close — para o handle não sobreviver à sessão que ele
// representa.
type Registry[T any] struct {
	mu    sync.Mutex
	prox  uintptr
	itens map[uintptr]*T
}

func New[T any]() *Registry[T] {
	return &Registry[T]{itens: map[uintptr]*T{}}
}

// Registrar cria um handle novo para v e o guarda.
func (r *Registry[T]) Registrar(v *T) uintptr {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prox++
	h := r.prox
	r.itens[h] = v
	return h
}

// De devolve o *T do handle, ou nil se ele já foi removido (sessão
// fechada) ou nunca existiu.
func (r *Registry[T]) De(h uintptr) *T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.itens[h]
}

// Remover apaga o handle. Chamar de novo, ou com um handle inexistente,
// não faz nada.
func (r *Registry[T]) Remover(h uintptr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.itens, h)
}
