// raster.go
//
// Converts a laid-out d2 diagram into .png bytes with d2's built-in pure-Go
// raster renderer (d2 >= v0.9.0). It replaces the former Playwright/Chromium
// screenshot of the .svg output, so the bot has no browser dependency.
//
// d2raster and d2scenebuild intentionally have no default limits: every
// ceiling below must be positive and is chosen by the caller. The values are
// scaled-down versions of the ones d2's own CLI uses in d2cli/raster.go; they
// bound what a single (untrusted) Telegram message may make this process
// allocate.

package main

import (
	"context"
	"fmt"
	"image/color"
	"sync"

	"github.com/d2lang/d2/d2renderers/d2fonts"
	"github.com/d2lang/d2/d2renderers/d2raster"
	"github.com/d2lang/d2/d2renderers/d2scene"
	"github.com/d2lang/d2/d2renderers/d2scenebuild"
	"github.com/d2lang/d2/d2renderers/d2svgimport"
	"github.com/d2lang/d2/d2target"
	"github.com/d2lang/d2/lib/imageasset"
)

const (
	// device scale of the rendered frame (2 = retina-ish, readable in chat)
	rasterScale = 2.0

	// frame geometry
	//
	// These are ceilings, not reservations: exceeding one replies with an error
	// message, while a ceiling above what the host can allocate ends in an OOM
	// kill. They are therefore sized for a small VPS and raised only when a real
	// diagram is rejected. rasterMaxPixels dominates peak memory because the
	// final frame is 4 bytes per pixel: 12Mi pixels = 48 MiB.
	rasterMaxDimension       = 8_192 // admits long, narrow diagrams
	rasterMaxPixels    int64 = 12 * 1024 * 1024

	// scene structure
	rasterMaxNodes        = 200_000
	rasterMaxDepth        = 256
	rasterMaxPathCommands = 2_000_000

	// text shaping
	rasterMaxTextRunesPerRun          = 100_000
	rasterMaxFontFacesPerText         = 64
	rasterMaxTextCoverageChecks int64 = 10_000_000
	rasterMaxTextShapingRuns          = 100_000

	// static .png output has no animation, but the ceilings must still be positive
	rasterMaxAnimationTracks    = 10_000
	rasterMaxAnimationKeyframes = 100_000

	// images and icons referenced by the diagram
	rasterMaxAssets = 256 // including the font faces reserved below
	// NOTE: rasterMaxAssetBytes covers fonts *and* images, so it must stay
	// larger than fontAssetByteReserve: the image budget is the difference
	// between them, and imageasset.Limits rejects a non-positive one.
	rasterMaxAssetBytes        int64 = fontAssetByteReserve + imageAssetMaxBytes
	rasterMaxDecodedAssetBytes int64 = 32 * 1024 * 1024
	// Icons and embedded images in real diagrams are a few KB each; these are
	// generous against that corpus while bounding retained bytes.
	imageAssetMaxBytes     int64 = 16 * 1024 * 1024 // cumulative, encoded
	imageAssetMaxBytesEach int64 = 4 * 1024 * 1024

	// transient rendering work: scratch layers, masks and pattern tiles, whose
	// live size tracks the frame rather than exceeding it by a wide margin
	rasterMaxOffscreenBytes  int64 = 64 * 1024 * 1024
	rasterMaxEvenOddClipWork int64 = 100_000_000
	rasterMaxScanlineWork    int64 = 1_000_000_000

	// tooltip/link badges painted into the .png
	rasterMaxLinkRegions     = 1_024
	rasterMaxLinkStringBytes = 256 * 1024

	// font fallback discovery (for scripts absent from d2's bundled fonts,
	// e.g. Hangul or CJK: the host needs a font such as Noto Sans CJK installed)
	//
	// fontAssetByteReserve stays well above the image budget on purpose: one
	// Noto Sans CJK collection is around 20 MB and several styles may resolve.
	fontAssetReserve                 = 64
	fontAssetByteReserve       int64 = 64 * 1024 * 1024
	fontMaxCoverageChecks      int64 = 50_000_000
	fontSearchDirectoryEntries       = 20_000
	fontSearchFiles                  = 4_096
	fontSearchFaces                  = 8_192
	fontSearchFileBytes        int64 = 32 * 1024 * 1024
	fontSearchScannedBytes     int64 = 256 * 1024 * 1024
	fontBundledCopyBytes       int64 = 8 * 1024 * 1024

	// imported .svg assets (icons, MathJax output)
	svgAssetMaxBytes              int64 = 64 * 1024
	svgAssetMaxDepth                    = 32
	svgAssetMaxElements                 = 256
	svgAssetMaxAttributes               = 512
	svgAssetMaxAttributeBytes           = 64 * 1024
	svgAssetMaxPathCommands             = 4_096
	svgAssetMaxTransformFunctions       = 128
	svgAssetMaxUseDepth                 = 8
	svgAssetMaxResources                = 64

	svgDocumentMaxSourceBytes          = 1 * 1024 * 1024
	svgDocumentMaxElements             = 4_096
	svgDocumentMaxAttributes           = 8_192
	svgDocumentMaxAttributeBytes       = 1 * 1024 * 1024
	svgDocumentMaxPathCommands         = 65_536
	svgDocumentMaxTransformFunctions   = 2_048
	svgDocumentMaxDeclaredResources    = 1_024
	svgDocumentMaxExpandedUseInstances = 1_024

	// A render session keeps parsed font faces and decoded images between
	// renders, which matters most for a CJK fallback face: parsing a ~20 MB
	// collection on every message is the single most expensive repeated step.
	// The cache is retained memory, so it is bounded well under what the
	// per-render ceilings above already admit.
	renderCacheEntries       = 512
	renderCacheBytes   int64 = 96 * 1024 * 1024
	renderCacheLoads         = 1

	// A font fallback resolver's limits are cumulative over its own lifetime,
	// not per render, so one kept forever would eventually refuse to resolve
	// anything and Hangul labels would silently lose their glyphs. Rotating it
	// every fontResolverRenders renders amortizes host font discovery while
	// keeping each resolver's budget a meaningful ceiling. Retained bytes stay
	// bounded per scene by FontFallbackOptions.MaxBytes regardless.
	fontResolverRenders = 32
)

