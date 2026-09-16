// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"errors"
	"fmt"
	"unsafe"

	"gioui.org/gpu"
	"gioui.org/internal/d3d11"
)

type d3d11Context struct {
	win *window
	dev *d3d11.Device
	ctx *d3d11.DeviceContext

	swchain       *d3d11.IDXGISwapChain
	renderTarget  *d3d11.RenderTargetView
	width, height int
}

const debugDirectX = false

func init() {
	drivers = append(drivers, gpuAPI{
		priority: 1,
		initializer: func(w *window) (context, error) {
			hwnd, _, _ := w.HWND()
			var flags uint32
			if debugDirectX {
				flags |= d3d11.CREATE_DEVICE_DEBUG
			}
			dev, ctx, _, err := d3d11.CreateDevice(
				d3d11.DRIVER_TYPE_HARDWARE,
				flags,
			)
			if err != nil {
				return nil, fmt.Errorf("NewContext: %v", err)
			}
			swchain, err := d3d11.CreateSwapChain(dev, hwnd)
			if err != nil {
				d3d11.IUnknownRelease(unsafe.Pointer(ctx), ctx.Vtbl.Release)
				d3d11.IUnknownRelease(unsafe.Pointer(dev), dev.Vtbl.Release)
				return nil, err
			}
			return &d3d11Context{win: w, dev: dev, ctx: ctx, swchain: swchain}, nil
		},
	})
}

func (c *d3d11Context) API() gpu.API {
	return gpu.Direct3D11{Device: unsafe.Pointer(c.dev)}
}

func (c *d3d11Context) RenderTarget() (gpu.RenderTarget, error) {
	return gpu.Direct3D11RenderTarget{
		RenderTarget: unsafe.Pointer(c.renderTarget),
	}, nil
}

func (c *d3d11Context) Present() error {
	return wrapErr(c.swchain.Present(1, 0))
}

func wrapErr(err error) error {
	if err, ok := err.(d3d11.ErrorCode); ok {
		switch err.Code {
		case d3d11.DXGI_STATUS_OCCLUDED:
			// Ignore
			return nil
		case d3d11.DXGI_ERROR_DEVICE_RESET, d3d11.DXGI_ERROR_DEVICE_REMOVED, d3d11.D3DDDIERR_DEVICEREMOVED:
			return gpu.ErrDeviceLost
		}
	}
	return err
}

// patch acessos: recoverableErr é o wrapErr do caminho de resize (ResizeBuffers/GetBuffer/
// CreateRenderTargetView): qualquer erro aqui — mesmo um DXGI_ERROR_INVALID_CALL
// sem código tratado por wrapErr, visto na prática ao trocar de monitor (adaptador
// ou DPI diferente) no meio de um redesenho pesado — vira ErrDeviceLost em vez de
// matar a janela. window.go já sabe recuperar disso: destrói o contexto D3D11 e
// recria do zero no próximo quadro. Um erro cru aqui não tinha essa segunda chance
// e derrubava o app inteiro (era exatamente isto que causava a travada ao
// minimizar/trocar de tela relatada no Windows).
func recoverableErr(err error) error {
	if err == nil {
		return nil
	}
	if wrapped := wrapErr(err); wrapped == nil || errors.Is(wrapped, gpu.ErrDeviceLost) {
		return wrapped
	}
	return gpu.ErrDeviceLost
}

func (c *d3d11Context) Refresh() error {
	var width, height int
	_, width, height = c.win.HWND()
	if c.renderTarget != nil && width == c.width && height == c.height {
		return nil
	}
	// Minimizar (ou trocar de monitor/DPI no meio de um redesenho pesado,
	// que passa por um retângulo de cliente momentaneamente inválido) pode
	// entregar 0x0 aqui. ResizeBuffers com isso reflete o tamanho ATUAL da
	// janela (0x0), o que deixa o swapchain num estado que nada do resto
	// desta função sabe desenhar — mais vale pular o frame (o próximo
	// Refresh tenta de novo com um tamanho de verdade) do que arriscar um
	// resize inválido.
	if width == 0 || height == 0 {
		return errOutOfDate
	}
	c.releaseFBO()
	if err := c.swchain.ResizeBuffers(0, 0, 0, d3d11.DXGI_FORMAT_UNKNOWN, 0); err != nil {
		return recoverableErr(err)
	}
	c.width = width
	c.height = height

	backBuffer, err := c.swchain.GetBuffer(0, &d3d11.IID_Texture2D)
	if err != nil {
		return recoverableErr(err)
	}
	texture := (*d3d11.Resource)(unsafe.Pointer(backBuffer))
	renderTarget, err := c.dev.CreateRenderTargetView(texture)
	d3d11.IUnknownRelease(unsafe.Pointer(backBuffer), backBuffer.Vtbl.Release)
	if err != nil {
		return recoverableErr(err)
	}
	c.renderTarget = renderTarget
	return nil
}

func (c *d3d11Context) Lock() error {
	c.ctx.OMSetRenderTargets(c.renderTarget, nil)
	return nil
}

func (c *d3d11Context) Unlock() {}

func (c *d3d11Context) Release() {
	c.releaseFBO()
	if c.swchain != nil {
		d3d11.IUnknownRelease(unsafe.Pointer(c.swchain), c.swchain.Vtbl.Release)
	}
	if c.ctx != nil {
		d3d11.IUnknownRelease(unsafe.Pointer(c.ctx), c.ctx.Vtbl.Release)
	}
	if c.dev != nil {
		d3d11.IUnknownRelease(unsafe.Pointer(c.dev), c.dev.Vtbl.Release)
	}
	*c = d3d11Context{}
	if debugDirectX {
		d3d11.ReportLiveObjects()
	}
}

func (c *d3d11Context) releaseFBO() {
	if c.renderTarget != nil {
		d3d11.IUnknownRelease(unsafe.Pointer(c.renderTarget), c.renderTarget.Vtbl.Release)
		c.renderTarget = nil
	}
}
