// Package charts renders server-side SVG (go-chart v2). Contract per the
// security finding: labels are ALWAYS html-escaped here before they reach
// the SVG generator; chart output is served as image/svg+xml only, never
// inlined through template.HTML.
package charts

import (
	"bytes"
	"fmt"
	"html"

	"congress-visualizer/internal/analytics"

	"github.com/wcharczuk/go-chart"
	"github.com/wcharczuk/go-chart/drawing"
)

// PartyLayer is one party's vote composition for a votation.
type PartyLayer struct {
	Label   string // party short name — ESCAPED by PartyStack before charting
	Color   string // curated hex, validated upstream
	Yes     int64
	No      int64
	Abstain int64
}

// PartyStack renders a votation's party composition: one bar per party
// (yes+no+abstain cast votes). Output is a plain <svg> document, no
// scripts; served standalone by the chart route so API-derived strings
// never bypass the template escaper.
func PartyStack(layers []PartyLayer, width, height int) ([]byte, error) {
	if width <= 0 {
		width, height = 900, 420
	}
	if len(layers) == 0 {
		return []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><text x=\"10\" y=\"50\">sin datos</text></svg>"), nil
	}
	bars := make([]chart.Value, 0, len(layers))
	for _, l := range layers {
		bars = append(bars, chart.Value{
			Value: float64(l.Yes + l.No + l.Abstain),
			Label: esc(l.Label),
			Style: chart.Style{Show: true, FillColor: hexToColor(l.Color)},
		})
	}
	// go-chart derives the y-range from data min/max; uniform vote counts
	// (every party with the same total) produce a zero delta and Render
	// fails. Pin a zero-based range with at least one unit of headroom.
	var maxV float64
	for _, l := range layers {
		if v := float64(l.Yes + l.No + l.Abstain); v > maxV {
			maxV = v
		}
	}
	bc := chart.BarChart{
		Title:      fmt.Sprintf("Votos por partido (%d)", len(layers)),
		TitleStyle: chart.StyleShow(),
		Width:      width,
		Height:     height,
		BarWidth:   40,
		XAxis:      chart.StyleShow(),
		YAxis: chart.YAxis{
			Style: chart.StyleShow(),
			Range: &chart.ContinuousRange{Min: 0, Max: maxV + 1},
		},
		Bars: bars,
	}
	var buf bytes.Buffer
	if err := bc.Render(chart.SVG, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// TrendLine renders a monthly series (chronological points, fixed 0..1
// scale) as a single line — used for party cohesion trends. Month labels go
// on sparse X ticks; labels are literal 'YYYY-MM' strings we generate, not
// API-derived text.
func TrendLine(points []analytics.TrendPoint, color string, width, height int) ([]byte, error) {
	if width <= 0 {
		width, height = 900, 320
	}
	if len(points) == 0 {
		return []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"><text x=\"10\" y=\"50\">sin datos</text></svg>"), nil
	}
	x := make([]float64, len(points))
	y := make([]float64, len(points))
	ticks := []chart.Tick{}
	for i, p := range points {
		x[i] = float64(i)
		y[i] = p.Score
		if isLabelTick(i, len(points)) {
			ticks = append(ticks, chart.Tick{Value: float64(i), Label: p.Month})
		}
	}
	c := chart.Chart{
		Title:      "Cohesión mensual",
		TitleStyle: chart.StyleShow(),
		Width:      width,
		Height:     height,
		XAxis: chart.XAxis{
			Style: chart.StyleShow(),
			Ticks: ticks,
		},
		YAxis: chart.YAxis{
			Style: chart.StyleShow(),
			Range: &chart.ContinuousRange{Min: 0, Max: 1},
		},
		Series: []chart.Series{chart.ContinuousSeries{
			Style: chart.Style{
				Show:        true,
				StrokeColor: hexToColor(color),
				StrokeWidth: 2,
			},
			XValues: x,
			YValues: y,
		}},
	}
	var buf bytes.Buffer
	if err := c.Render(chart.SVG, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isLabelTick(i, n int) bool {
	if n <= 10 {
		return true
	}
	step := (n + 9) / 10
	return i%step == 0
}

func esc(s string) string { return html.EscapeString(s) }

// hexToColor parses a curated "#RRGGBB" into a chart color; any bad value
// maps to grey (validated upstream, this is a render-safety fallback).
func hexToColor(color string) drawing.Color {
	if len(color) != 7 || color[0] != '#' {
		return drawing.Color{R: 128, G: 128, B: 128, A: 255}
	}
	n := func(i int) uint8 {
		var v uint8
		for _, c := range color[i : i+2] {
			switch {
			case c >= '0' && c <= '9':
				v = 16*v + uint8(c-'0')
			case c >= 'a' && c <= 'f':
				v = 16*v + uint8(c-'a'+10)
			case c >= 'A' && c <= 'F':
				v = 16*v + uint8(c-'A'+10)
			default:
				return 128
			}
		}
		return v
	}
	return drawing.Color{R: n(1), G: n(3), B: n(5), A: 255}
}