var (
	renderSessionOnce sync.Once
	renderSession     *d2raster.RenderSession
	renderSessionErr  error

	fontOptionsMutex  sync.Mutex
	sharedFontOptions *d2scenebuild.FontFallbackOptions
	sharedFontRenders int
)

// renders given diagram into .png bytes
func renderPNG(
	ctx context.Context,
	conf config,
	diagram *d2target.Diagram,
) (bs []byte, err error) {
	var document *d2scene.Document
	if document, err = buildScene(ctx, conf, diagram); err != nil {
		return nil, err
	}

	session, err := sharedRenderSession()
	if err != nil {
		return nil, err
	}

	frame, err := session.Render(ctx, document, d2raster.FrameOptions{
		Scale:                 rasterScale,
		Background:            color.White,
		MaxWidth:              rasterMaxDimension,
		MaxHeight:             rasterMaxDimension,
		MaxPixels:             rasterMaxPixels,
		MaxNodes:              rasterMaxNodes,
		MaxDepth:              rasterMaxDepth,
		MaxPathCommands:       rasterMaxPathCommands,
		MaxTextRunesPerRun:    rasterMaxTextRunesPerRun,
		MaxFontFacesPerText:   rasterMaxFontFacesPerText,
		MaxTextCoverageChecks: rasterMaxTextCoverageChecks,
		MaxTextShapingRuns:    rasterMaxTextShapingRuns,
		MaxAnimationTracks:    rasterMaxAnimationTracks,
		MaxAnimationKeyframes: rasterMaxAnimationKeyframes,
		MaxAssets:             rasterMaxAssets,
		MaxAssetBytes:         rasterMaxAssetBytes,
		MaxDecodedAssetBytes:  rasterMaxDecodedAssetBytes,
		MaxImportDepth:        rasterMaxDepth,
		MaxOffscreenBytes:     rasterMaxOffscreenBytes,
		MaxEvenOddClipWork:    rasterMaxEvenOddClipWork,
		MaxScanlineWork:       rasterMaxScanlineWork,
	})
	if err != nil {
		return nil, err
	}

	return d2raster.EncodePNG(ctx, frame)
}

