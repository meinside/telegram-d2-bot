// raster_test.go

package main

import (
	"bytes"
	"context"
	"testing"
)

// The image and font budgets in raster.go are derived from each other by
// subtraction, and imageasset/d2raster reject any non-positive limit. A
// mis-scaled constant is invisible until a message is rendered, so build the
// bounded options here.
func TestAssetOptionsLimitsArePositive(t *testing.T) {
	if _, err := assetOptions(); err != nil {
		t.Fatalf("assetOptions() failed: %s", err)
	}
}

func TestFontOptionsLimitsArePositive(t *testing.T) {
	if _, err := newFontOptions(); err != nil {
		t.Fatalf("newFontOptions() failed: %s", err)
	}
}

func TestRenderSessionLimitsArePositive(t *testing.T) {
	if _, err := sharedRenderSession(); err != nil {
		t.Fatalf("sharedRenderSession() failed: %s", err)
	}
}

// renders a plain diagram end to end: compile, layout, export, rasterize.
func TestRenderDiagramReturnsPNG(t *testing.T) {
	bs, err := renderDiagram(context.Background(), config{}, "a -> b -> c -> a")
	if err != nil {
		t.Fatalf("renderDiagram() failed: %s", err)
	}

	pngMagic := []byte("\x89PNG\r\n\x1a\n")
	if !bytes.HasPrefix(bs, pngMagic) {
		t.Errorf("rendered %d bytes which are not a .png", len(bs))
	}
}

func TestRenderDiagramSketchReturnsPNG(t *testing.T) {
	bs, err := renderDiagram(
		context.Background(),
		config{Sketch: true},
		"a -> b: hello",
	)
	if err != nil {
		t.Fatalf("renderDiagram() with sketch failed: %s", err)
	}

	pngMagic := []byte("\x89PNG\r\n\x1a\n")
	if !bytes.HasPrefix(bs, pngMagic) {
		t.Errorf("rendered %d bytes which are not a .png", len(bs))
	}
}

// The render session and font resolver are process-wide, so a second message
// must render as well as the first one.
func TestRenderDiagramRepeatedly(t *testing.T) {
	for i := range 3 {
		bs, err := renderDiagram(context.Background(), config{}, "a -> b -> c -> a")
		if err != nil {
			t.Fatalf("renderDiagram() #%d failed: %s", i, err)
		}
		if len(bs) == 0 {
			t.Fatalf("renderDiagram() #%d returned no bytes", i)
		}
	}
}

// A font fallback resolver's budgets are cumulative over its own lifetime, so
// fontOptions() reuses one for fontResolverRenders renders and then builds a
// fresh one instead of exhausting it.
func TestFontOptionsAreSharedThenRotated(t *testing.T) {
	fontOptionsMutex.Lock()
	sharedFontOptions, sharedFontRenders = nil, 0
	fontOptionsMutex.Unlock()

	first, err := fontOptions()
	if err != nil {
		t.Fatalf("fontOptions() failed: %s", err)
	}

	for i := 1; i < fontResolverRenders; i++ {
		again, err := fontOptions()
		if err != nil {
			t.Fatalf("fontOptions() #%d failed: %s", i, err)
		}
		if again != first {
			t.Fatalf("fontOptions() rebuilt the resolver after %d renders, want %d", i, fontResolverRenders)
		}
	}

	rotated, err := fontOptions()
	if err != nil {
		t.Fatalf("fontOptions() after rotation failed: %s", err)
	}
	if rotated == first {
		t.Errorf("fontOptions() kept one resolver for more than %d renders", fontResolverRenders)
	}
}
