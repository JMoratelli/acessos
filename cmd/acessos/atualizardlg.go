package main

import (
	"fmt"
	"sync"

	"acessos-go/internal/atualizador"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// dlgAtualizar avisa que há versão nova e instala sob confirmação.
//
// A confirmação existe porque atualizar FECHA as sessões abertas: o app
// sai e volta. Quem está no meio de um atendimento decide a hora.
type dlgAtualizar struct {
	w   *app.Window
	rel *atualizador.Release

	mu        sync.Mutex
	baixando  bool
	fracao    float64
	erro      string
	btnAgora  widget.Clickable
	btnDepois widget.Clickable
}

func oferecerAtualizacao(w *app.Window, r *atualizador.Release) {
	abrirDialogo(&dlgAtualizar{w: w, rel: r})
}

func (d *dlgAtualizar) Titulo() string   { return "Atualização disponível" }
func (d *dlgAtualizar) Largura() unit.Dp { return 460 }

func (d *dlgAtualizar) Corpo(gtx layout.Context, th *material.Theme) layout.Dimensions {
	d.mu.Lock()
	baixando, fracao, erro := d.baixando, d.fracao, d.erro
	d.mu.Unlock()

	if d.btnDepois.Clicked(gtx) {
		fecharDialogo()
	}
	if !baixando && d.btnAgora.Clicked(gtx) {
		d.instalar()
	}

	filhos := []layout.FlexChild{
		layout.Rigid(rotulo(th, fonteSans, spCorpo,
			fmt.Sprintf("A versão %s está disponível (você tem a %s).",
				d.rel.Tag, versaoInstalada()), tema.Texto)),
		espaco(6),
		layout.Rigid(rotulo(th, fonteMono, spCardMeta,
			"O app será fechado e reaberto — as sessões abertas se encerram.", tema.Sec)),
	}
	if baixando {
		filhos = append(filhos, espaco(10),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				b := material.ProgressBar(th, float32(fracao))
				b.Color = tema.Azul
				b.TrackColor = tema.Vidro2
				return b.Layout(gtx)
			}))
	}
	if erro != "" {
		filhos = append(filhos, espaco(8),
			layout.Rigid(rotulo(th, fonteMono, spSecundario, erro, tema.ErroFg)))
	}
	filhos = append(filhos, espaco(14), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botaoNeutro(gtx, th, &d.btnDepois, "Depois")
			}),
			layout.Rigid(layout.Spacer{Width: 8}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return botao(gtx, th, &d.btnAgora, "Atualizar agora", pesoPrimario, baixando)
			}),
		)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, filhos...)
}

func (d *dlgAtualizar) instalar() {
	d.mu.Lock()
	d.baixando, d.erro = true, ""
	d.mu.Unlock()

	go func() {
		err := atualizador.Instalar(d.rel, func(f float64) {
			d.mu.Lock()
			d.fracao = f
			d.mu.Unlock()
			d.w.Invalidate()
		})
		if err != nil {
			d.mu.Lock()
			d.baixando, d.erro = false, err.Error()
			d.mu.Unlock()
			d.w.Invalidate()
			return
		}
		// A instância nova já está subindo; esta sai.
		d.w.Perform(system.ActionClose)
	}()
}

// checarAtualizacao roda no start, em segundo plano. Silenciosa: sem
// rede, sem release nova ou fora do Flatpak, ninguém fica sabendo — aviso
// de atualização que aparece para dizer "está tudo certo" vira ruído.
func checarAtualizacao(w *app.Window) {
	if !atualizador.EmFlatpak() {
		return
	}
	atual := versaoInstalada()
	if atual == "" {
		return
	}
	go func() {
		rel, err := atualizador.Checar(atual)
		if err != nil || rel == nil {
			return
		}
		reg("atualização disponível: %s", rel.Tag)
		oferecerAtualizacao(w, rel)
		w.Invalidate()
	}()
}