// returns the process-wide render session, which reuses parsed fonts and
// decoded images across renders. It is safe for concurrent use, and applies
// every per-render limit in FrameOptions independently on each call.
func sharedRenderSession() (*d2raster.RenderSession, error) {
	renderSessionOnce.Do(func() {
		renderSession, renderSessionErr = d2raster.NewRenderSession(d2raster.RenderSessionOptions{
			MaxCacheEntries:    renderCacheEntries,
			MaxCacheBytes:      renderCacheBytes,
			MaxConcurrentLoads: renderCacheLoads,
		})
		if renderSessionErr != nil {
			renderSessionErr = fmt.Errorf("failed to initialize render session: %w", renderSessionErr)
		}
	})

	return renderSession, renderSessionErr
}

// builds a network-free scene from given diagram
func buildScene(
	ctx context.Context,
	conf config,
	diagram *d2target.Diagram,
) (*d2scene.Document, error) {
	if diagram == nil {
		return nil, fmt.Errorf("no diagram to rasterize")
	}

	assets, err := assetOptions()
	if err != nil {
		return nil, err
	}

	fonts, err := fontOptions()
	if err != nil {
		return nil, err
	}

	// NOTE: render only this board; nested boards would be composed into one frame
	board := *diagram
	board.Layers = nil
	board.Scenarios = nil
	board.Steps = nil

	return d2scenebuild.Build(ctx, &board, d2scenebuild.Options{
		Pad:             toPointer(renderPadding),
		Scale:           toPointer(1.0), // 1:1 logical size; pixels come from FrameOptions.Scale
		ThemeID:         toPointer(conf.ThemeID),
		MaxNodes:        rasterMaxNodes,
		MaxPathCommands: rasterMaxPathCommands,
		Sketch:          conf.Sketch,
		SketchBudget: d2scenebuild.SketchBudget{
			MaxOperationSets: rasterMaxNodes,
			MaxOperations:    rasterMaxPathCommands,
			MaxPathCommands:  rasterMaxPathCommands,
		},
		LinkBudget: d2scenebuild.LinkBudget{
			MaxRegions:     rasterMaxLinkRegions,
			MaxStringBytes: rasterMaxLinkStringBytes,
		},
		Appendix: true, // paint tooltip/link badges, as d2's own .png export does
		Assets:   assets,
		Fonts:    fonts,
	})
}

// bounded image/icon loading for one rendering
//
// NOTE: d2 v0.9.0's resolver has no local-file or network policy, so a diagram
// may reference host paths and get them embedded into the rendered image.
// BaseDir only affects relative paths; absolute ones are still readable. The
// bot's `allowed_ids` allowlist is what keeps that input semi-trusted.
//
// TODO: when d2 releases `lib/localfile` and `lib/netpolicy` (already on
// master), pass `LocalFiles: localfile.Policy{}` (zero value = deny every
// local file) and `NetworkPolicy: netpolicy.Policy{}` (public addresses only)
// here, so diagram sources can no longer read host files.
func assetOptions() (*d2scenebuild.AssetOptions, error) {
	resolver, err := imageasset.New(imageasset.Options{
		Limits: imageasset.Limits{
			MaxFetchedBytes:           imageAssetMaxBytesEach,
			MaxEncodedBytes:           imageAssetMaxBytesEach,
			MaxDecompressedBytes:      imageAssetMaxBytesEach,
			MaxSVGBytes:               svgAssetMaxBytes,
			MaxDecodedWidth:           rasterMaxDimension,
			MaxDecodedHeight:          rasterMaxDimension,
			MaxDecodedPixels:          rasterMaxPixels,
			MaxAssets:                 rasterMaxAssets - fontAssetReserve,
			MaxCumulativeEncodedBytes: imageAssetMaxBytes,
			MaxCumulativeDecodedBytes: rasterMaxDecodedAssetBytes,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize image resolver: %w", err)
	}

	return &d2scenebuild.AssetOptions{
		Resolver: resolver,
		SVGImportLimits: d2svgimport.Limits{
			MaxBytes:              int(svgAssetMaxBytes),
			MaxDepth:              svgAssetMaxDepth,
			MaxElements:           svgAssetMaxElements,
			MaxAttributes:         svgAssetMaxAttributes,
			MaxAttributeBytes:     svgAssetMaxAttributeBytes,
			MaxPathCommands:       svgAssetMaxPathCommands,
			MaxTransformFunctions: svgAssetMaxTransformFunctions,
			MaxUseDepth:           svgAssetMaxUseDepth,
			MaxResources:          svgAssetMaxResources,
		},
		SVGImportBudget: d2scenebuild.SVGImportBudget{
			MaxSourceBytes:          svgDocumentMaxSourceBytes,
			MaxElements:             svgDocumentMaxElements,
			MaxAttributes:           svgDocumentMaxAttributes,
			MaxAttributeBytes:       svgDocumentMaxAttributeBytes,
			MaxPathCommands:         svgDocumentMaxPathCommands,
			MaxTransformFunctions:   svgDocumentMaxTransformFunctions,
			MaxDeclaredResources:    svgDocumentMaxDeclaredResources,
			MaxExpandedUseInstances: svgDocumentMaxExpandedUseInstances,
		},
	}, nil
}

// font fallback for runes which d2's bundled fonts don't cover (eg. Hangul)
//
// The resolver is shared between renders so host font discovery and file reads
// are not repeated for every message, and rotated every fontResolverRenders
// renders because its limits are cumulative over one resolver's lifetime.
func fontOptions() (*d2scenebuild.FontFallbackOptions, error) {
	fontOptionsMutex.Lock()
	defer fontOptionsMutex.Unlock()

	if sharedFontOptions == nil || sharedFontRenders >= fontResolverRenders {
		options, err := newFontOptions()
		if err != nil {
			return nil, err
		}
		sharedFontOptions, sharedFontRenders = options, 0
	}
	sharedFontRenders++

	return sharedFontOptions, nil
}

// builds one font fallback resolver, whose work and byte budgets cover
// fontResolverRenders renders. Only the cumulative ones are multiplied:
// MaxFileBytes bounds a single candidate file, and the MaxAssets/MaxBytes
// below bound what one scene retains, so both stay per-render values.
func newFontOptions() (*d2scenebuild.FontFallbackOptions, error) {
	system, err := d2fonts.NewSystemFallbackResolver(d2fonts.SystemFallbackLimits{
		MaxDirectoryEntries: fontSearchDirectoryEntries * fontResolverRenders,
		MaxFiles:            fontSearchFiles * fontResolverRenders,
		MaxFaces:            fontSearchFaces * fontResolverRenders,
		MaxRequestedRunes:   rasterMaxTextRunesPerRun * fontResolverRenders,
		MaxCoverageChecks:   fontMaxCoverageChecks * fontResolverRenders,
		MaxFileBytes:        fontSearchFileBytes,
		MaxScannedBytes:     fontSearchScannedBytes * fontResolverRenders,
		MaxResolvedBytes:    fontAssetByteReserve * fontResolverRenders,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize font fallback resolver: %w", err)
	}

	// resolves emoji/symbols from d2's bundled face first, host fonts after
	bundled, err := d2fonts.NewBundledFallbackResolver(system, d2fonts.BundledFallbackLimits{
		MaxRequestedRunes: rasterMaxTextRunesPerRun * fontResolverRenders,
		MaxBundledBytes:   fontBundledCopyBytes,
		MaxResolvedBytes:  (fontBundledCopyBytes + fontAssetByteReserve) * fontResolverRenders,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize bundled font fallback resolver: %w", err)
	}

	return &d2scenebuild.FontFallbackOptions{
		Resolver:            bundled,
		MaxAssets:           fontAssetReserve,
		MaxBytes:            fontAssetByteReserve,
		MaxRunesPerText:     rasterMaxTextRunesPerRun,
		MaxTotalRunes:       rasterMaxPathCommands,
		MaxCoverageChecks:   fontMaxCoverageChecks,
		MaxFontFacesPerText: rasterMaxFontFacesPerText,
		MaxShapingRuns:      rasterMaxTextShapingRuns,
		MaxShapedGlyphs:     rasterMaxPathCommands,
	}, nil
}
